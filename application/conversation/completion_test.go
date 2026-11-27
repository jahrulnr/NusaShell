package appconversation

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// recordsInHistory counts the messages a wake turn wrote.
func recordsInHistory(t *testing.T, app *ConversationApp, convID string) int {
	t.Helper()
	conv, err := app.deps.Convs.Load(t.Context(), convID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range conv.Messages() {
		if m.Role() == enum.RoleUser && m.UserType() == enum.LLMUserTypeSystem &&
			strings.Contains(m.Content(), "Background completions") {
			n++
		}
	}
	return n
}

// An exit reported while the room is idle becomes a turn that reads it: the record
// lands in history as a system-typed message, and the pending list empties.
func TestCompletionWakesAnIdleRoom(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_wake", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	app.OnCompletion("c_wake", dservice.Completion{Source: "session", ID: "4242", Status: "exited", Detail: "exit 0\nall done"})
	waitFor(t, "the wake turn landing the record", func() bool {
		return recordsInHistory(t, app, "c_wake") == 1
	})
	if got := app.PendingCompletions("c_wake"); len(got) != 0 {
		t.Fatalf("the record stayed pending after it was read: %+v", got)
	}
	// The data the model read is a record, not the message: the wake turn's step
	// carries it as a TooltypeInjection, the same shape project_context and memory
	// take, with the detail the line in history leaves out.
	conv, err := app.deps.Convs.Load(ctx, "c_wake")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, msg := range conv.Messages() {
		for _, call := range msg.ToolCalls() {
			if call.Type() == enum.TooltypeInjection && call.Name() == appjob.CompletionRecord {
				found = true
				if !strings.Contains(call.Output(), "exit 0") {
					t.Fatalf("the record lost its detail: %s", call.Output())
				}
			}
		}
	}
	if !found {
		t.Fatal("the wake turn carried no completion record")
	}
	// One exit, one record, ever: the same completion reported again is a no-op.
	app.OnCompletion("c_wake", dservice.Completion{Source: "session", ID: "4242", Status: "exited", Detail: "exit 0\nall done"})
	time.Sleep(200 * time.Millisecond)
	if n := recordsInHistory(t, app, "c_wake"); n != 1 {
		t.Fatalf("a repeated report rendered %d records", n)
	}
}

// A busy room keeps the record and spends no turn: the running turn reads it on
// its next hydration step, and nothing is queued behind it.
func TestCompletionDoesNotWakeABusyRoom(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_busy", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	hold := newWedgeJob()
	app.post("c_busy", hold)
	select {
	case <-hold.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the holding job never started")
	}
	app.OnCompletion("c_busy", dservice.Completion{Source: "session", ID: "77", Status: "exited", Detail: "exit 0"})
	if got := app.PendingCompletions("c_busy"); len(got) != 1 {
		t.Fatalf("a busy room lost the record: %+v", got)
	}
	if depth := app.QueueDepth("c_busy"); depth != 0 {
		t.Fatalf("a busy room queued %d wake turns, want none", depth)
	}
	close(hold.release)
	waitIdle(t, app, "c_busy")
}

// One record is claimed by one wake: callers that race for it — a completion
// arriving from another goroutine and the room going idle — spend a single turn,
// not one each. A second claim used to charge the budget for a wake that read
// nothing, and the record behind it waited for a turn that never came.
func TestOneRecordIsClaimedByOneWake(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_claim", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, app, "c_claim")
	// The record is parked in the store, not reported through OnCompletion: the
	// test wants several callers racing for a record that is already waiting.
	recorded, err := app.depsSnapshot().Convs.SaveCompletions(ctx, "c_claim",
		[]dservice.Completion{{Source: "session", ID: "1", Status: "exited", Detail: "exit 0"}})
	if err != nil || !recorded {
		t.Fatalf("parking the record = %v, %v", recorded, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			app.wake("c_claim", true)
		}()
	}
	wg.Wait()
	app.mu.Lock()
	claimed := app.machineTurns["c_claim"]
	app.mu.Unlock()
	if claimed != 1 {
		t.Fatalf("one record claimed %d wake turns, want 1", claimed)
	}
	waitFor(t, "the claimed wake reading its record", func() bool {
		return len(app.PendingCompletions("c_claim")) == 0
	})
	if n := recordsInHistory(t, app, "c_claim"); n != 1 {
		t.Fatalf("the record was read in %d wake messages, want 1", n)
	}
}

// The wake budget is three consecutive machine turns: the fourth completion keeps
// its record and spends no turn.
func TestCompletionStopsWakingAtTheCap(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_cap", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	// One record at a time, each read before the next arrives: a wake reads every
	// record pending at that moment, so batching them would land one message for
	// several records and say nothing about the cap.
	for i, id := range []string{"1", "2", "3"} {
		app.OnCompletion("c_cap", dservice.Completion{Source: "session", ID: id, Status: "exited", Detail: "exit 0"})
		waitFor(t, "the wake turn reading "+id, func() bool {
			return len(app.PendingCompletions("c_cap")) == 0
		})
		if n := recordsInHistory(t, app, "c_cap"); n != i+1 {
			t.Fatalf("after %d records the room read %d wake messages, want %d", i+1, n, i+1)
		}
	}
	// The cap is reached: the next record waits, and no turn is spent on it.
	app.OnCompletion("c_cap", dservice.Completion{Source: "session", ID: "4", Status: "exited", Detail: "exit 0"})
	time.Sleep(300 * time.Millisecond)
	if n := recordsInHistory(t, app, "c_cap"); n != maxMachineTurns {
		t.Fatalf("a capped room ran %d wake turns, want %d", n, maxMachineTurns)
	}
	if got := app.PendingCompletions("c_cap"); len(got) != 1 || got[0].ID != "4" {
		t.Fatalf("the capped record was lost: %+v", got)
	}
	// An owner turn intervenes: it reads the waiting record as its own hydration —
	// no wake is spent on it — and resets the budget, so a later finish may wake.
	if _, err := app.Send(ctx, "c_cap", "gw1", "m1", "carry on", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if pending := app.PendingCompletions("c_cap"); len(pending) != 0 {
		t.Fatalf("the owner turn did not read the capped record: %+v", pending)
	}
	app.OnCompletion("c_cap", dservice.Completion{Source: "session", ID: "5", Status: "exited", Detail: "exit 0"})
	// The line reaches history first: the wake clears the record it rendered only
	// after that turn saved, so a failed turn can retry it. Waiting on the message
	// alone would read the book mid-turn and call a good wake unread.
	waitFor(t, "the wake after the owner turn reading its record", func() bool {
		return recordsInHistory(t, app, "c_cap") == maxMachineTurns+1 &&
			len(app.PendingCompletions("c_cap")) == 0
	})
}

// A deleted room takes its records and dedup keys with it.
func TestDeletedRoomDropsItsCompletions(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_del", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	app.OnCompletion("c_del", dservice.Completion{Source: "session", ID: "9", Status: "exited", Detail: "exit 0"})
	if got := app.PendingCompletions("c_del"); len(got) != 1 {
		t.Fatalf("the room never parked the record: %+v", got)
	}
	if err := app.Delete(ctx, "c_del"); err != nil {
		t.Fatal(err)
	}
	// The book lives inside the room's directory: deleting the room takes it, and
	// the store answers "no such room" rather than "nothing pending" — a deleted
	// room must never read as a room at peace.
	if _, err := app.deps.Convs.PendingCompletions(ctx, "c_del"); apperror.As(err).StatusCode() != apperror.StatusNotFound {
		t.Fatalf("a deleted room answered %v, want not found", err)
	}
	// And a finish reported after the delete has nowhere to land: it is refused,
	// not written into a directory nobody owns.
	app.OnCompletion("c_del", dservice.Completion{Source: "session", ID: "10", Status: "exited"})
	if _, err := app.deps.Convs.PendingCompletions(ctx, "c_del"); apperror.As(err).StatusCode() != apperror.StatusNotFound {
		t.Fatalf("a report resurrected the room's book: %v", err)
	}
}

// A record reported while the room works is read as soon as that turn ends: the
// record waited, and the room going idle is what retries the wake.
func TestCompletionReportedWhileBusyIsReadWhenIdle(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_later", "", "", "hi", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_later")
	app.OnCompletion("c_later", dservice.Completion{Source: "session", ID: "5", Status: "exited", Detail: "exit 0"})
	if got := app.PendingCompletions("c_later"); len(got) != 1 {
		t.Fatalf("the record was lost while the turn ran: %+v", got)
	}
	release()
	waitIdle(t, app, "c_later")
	waitFor(t, "the record landing once the turn ended", func() bool {
		return recordsInHistory(t, app, "c_later") == 1
	})
}

// A finish reported while a turn runs rides that turn's next hydration step: no
// job is queued behind the running turn, and the record the model reads is the
// same shape every other system record takes.
func TestCompletionWhileATurnRunsRidesItsHydration(t *testing.T) {
	var app *ConversationApp
	var step atomic.Int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		if step.Add(1) == 1 {
			// The exit is reported in the middle of the turn: after the first
			// hydration step, before the second.
			app.OnCompletion("c_ride", dservice.Completion{Source: "session", ID: "77", Status: "exited", Detail: "exit 0"})
			if err := json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
						map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "probe", "arguments": "{}"}},
					}},
					"finish_reason": "tool_calls",
				}},
				"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
			}); err != nil {
				http.Error(w, "fixture", http.StatusInternalServerError)
			}
			return
		}
		okHandler(w, r)
	}
	app, _ = fixture(t, handler)
	if _, err := app.Send(t.Context(), "c_ride", "gw1", "m1", "go", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if depth := app.QueueDepth("c_ride"); depth != 0 {
		t.Fatalf("a busy room queued %d jobs behind its turn, want none", depth)
	}
	if n := recordsInHistory(t, app, "c_ride"); n != 0 {
		t.Fatalf("the record spent %d wake turns, want none", n)
	}
	if pending := app.PendingCompletions("c_ride"); len(pending) != 0 {
		t.Fatalf("the running turn did not read the record: %+v", pending)
	}
	conv, err := app.deps.Convs.Load(t.Context(), "c_ride")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, msg := range conv.Messages() {
		for _, call := range msg.ToolCalls() {
			if call.Type() == enum.TooltypeInjection && call.Name() == appjob.CompletionRecord {
				found = true
				if !strings.Contains(call.Output(), "exit 0") || !strings.Contains(call.Input(), `"77"`) {
					t.Fatalf("record shape: input=%s output=%s", call.Input(), call.Output())
				}
			}
		}
	}
	if !found {
		t.Fatal("the running turn's hydration carried no completion record")
	}
}
