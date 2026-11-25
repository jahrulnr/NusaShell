package appconversation

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// probeTurn is a provider turn that keeps running long enough to steer it: the
// call the test names blocks until released, then the turn asks for a tool so
// the loop runs another provider call. Every request body is kept, so a test
// can read what the model actually saw.
type probeTurn struct {
	mu      sync.Mutex
	blockOn int
	entered chan struct{}
	release chan struct{}
	calls   int
	bodies  []string
	// releaseOnce lets the test and its cleanup release the turn exactly once.
	releaseOnce sync.Once
}

func newProbeTurn(t *testing.T, blockOn int) *probeTurn {
	t.Helper()
	p := &probeTurn{blockOn: blockOn, entered: make(chan struct{}), release: make(chan struct{})}
	// A test that fails while the turn is still blocked must not leave it
	// blocked: the turn's cleanup would wait for it and the failure would read
	// as a timeout instead of an assertion.
	t.Cleanup(func() {
		p.releaseOnce.Do(func() { close(p.release) })
	})
	return p
}

func (p *probeTurn) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.bodies = append(p.bodies, string(body))
	p.mu.Unlock()
	if n == p.blockOn {
		close(p.entered)
		<-p.release
	}
	if n <= p.blockOn {
		// Ask for a tool the room does not have: the loop records the failed
		// call and runs another step, which is where a steered prompt folds.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
					map[string]any{"id": "call_probe", "type": "function", "function": map[string]any{"name": "probe", "arguments": "{}"}},
				}},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
		})
		return
	}
	if err := json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "pong"},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	}); err != nil {
		http.Error(w, "fixture", http.StatusInternalServerError)
	}
}

// lastBody answers the newest request body the model was given.
func (p *probeTurn) lastBody() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bodies) == 0 {
		return ""
	}
	return p.bodies[len(p.bodies)-1]
}

func (p *probeTurn) bodyAfter(n int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bodies) <= n {
		return ""
	}
	return p.bodies[n]
}

// startTurn runs one send in the background and answers it when the probe turn
// has blocked, so the test steers while the turn really runs.
func startTurn(t *testing.T, app *ConversationApp, p *probeTurn, convID, text string) chan error {
	t.Helper()
	// Registered after the fixture, so it runs BEFORE the app's shutdown: a
	// failure must free the blocked provider call instead of making shutdown
	// wait on it and reporting a timeout in place of the assertion.
	t.Cleanup(func() { p.releaseOnce.Do(func() { close(p.release) }) })
	done := make(chan error, 1)
	go func() {
		_, err := app.Send(t.Context(), convID, "gw1", "m1", text, nil, appjob.SendOptions{}, nil)
		done <- err
	}()
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached the provider")
	}
	return done
}

func release(t *testing.T, app *ConversationApp, p *probeTurn, done chan error, convID string) {
	t.Helper()
	p.releaseOnce.Do(func() { close(p.release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the turn never came back after it was released")
	}
	waitIdle(t, app, convID)
}

// relayedUserRows answers the user messages that carry a relay record, in order.
func relayedUserRows(t *testing.T, app *ConversationApp, convID string) []entity.Message {
	t.Helper()
	conv, err := app.deps.Convs.Load(t.Context(), convID)
	if err != nil {
		t.Fatal(err)
	}
	var out []entity.Message
	for _, m := range conv.Messages() {
		if m.Role() == enum.RoleUser && m.UserType() == enum.LLMUserTypeRelay {
			out = append(out, m)
		}
	}
	return out
}

// A steer into a busy provider room is one user line carrying the sender's
// record, and the model reads it on its next call.
func TestRelaySteerFoldsOneRecordTheModelReads(t *testing.T) {
	p := newProbeTurn(t, 1)
	app, _ := fixture(t, p.handler)
	done := startTurn(t, app, p, "c_relay", "owner question")
	taken, err := app.SteerInput(t.Context(), "c_relay", valueobject.UserInput{
		Text:  "status please",
		Relay: &valueobject.Relay{ID: "c_pa", Label: "coyote", Title: "PA"},
	})
	if err != nil || !taken {
		t.Fatalf("steer into the running turn = %v, %v", taken, err)
	}
	release(t, app, p, done, "c_relay")
	rows := relayedUserRows(t, app, "c_relay")
	if len(rows) != 1 {
		t.Fatalf("steer wrote %d relay lines, want one: %+v", len(rows), rows)
	}
	if rows[0].Content() != "status please" || rows[0].Relay().ID != "c_pa" {
		t.Fatalf("the line lost its words or its record: %q %+v", rows[0].Content(), rows[0].Relay())
	}
	// The first call went out before the fold; the second one is what the model
	// read after it.
	body := p.bodyAfter(1)
	if !strings.Contains(body, "status please") {
		t.Fatalf("the model never read the steered text: %s", body)
	}
	if !strings.Contains(body, "[From thread @coyote PA (c_pa)]") {
		t.Fatalf("the model read the text without the relay head: %s", body)
	}
	if n := strings.Count(body, "status please"); n != 1 {
		t.Fatalf("the text reached the model %d times in one call: %s", n, body)
	}
}

// Three prompts while the turn runs are three lines, in arrival order.
func TestRelaySteersKeepArrivalOrder(t *testing.T) {
	p := newProbeTurn(t, 1)
	app, _ := fixture(t, p.handler)
	done := startTurn(t, app, p, "c_three", "owner question")
	for _, text := range []string{"first", "second", "third"} {
		taken, err := app.SteerInput(t.Context(), "c_three", valueobject.UserInput{
			Text:  text,
			Relay: &valueobject.Relay{ID: "c_pa", Label: "coyote"},
		})
		if err != nil || !taken {
			t.Fatalf("steer %q = %v, %v", text, taken, err)
		}
	}
	release(t, app, p, done, "c_three")
	rows := relayedUserRows(t, app, "c_three")
	if len(rows) != 3 {
		t.Fatalf("three prompts made %d lines, want three", len(rows))
	}
	for i, want := range []string{"first", "second", "third"} {
		if rows[i].Content() != want {
			t.Fatalf("line %d = %q, want %q", i, rows[i].Content(), want)
		}
	}
}

// A turn that ends without folding the prompt still keeps the record: history is
// the persistence, and the next turn reads the text exactly once.
func TestRelaySteerSurvivesATurnThatNeverFoldsIt(t *testing.T) {
	p := newProbeTurn(t, 2)
	app, _ := fixture(t, p.handler)
	done := startTurn(t, app, p, "c_late", "owner question")
	// The second call is already blocked: the prompt arrives after the turn's
	// last fold point, so this turn can never put it in history itself.
	taken, err := app.SteerInput(t.Context(), "c_late", valueobject.UserInput{
		Text:  "arrived too late",
		Relay: &valueobject.Relay{ID: "c_pa", Label: "coyote"},
	})
	if err != nil || !taken {
		t.Fatalf("steer = %v, %v", taken, err)
	}
	// Accepted is recorded: the line exists while the turn still runs, before
	// any fold could write — a restart here would postpone delivery, not lose it.
	if rows := userRowsWithText(t, app, "c_late", "arrived too late"); len(rows) != 1 {
		t.Fatalf("no line for an accepted relay: %+v", rows)
	}
	release(t, app, p, done, "c_late")
	rows := relayedUserRows(t, app, "c_late")
	if len(rows) != 1 {
		t.Fatalf("the unfired prompt left %d records, want one", len(rows))
	}
	if rows[0].Content() != "arrived too late" {
		t.Fatalf("the record lost the text: %q", rows[0].Content())
	}
	// The next turn reads it — once.
	if _, err := app.Send(t.Context(), "c_late", "", "", "carry on", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	body := p.lastBody()
	if n := strings.Count(body, "arrived too late"); n != 1 {
		t.Fatalf("the next turn read the late prompt %d times: %s", n, body)
	}
	if !strings.Contains(body, "[From thread") {
		t.Fatalf("the late prompt reached the model without its record: %s", body)
	}
}

// A relayed prompt an agent room cannot take mid-turn keeps its accept line and
// rides it into the follow-up: one line, text read once, no join row.
func TestRelaySteerInAnAgentRoomStaysOneLine(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	if _, err := app.Send(t.Context(), "c_agent", "", "", "start", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_agent")
	taken, err := app.SteerInput(t.Context(), "c_agent", valueobject.UserInput{
		Text:  "status from PA",
		Relay: &valueobject.Relay{ID: "c_pa", Label: "coyote"},
	})
	if err != nil || !taken {
		t.Fatalf("steer into a busy agent room = %v, %v", taken, err)
	}
	release()
	waitIdle(t, app, "c_agent")
	// The agent gets the text on its next prompt — the accept line is the
	// message, and nothing wrote a second one.
	waitFor(t, "the follow-up reaching the agent", func() bool {
		return agentSawOnce(rt, "status from PA")
	})
	// Every user row carrying the words, whatever its type: a second line for
	// the same prompt is the bug, not a differently typed one.
	rows := userRowsWithText(t, app, "c_agent", "status from PA")
	if len(rows) != 1 {
		t.Fatalf("one relayed prompt made %d lines, want one: %+v", len(rows), rows)
	}
	if rows[0].UserType() != enum.LLMUserTypeRelay {
		t.Fatalf("the line lost its record: %s", rows[0].UserType())
	}
	if n := countPromptText(rt, "status from PA"); n != 1 {
		t.Fatalf("the agent read the text %d times, want once", n)
	}
}

// agentSawOnce reports whether the agent has read the text at all.
func agentSawOnce(rt *scriptedAgent, text string) bool {
	return countPromptText(rt, text) > 0
}

// countPromptText counts the prompts whose content carries the text.
func countPromptText(rt *scriptedAgent, text string) int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	n := 0
	for _, p := range rt.prompts {
		if strings.Contains(p.Input.Content(), text) {
			n++
		}
	}
	return n
}

// userRowsWithText answers the user messages whose body carries the text.
func userRowsWithText(t *testing.T, app *ConversationApp, convID, text string) []entity.Message {
	t.Helper()
	conv, err := app.deps.Convs.Load(t.Context(), convID)
	if err != nil {
		t.Fatal(err)
	}
	var out []entity.Message
	for _, m := range conv.Messages() {
		if m.Role() == enum.RoleUser && strings.Contains(m.Content(), text) {
			out = append(out, m)
		}
	}
	return out
}

// A relayed prompt an agent takes mid-turn is the case that writes nothing at
// all: the accept line is the only row, and the text reaches the agent through
// the steer.
func TestRelaySteerTakenByTheAgentWritesOneLine(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	rt.steerable = true
	rt.steered = make(chan string, 1)
	// The prompt holds until the steer arrives, so the send runs in the
	// background: this test drives both sides.
	done := make(chan error, 1)
	go func() {
		_, err := app.Send(t.Context(), "c_taken", "", "", "wait for steer", nil, appjob.SendOptions{Agent: "fake"}, nil)
		done <- err
	}()
	waitFor(t, "the agent prompt starting", func() bool {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return len(rt.prompts) == 1
	})
	taken, err := app.SteerInput(t.Context(), "c_taken", valueobject.UserInput{
		Text:  "status from PA",
		Relay: &valueobject.Relay{ID: "c_pa", Label: "coyote"},
	})
	if err != nil || !taken {
		t.Fatalf("steer into the running agent = %v, %v", taken, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the turn never came back after the steer")
	}
	waitFor(t, "the agent taking the steer", func() bool {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return len(rt.steers) == 1
	})
	// What the runtime received, not what a surface shows.
	rt.mu.Lock()
	got := append([]string(nil), rt.steers...)
	prompts := len(rt.prompts)
	rt.mu.Unlock()
	if len(got) != 1 || got[0] != "status from PA" {
		t.Fatalf("the agent took %v, want the text once", got)
	}
	if prompts != 1 {
		t.Fatalf("the accepted steer started %d prompts, want the one it joined", prompts)
	}
	rows := userRowsWithText(t, app, "c_taken", "status from PA")
	if len(rows) != 1 {
		t.Fatalf("one accepted steer made %d lines, want one: %+v", len(rows), rows)
	}
	if rows[0].UserType() != enum.LLMUserTypeRelay || rows[0].Relay().ID != "c_pa" {
		t.Fatalf("the line lost its record: %s %+v", rows[0].UserType(), rows[0].Relay())
	}
}
