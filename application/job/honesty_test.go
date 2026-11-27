package job

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
)

func shortenBackoff(t *testing.T, steps ...time.Duration) {
	t.Helper()
	prev := append([]time.Duration(nil), sendRetryBackoff...)
	sendRetryBackoff = steps
	resetAgentHonesty()
	t.Cleanup(func() {
		sendRetryBackoff = prev
		resetAgentHonesty()
	})
}

type honestAgent struct {
	dservice.AgentRuntime
	mu    sync.Mutex
	calls []struct {
		conv string
		at   time.Time
	}
	fn func(conv string, n int, onEvent event.TurnEventHandler) error
}

func (a *honestAgent) Prompt(_ context.Context, p dservice.AgentPrompt, onEvent event.TurnEventHandler, _ dservice.Approver) error {
	a.mu.Lock()
	n := 0
	for _, c := range a.calls {
		if c.conv == p.Conversation.ID() {
			n++
		}
	}
	a.calls = append(a.calls, struct {
		conv string
		at   time.Time
	}{p.Conversation.ID(), time.Now()})
	a.mu.Unlock()
	if a.fn == nil {
		return nil
	}
	return a.fn(p.Conversation.ID(), n, onEvent)
}

func (a *honestAgent) Steer(context.Context, string, valueobject.UserInput, string, func()) bool {
	return false
}

func (a *honestAgent) callCount(conv string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.calls {
		if c.conv == conv {
			n++
		}
	}
	return n
}

func (a *honestAgent) firstAt(conv string) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.calls {
		if c.conv == conv {
			return c.at
		}
	}
	return time.Time{}
}

func honestDeps(t *testing.T, rt dservice.AgentRuntime) Deps {
	t.Helper()
	d := hydrationDeps(t, nil)
	agent, err := aggregate.NewAgent("claude", "Claude", "claude", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	agents := repoagent.New(t.TempDir(), nil)
	if err := agents.Save(t.Context(), &agent); err != nil {
		t.Fatal(err)
	}
	d.Agents = agents
	d.AgentRuntime = rt
	return d
}

func runHonest(t *testing.T, d Deps, room string, onEvent event.TurnEventHandler) error {
	t.Helper()
	job := &Send{
		Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(),
		Message: "hello", Opts: SendOptions{Agent: "claude"}, OnEvent: onEvent,
	}
	job.Bind(room, nil)
	return job.Execute(t.Context())
}

func rateLimited(msg string, delay time.Duration) error {
	return apperror.NewAgentFailure(apperror.AgentClassRateLimited, 429, "upstream_error", msg, nil, delay)
}

func TestRateLimitedRetriesTwiceThenStops(t *testing.T) {
	shortenBackoff(t, time.Millisecond, time.Millisecond)
	rt := &honestAgent{fn: func(string, int, event.TurnEventHandler) error {
		return rateLimited("rate limit exceeded", 0)
	}}
	d := honestDeps(t, rt)
	var waits int
	err := runHonest(t, d, "room-a", func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventStatus && ev.Status() == enum.TurnStatusRetryWait {
			waits++
		}
	})
	if apperror.As(err).StatusCode() != 429 {
		t.Fatalf("turn = %v, want the rate limit", err)
	}
	if rt.callCount("room-a") != 3 {
		t.Fatalf("prompts = %d, want the call plus two retries", rt.callCount("room-a"))
	}
	if waits != 2 {
		t.Fatalf("retry pauses = %d, want 2", waits)
	}
	hold, ok := CurrentAgentHold("room-a")
	if !ok || hold.Class != apperror.AgentClassRateLimited || hold.Retry != "none" || hold.RetryAt != 0 {
		t.Fatalf("hold = %+v ok=%v", hold, ok)
	}
	if hold.Message == "agent turn failed" || hold.Message == "unknown error" || hold.Message == "" {
		t.Fatalf("message = %q", hold.Message)
	}
	rt.fn = func(_ string, _ int, _ event.TurnEventHandler) error { return nil }
	if err := runHonest(t, d, "room-a", nil); err != nil {
		t.Fatalf("later turn = %v", err)
	}
	if _, ok := CurrentAgentHold("room-a"); ok {
		t.Fatal("a completed turn left the hold in place")
	}
}

func TestRateLimitedDoesNotRetryAfterAnUpdate(t *testing.T) {
	shortenBackoff(t, time.Millisecond, time.Millisecond)
	rt := &honestAgent{fn: func(_ string, _ int, on event.TurnEventHandler) error {
		if on != nil {
			on(event.NewClockEvent("working", 10, 25))
		}
		return rateLimited("rate limit exceeded", 0)
	}}
	d := honestDeps(t, rt)
	if err := runHonest(t, d, "room-upd", nil); apperror.As(err).StatusCode() != 429 {
		t.Fatalf("turn = %v", err)
	}
	if rt.callCount("room-upd") != 1 {
		t.Fatalf("prompts = %d, want one — an update already landed", rt.callCount("room-upd"))
	}
	hold, ok := CurrentAgentHold("room-upd")
	if !ok || hold.Retry != "none" || hold.Class != apperror.AgentClassRateLimited {
		t.Fatalf("hold = %+v ok=%v", hold, ok)
	}
}

func TestOtherAgentClassesDoNotRetry(t *testing.T) {
	shortenBackoff(t, time.Millisecond, time.Millisecond)
	cases := []struct {
		room  string
		class string
		err   error
		code  string
		at    bool
	}{
		{"room-q", apperror.AgentClassQuotaExhausted, apperror.NewAgentFailure(apperror.AgentClassQuotaExhausted, 402, "quota_exhausted", "quota exhausted", nil, 0), "quota_exhausted", false},
		{"room-c", apperror.AgentClassCredential, apperror.NewAgentFailure(apperror.AgentClassCredential, 403, "agent_credential", "invalid api key", nil, 0), "agent_credential", false},
		{"room-p", apperror.AgentClassProcess, apperror.NewAgentFailure(apperror.AgentClassProcess, 502, "upstream_error", "the agent process exited", nil, 0), "upstream_error", false},
		{"room-u", apperror.AgentClassUnknown, apperror.NewAgentFailure(apperror.AgentClassUnknown, 502, "upstream_error", "the compiler exploded", nil, 0), "upstream_error", false},
	}
	for _, tc := range cases {
		t.Run(tc.class, func(t *testing.T) {
			rt := &honestAgent{fn: func(string, int, event.TurnEventHandler) error { return tc.err }}
			d := honestDeps(t, rt)
			err := runHonest(t, d, tc.room, nil)
			if apperror.As(err).StatusCode() != apperror.As(tc.err).StatusCode() || apperror.As(err).Code() != tc.code {
				t.Fatalf("turn = %v", err)
			}
			if rt.callCount(tc.room) != 1 {
				t.Fatalf("prompts = %d, want one", rt.callCount(tc.room))
			}
			hold, ok := CurrentAgentHold(tc.room)
			if !ok || hold.Class != tc.class || hold.Retry != "none" || (hold.RetryAt != 0) != tc.at {
				t.Fatalf("hold = %+v ok=%v", hold, ok)
			}
		})
	}
}

func TestNamedDelayInsideSixtySecondsReplacesTheBackoff(t *testing.T) {
	shortenBackoff(t, 5*time.Second, 5*time.Second)
	rt := &honestAgent{fn: func(string, int, event.TurnEventHandler) error {
		return rateLimited("rate limit exceeded", 20*time.Millisecond)
	}}
	d := honestDeps(t, rt)
	start := time.Now()
	err := runHonest(t, d, "room-delay", nil)
	if time.Since(start) > time.Second {
		t.Fatalf("the named delay was not used: took %s", time.Since(start))
	}
	if apperror.As(err).StatusCode() != 429 || rt.callCount("room-delay") != 3 {
		t.Fatalf("prompts = %d err = %v", rt.callCount("room-delay"), err)
	}
	hold, ok := CurrentAgentHold("room-delay")
	if !ok || hold.Retry != "none" || hold.RetryAt == 0 {
		t.Fatalf("hold = %+v ok=%v, want retry none and a named retryAt", hold, ok)
	}
}

func TestNamedDelayPastSixtySecondsEndsTheTurn(t *testing.T) {
	shortenBackoff(t, time.Millisecond, time.Millisecond)
	rt := &honestAgent{fn: func(string, int, event.TurnEventHandler) error {
		return rateLimited("rate limit exceeded", 61*time.Second)
	}}
	d := honestDeps(t, rt)
	start := time.Now()
	err := runHonest(t, d, "room-long", nil)
	if time.Since(start) > time.Second {
		t.Fatalf("a delay past 60s was sat out: %s", time.Since(start))
	}
	if rt.callCount("room-long") != 1 || apperror.As(err).StatusCode() != 429 {
		t.Fatalf("prompts = %d err = %v", rt.callCount("room-long"), err)
	}
	hold, ok := CurrentAgentHold("room-long")
	if !ok || hold.Class != apperror.AgentClassRateLimited || hold.Retry != "none" || hold.RetryAt == 0 {
		t.Fatalf("hold = %+v ok=%v", hold, ok)
	}
	if hold.RetryAt < time.Now().Add(60*time.Second).UnixMilli() {
		t.Fatalf("retryAt = %d, want the named time", hold.RetryAt)
	}
}

func TestAPausedAgentDoesNotFailTheNextRoom(t *testing.T) {
	shortenBackoff(t, 200*time.Millisecond, 200*time.Millisecond)
	rt := &honestAgent{fn: func(conv string, n int, _ event.TurnEventHandler) error {
		if conv == "room-b" {
			return nil
		}
		return rateLimited("rate limit exceeded", 0)
	}}
	d := honestDeps(t, rt)
	errA := make(chan error, 1)
	go func() {
		job := &Send{
			Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(),
			Message: "hello", Opts: SendOptions{Agent: "claude"},
		}
		job.Bind("room-a", nil)
		errA <- job.Execute(t.Context())
	}()
	deadline := time.Now().Add(2 * time.Second)
	for agentPauseRemaining("claude") < 80*time.Millisecond {
		if time.Now().After(deadline) {
			t.Fatal("the first room never paused the agent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	jobB := &Send{
		Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(),
		Message: "hello", Opts: SendOptions{Agent: "claude"},
	}
	jobB.Bind("room-b", nil)
	if err := jobB.Execute(t.Context()); err != nil {
		t.Fatalf("second room = %v", err)
	}
	entered := rt.firstAt("room-b")
	if entered.IsZero() || entered.Sub(start) < 50*time.Millisecond {
		t.Fatalf("second room called the agent %s after it was started, during the pause", entered.Sub(start))
	}
	if err := <-errA; apperror.As(err).StatusCode() != 429 {
		t.Fatalf("first room = %v", err)
	}
	if _, ok := CurrentAgentHold("room-b"); ok {
		t.Fatal("the room that waited out the pause kept a hold")
	}
}

// An explicit Retry is turn.resume. It sends during the pause. Another
// room on the same model does not start on its own.
func TestExplicitRetryIsNotHeldByThePause(t *testing.T) {
	shortenBackoff(t, 2*time.Second, 2*time.Second)
	rt := &honestAgent{fn: func(conv string, n int, on event.TurnEventHandler) error {
		if conv == "room-a" && n == 0 {
			if on != nil {
				on(event.NewClockEvent("working", 10, 25))
			}
			return rateLimited("rate limit exceeded", 0)
		}
		return nil
	}}
	d := honestDeps(t, rt)
	if err := runHonestModel(t, d, "room-a", "opus", nil); apperror.As(err).StatusCode() != 429 {
		t.Fatalf("turn = %v", err)
	}
	if rt.callCount("room-a") != 1 {
		t.Fatalf("prompts = %d, want one — an update already landed", rt.callCount("room-a"))
	}
	waitForPause(t, "claude")

	heldCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	heldErr := make(chan error, 1)
	go func() {
		heldErr <- runHonestModel(t, d, "room-held", "opus", heldCtx)
	}()
	time.Sleep(80 * time.Millisecond)
	if rt.callCount("room-held") != 0 {
		t.Fatal("another room on the limited model started during the pause")
	}

	start := time.Now()
	resume := &Resume{Deps: d, Ctx: t.Context()}
	resume.Bind("room-a", nil)
	if err := resume.Execute(t.Context()); err != nil {
		t.Fatalf("retry = %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("explicit retry sat out the pause: %s", time.Since(start))
	}
	if rt.callCount("room-a") != 2 {
		t.Fatalf("retry prompts = %d, want the failed call plus the retry", rt.callCount("room-a"))
	}
	if rt.callCount("room-held") != 0 {
		t.Fatal("the retry released the other room")
	}
	cancel()
	<-heldErr
}

// The pause remembers the model and upstream it was limited on. A send
// on another model, or after this room's upstream changed, is not held.
// A send of the same pair still is.
func TestChangedModelOrUpstreamIsNotHeld(t *testing.T) {
	shortenBackoff(t, 2*time.Second, 2*time.Second)
	rt := &honestAgent{fn: func(conv string, n int, on event.TurnEventHandler) error {
		if conv == "room-a" && n == 0 {
			if on != nil {
				on(event.NewClockEvent("working", 10, 25))
			}
			return rateLimited("rate limit exceeded", 0)
		}
		return nil
	}}
	d := honestDeps(t, rt)
	if err := runHonestModel(t, d, "room-a", "opus", nil); apperror.As(err).StatusCode() != 429 {
		t.Fatalf("turn = %v", err)
	}
	waitForPause(t, "claude")

	heldCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	heldErr := make(chan error, 1)
	go func() {
		heldErr <- runHonestModel(t, d, "room-held", "opus", heldCtx)
	}()
	time.Sleep(80 * time.Millisecond)
	if rt.callCount("room-held") != 0 {
		t.Fatal("the same model started during the pause")
	}

	start := time.Now()
	if err := runHonestModel(t, d, "room-other", "sonnet", nil); err != nil {
		t.Fatalf("different model = %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("a different model sat out the pause: %s", time.Since(start))
	}
	if rt.callCount("room-other") != 1 {
		t.Fatal("a different model did not start")
	}
	if rt.callCount("room-held") != 0 {
		t.Fatal("the other model released the limited one")
	}

	conv, err := d.Convs.Load(t.Context(), "room-a")
	if err != nil {
		t.Fatal(err)
	}
	conv.SetUpstream("beta")
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	if err := runHonestModel(t, d, "room-a", "", nil); err != nil {
		t.Fatalf("changed upstream = %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("a changed upstream sat out the pause: %s", time.Since(start))
	}
	if rt.callCount("room-a") != 2 {
		t.Fatalf("prompts = %d, want the limited call plus the new upstream", rt.callCount("room-a"))
	}
	if rt.callCount("room-held") != 0 {
		t.Fatal("the upstream change released the limited model")
	}
	cancel()
	<-heldErr
}

func runHonestModel(t *testing.T, d Deps, room, model string, ctx context.Context) error {
	t.Helper()
	if ctx == nil {
		ctx = t.Context()
	}
	job := &Send{
		Deps: d, Ctx: ctx, Receipt: NewPersistenceReceipt(),
		Message: "hello", ModelID: model, Opts: SendOptions{Agent: "claude"},
	}
	job.Bind(room, nil)
	return job.Execute(ctx)
}

func waitForPause(t *testing.T, agentID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for agentPauseRemaining(agentID) < 500*time.Millisecond {
		if time.Now().After(deadline) {
			t.Fatal("the rate limit never paused the agent")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
