package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"nusashell/application/internal/service/learnedparams"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// textOnlyThenOKProvider returns a 400 "text-only" UpstreamError on the
// first Stream call, then succeeds on the second. It records every request
// so the test can assert the retry actually happened and that the image
// was stripped after the 400-learning disabled Vision.
type textOnlyThenOKProvider struct {
	mu       sync.Mutex
	calls    int
	requests []*core.Request
}

func (p *textOnlyThenOKProvider) Name() string { return "text-only-then-ok" }

func (p *textOnlyThenOKProvider) Stream(_ context.Context, req *core.Request) (core.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, req)
	if p.calls == 1 {
		return nil, &UpstreamError{
			StatusCode: 400,
			Err:        errors.New(`Qwen3.8 open checkpoint is text-only; messages[0].content[1] must be a text part`),
		}
	}
	resp := &core.Response{
		Blocks:       []core.Block{core.TextBlock{Text: "ok after learning"}},
		FinishReason: core.FinishReasonStop,
	}
	return &stubStream{events: coreResponseEvents(resp)}, nil
}

func (p *textOnlyThenOKProvider) Chat(context.Context, *core.Request) (*core.Response, error) {
	return nil, errors.New("chat not used")
}

// TestStreamTurnRoundRetriesAfterLearnable400 proves that a learnable 400
// (text-only model rejecting an image) triggers an in-turn retry after the
// 400-learning classifier disables Vision and the request is rebuilt with
// the image stripped. Without the retry, the turn fails immediately on the
// first 400 even though the learning already recorded the fix.
func TestStreamTurnRoundRetriesAfterLearnable400(t *testing.T) {
	provider := &textOnlyThenOKProvider{}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "describe this", Attachments: []domain.Attachment{
				{Type: "image", Name: "pic.png", FilePath: "/tmp/pic.png", MediaType: "image/png", DataURL: "data:image/png;base64,ZmFrZQ=="},
			}},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Toolbox:       &recordingToolbox{},
		Bus:           NewBus(),
		learnedParams: learnedparams.New(&fakeLearnedParamStore{}),
		retrySleeper:  func(context.Context, time.Duration) error { return nil },
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", ProviderID: "9router", Ctx: context.Background()}
	caps := ModelCapabilities{Vision: true}

	res, err := app.streamTurnRound(run, stubProviderContext(provider), conv, "a1", "qwen/qwen3.8-max-free", "", nil, domain.Settings{}, false, 100, nil, caps, 1)
	if err != nil {
		t.Fatalf("streamTurnRound returned error after learnable 400: %v (expected retry to succeed)", err)
	}
	if res.Content != "ok after learning" {
		t.Fatalf("content = %q, want %q", res.Content, "ok after learning")
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (initial 400 + retry after learning)", provider.calls)
	}
	// The retry request must not carry the image block: Vision was
	// disabled by the 400-learning, so chatMessages should have stripped
	// the image attachment and replaced it with a read_media placeholder.
	if requestHasImageBlock(provider.requests[1]) {
		t.Fatal("retry request still contains an image block; 400-learning should have stripped it after disabling Vision")
	}
}

// requestHasImageBlock reports whether any message in the request carries
// an image block.
func requestHasImageBlock(req *core.Request) bool {
	if req == nil {
		return false
	}
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if _, ok := b.(core.ImageBlock); ok {
				return true
			}
		}
	}
	return false
}
