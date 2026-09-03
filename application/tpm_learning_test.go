package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/application/service/learnedparams"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// tpmRejectionBody is the field-observed OpenAI Responses API rejection
// (in-stream SSE error): the request fits the raw limit but consumes more
// than half the per-minute budget, so it keeps colliding with whatever is
// already used in the window.
const tpmRejectionBody = "openai: stream error: Rate limit reached for gpt-5.6-luna in organization org-sFd4kjWg5yerL8fwlgAerbgo on tokens per min (TPM): Limit 500000, Used 241004, Requested 355391. Please try again in 11.567s. Visit https://platform.openai.com/account/rate-limits to learn more. kind=sse_transport"

func TestTPMContextCap(t *testing.T) {
	cases := []struct {
		limit, maxOutput, want int
	}{
		{500000, 65536, 184464},  // 500k/2 − 65k output; floor 125k does not bind
		{200000, 65536, 50000},   // 100k − 65k < floor 50k → floor binds
		{500000, 300000, 125000}, // output alone exceeds half → floor 125k
		{100000, 65536, 25000},
		{30000, 65536, 7500},
		{0, 65536, 0},
	}
	for _, tc := range cases {
		if got := tpmContextCap(tc.limit, tc.maxOutput); got != tc.want {
			t.Errorf("tpmContextCap(%d, %d) = %d, want %d", tc.limit, tc.maxOutput, got, tc.want)
		}
	}
}

// TestLearnTPMContextCapWiresIntoContextWindow: learning from a dominant TPM
// rejection must shrink the effective context window used for compaction
// decisions, which makes every future request on that provider+model smaller.
func TestLearnTPMContextCapWiresIntoContextWindow(t *testing.T) {
	app := &App{
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		Logs:          &fakeLogStore{},
	}
	run := &TurnRun{ID: "r1", ProviderID: "openai", Ctx: context.Background()}
	err := &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: errors.New(tpmRejectionBody)}

	if !app.learnTPMContextCap(run, "gpt-5.6-luna", err, 65536) {
		t.Fatal("dominant TPM rejection must learn a context cap")
	}
	// Idempotent: the same rejection must not re-learn (cap unchanged).
	if app.learnTPMContextCap(run, "gpt-5.6-luna", err, 65536) {
		t.Fatal("repeat observation must not re-learn")
	}

	provider := &domain.Provider{ID: "openai", Models: []domain.Model{{ID: "gpt-5.6-luna", Context: 1000000}}}
	settings := domain.DefaultSettings()
	settings.MaxInputTokens = 1000000
	if got := app.resolveContextWindow(provider, "gpt-5.6-luna", settings); got != 184464 {
		t.Fatalf("resolveContextWindow = %d, want 184464 (TPM cap overrides catalog)", got)
	}
}

// TestLearnTPMContextCapSkipsModestRequests: congestion (Used high, request
// small) must not shrink the window — waiting is the right fix there.
func TestLearnTPMContextCapSkipsModestRequests(t *testing.T) {
	app := &App{
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		Logs:          &fakeLogStore{},
	}
	run := &TurnRun{ID: "r1", ProviderID: "openai", Ctx: context.Background()}
	modest := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, RetryAfter: 30 * time.Second,
		Err: errors.New("Rate limit reached for gpt-5.6-luna on tokens per min (TPM): Limit 500000, Used 450000, Requested 40000.")}
	if app.learnTPMContextCap(run, "gpt-5.6-luna", modest, 65536) {
		t.Fatal("modest request must not learn a cap (congestion, not size)")
	}
	if got := app.learnedParams.ContextCap("openai", "gpt-5.6-luna"); got != 0 {
		t.Fatalf("ContextCap = %d, want 0 (nothing learned)", got)
	}
}

// tpmDominatedProvider always rejects with the field-observed TPM body.
type tpmDominatedProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *tpmDominatedProvider) Name() string { return "tpm-dominated" }

func (p *tpmDominatedProvider) Stream(_ context.Context, _ *core.Request) (core.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return nil, &domain.ProviderError{Kind: domain.KindSSETransport, Temporary: true, Err: errors.New(tpmRejectionBody)}
}

func (p *tpmDominatedProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

// TestStreamTurnRoundBailsToCompactionOnDominatedTPM proves the retry loop
// does not burn provider attempts on a dominant TPM rejection: it returns
// the error on the first attempt (no backoff sleep) so the emergency
// compaction hook can shrink the context and retry the round, and it learns
// the context cap for subsequent rounds/conversations.
func TestStreamTurnRoundBailsToCompactionOnDominatedTPM(t *testing.T) {
	provider := &tpmDominatedProvider{}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "do the thing"},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	sleeps := 0
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		Logs:          &fakeLogStore{},
		retrySleeper: func(context.Context, time.Duration) error {
			sleeps++
			return nil
		},
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", ProviderID: "openai", Ctx: context.Background()}

	_, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "gpt-5.6-luna", "", nil, domain.Settings{}, false, 65536, nil, ModelCapabilities{}, 1)
	if err == nil {
		t.Fatal("dominated TPM must surface the provider error (compaction hook handles it)")
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (no futile retries)", provider.calls)
	}
	if sleeps != 0 {
		t.Fatalf("backoff sleeps = %d, want 0 (bail straight to compaction)", sleeps)
	}
	if got := app.learnedParams.ContextCap("openai", "gpt-5.6-luna"); got != 184464 {
		t.Fatalf("learned ContextCap = %d, want 184464", got)
	}
}

// TestFriendlyRateLimitMessageShowsTPMAccounting: the 429 surface for a
// dominant TPM rejection must quote the provider's own numbers (limit, used,
// requested) so the user understands the request must shrink, not that they
// should wait.
func TestFriendlyRateLimitMessageShowsTPMAccounting(t *testing.T) {
	app := &App{Logs: &fakeLogStore{}}
	upstream := &domain.ProviderError{
		Kind:       domain.KindSSETransport,
		StatusCode: 429,
		Err:        errors.New(tpmRejectionBody),
	}
	err := app.friendlyRateLimitError("openai", upstream, 30*time.Second)
	msg := err.Error()
	for _, want := range []string{"500000", "241004", "355391", "compacted"} {
		if !strings.Contains(msg, want) {
			t.Errorf("friendly message missing %q: %s", want, msg)
		}
	}
	// A request-count (RPM) limit without TPM numbers keeps the generic
	// message with the window hint.
	rpm := &domain.ProviderError{Kind: domain.KindHTTPStatus, StatusCode: 429, Err: errors.New("rate limited")}
	if msg := app.friendlyRateLimitError("openai", rpm, 0).Error(); strings.Contains(msg, "500000") {
		t.Errorf("RPM message must not quote TPM numbers: %s", msg)
	}
}
