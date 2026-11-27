package wsconversation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// A queued turn opens exactly one journal, and a watcher that never issued the
// command sees it live: opened, the streamed delta, then the terminal — the same
// frame contract an admitted turn has. The notice names the room's turn with an
// empty requestId (no tab may claim it) and the job's origin.
func TestWorkerTurnIsWatchableLive(t *testing.T) {
	release := make(chan struct{})
	h := newHub(t, stubSender{
		fn: func(ctx context.Context, id, _, _, _ string, onEvent event.TurnEventHandler) (appconversation.SendResult, error) {
			onEvent(event.NewDeltaEvent("machine work"))
			select {
			case <-release:
			case <-ctx.Done():
				return appconversation.SendResult{ConversationID: id}, ctx.Err()
			}
			return appconversation.SendResult{ConversationID: id, UserMessagePersisted: true}, nil
		},
	})
	// A tab is already connected when the machine turn starts; the activity notice
	// is what tells it the turn exists.
	ws := dial(t, h)

	// The turn the job runs: it reaches execution with no handler — the shape a
	// queued relay message has.
	handler, seal := h.Open("c_machine", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil || seal == nil {
		t.Fatal("the surface refused a turn it had room for")
	}
	go func() {
		handler(event.NewDeltaEvent("machine work"))
		time.Sleep(20 * time.Millisecond)
		seal(true, nil)
	}()

	// The opened edge reaches every socket, empty requestId, origin conversation.
	f := readAnyFrame(t, ws)
	for f.Type != "conversation.activity" {
		f = readAnyFrame(t, ws)
	}
	var p activityPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.State != "opened" || p.Kind != "send" || p.RequestID != "" || p.Origin != "conversation" {
		t.Fatalf("opened notice = %+v, want kind send, empty requestId, origin conversation", p)
	}
	// The tab attaches to the turn it did not issue, and replays it.
	sendCommand(t, ws, `{"type":"turn.watch","requestId":"w1","conversationId":"c_machine","turnId":"`+f.TurnID+`","afterSeq":-1}`)
	sawDelta, sawTerminal := false, false
	for !sawTerminal {
		fr := readFrame(t, ws)
		switch fr.Type {
		case "turn.delta":
			sawDelta = strings.Contains(string(fr.Payload), "machine work") || sawDelta
		case "turn.completed":
			sawTerminal = true
		case "turn.failed":
			t.Fatalf("the machine turn failed: %+v", fr)
		}
	}
	if !sawDelta {
		t.Fatal("the watcher never replayed the machine turn's stream")
	}
}

// The pool is observability, not a gate: a full pool refuses the journal, the turn
// still runs, and the slot comes back once a turn finishes.
func TestWorkerTurnRunsBlindWhenThePoolIsFull(t *testing.T) {
	h := newHub(t, stubSender{})
	// Fill every slot with journals the surface opened.
	seals := make([]func(bool, error), 0, maxActiveTurns)
	for i := range maxActiveTurns {
		_, seal := h.Open("c_full", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
		if seal == nil {
			t.Fatalf("slot %d was refused before the pool was full", i)
		}
		seals = append(seals, seal)
	}
	handler, seal := h.Open("c_full", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler != nil || seal != nil {
		t.Fatal("a full pool opened another journal")
	}
	// A slot returns when one of them finishes.
	seals[0](true, nil)
	handler, seal = h.Open("c_full", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil || seal == nil {
		t.Fatal("the slot did not come back after a turn finished")
	}
	seal(true, nil)
	for _, s := range seals[1:] {
		s(true, nil)
	}
}

// A kind the hub does not know is refused: a journal must not exist under a name
// no contract defines.
func TestWorkerTurnRefusesAnUnknownKind(t *testing.T) {
	h := newHub(t, stubSender{})
	if handler, seal := h.Open("c_kind", "teleport", valueobject.TurnOrigin{}); handler != nil || seal != nil {
		t.Fatal("an unknown turn kind opened a journal")
	}
}

// A journal-only turn's cancel must reach the worker that owns the job,
// never the bookkeeping ctx: the hub forwards through jobControl, and the
// ack is honest — a forward that lands nowhere is a not_found, not an ack.
func TestCancelJournalOnlyTurnForwardsToTheWorker(t *testing.T) {
	t.Skip("ack is UI plumbing; what must be proven first is that the job ends — see the chain test")
	h := newHub(t, stubSender{})
	cancelled := make(chan string, 1)
	h.UseJobControl(func(convID string) bool { cancelled <- convID; return true }, nil, nil)
	handler, seal := h.Open("c_job", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	defer seal(true, nil)
	ws := dial(t, h)
	// learn the turn id from the activity notice
	tid := ""
	for tid == "" {
		f := readAnyFrame(t, ws)
		if f.Type == "conversation.activity" {
			tid = f.TurnID
		}
	}
	sendCommand(t, ws, `{"type":"turn.cancel","requestId":"c1","conversationId":"c_job","turnId":"`+tid+`"}`)
	select {
	case got := <-cancelled:
		if got != "c_job" {
			t.Fatalf("forwarded cancel convID = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel never reached the worker callback")
	}
	f := readAnyFrame(t, ws)
	for f.Type != "turn.cancel_ack" {
		f = readAnyFrame(t, ws)
	}
}

// Honesty: when the worker is gone, the forward reports false and the
// socket hears not_found — never an ack for a cancellation that reached
// nothing.
func TestCancelJournalOnlyTurnWithoutAWorkerIsNotAcked(t *testing.T) {
	t.Skip("ack is UI plumbing; what must be proven first is that the job ends — see the chain test")
	h := newHub(t, stubSender{})
	h.UseJobControl(func(convID string) bool { return false }, nil, nil)
	handler, seal := h.Open("c_job", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	defer seal(true, nil)
	ws := dial(t, h)
	tid := ""
	for tid == "" {
		f := readAnyFrame(t, ws)
		if f.Type == "conversation.activity" {
			tid = f.TurnID
		}
	}
	sendCommand(t, ws, `{"type":"turn.cancel","requestId":"c1","conversationId":"c_job","turnId":"`+tid+`"}`)
	f := readAnyFrame(t, ws)
	for f.Type != "error" && f.Type != "turn.cancel_ack" {
		f = readAnyFrame(t, ws)
	}
	if f.Type == "turn.cancel_ack" {
		t.Fatal("an ack was pushed for a cancellation that reached nothing")
	}
}

// Hub.Cancel is honest: a journal-only turn counts only when the worker
// took the interruption. Before the forwarding existed it answered true
// for a cancel that reached nothing — the positive lie of B-068.
func TestHubCancelAnswersTrueOnlyWhenTheWorkerTakesIt(t *testing.T) {
	h := newHub(t, stubSender{})
	workerAlive := false
	h.UseJobControl(func(convID string) bool { return workerAlive }, nil, nil)
	handler, seal := h.Open("c_job", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	defer seal(true, nil)
	if h.Cancel("c_job") {
		t.Fatal("stopped=true while no worker took the interruption")
	}
	workerAlive = true
	if !h.Cancel("c_job") {
		t.Fatal("stopped=false while the worker took the interruption")
	}
}

// A journal-only send turn is steered through the worker: the admitted
// path's mailbox was never bound for it, and before the forwarding the
// channel fell back to a second turn.
func TestSteerJournalOnlyTurnForwardsToTheWorker(t *testing.T) {
	h := newHub(t, stubSender{})
	steered := make(chan string, 1)
	h.UseJobControl(nil, func(convID string, in valueobject.UserInput) (*appjob.SteeringReceipt, bool) {
		steered <- in.Text
		return &appjob.SteeringReceipt{}, true
	}, nil)
	handler, seal := h.Open("c_job", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	defer seal(true, nil)
	if !h.Steer("c_job", "second message") {
		t.Fatal("steer reported not taken while the worker accepted it")
	}
	select {
	case got := <-steered:
		if got != "second message" {
			t.Fatalf("forwarded steer text = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("steer never reached the worker callback")
	}
}

func activityTurn(t *testing.T, ws *websocket.Conn, convID string) string {
	t.Helper()
	for {
		f := readAnyFrame(t, ws)
		if f.Type == "conversation.activity" && f.ConversationID == convID && f.TurnID != "" {
			return f.TurnID
		}
	}
}

func inputIDOf(t *testing.T, f frame) string {
	t.Helper()
	var p steerInputPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.InputID
}

// A worker-owned send is steered through the websocket command, not only
// through Hub.Steer. The forward carries the command's request id, and
// recall withdraws that id rather than whichever input arrived last.
func TestJobOwnedSteerCommandReachesTheWorkerAndRecallNamesThatInput(t *testing.T) {
	box := appjob.NewSteeringInput()
	var got []valueobject.UserInput
	h := newHub(t, stubSender{})
	h.UseJobControl(nil,
		func(convID string, in valueobject.UserInput) (*appjob.SteeringReceipt, bool) {
			if convID != "c_job" {
				t.Errorf("steer conv = %q", convID)
				return nil, false
			}
			got = append(got, in)
			receipt, err := box.SubmitBefore(in, 0, nil)
			if err != nil {
				t.Errorf("mailbox: %v", err)
				return nil, false
			}
			return receipt, true
		},
		func(convID, inputID string) bool {
			if convID != "c_job" {
				return false
			}
			return box.RecallID(inputID)
		},
	)
	ws := dial(t, h)
	handler, seal := h.Open("c_job", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	defer seal(true, nil)
	turnID := activityTurn(t, ws, "c_job")

	sendCommand(t, ws, `{"type":"turn.steer","requestId":"older","conversationId":"c_job","turnId":"`+turnID+`","payload":{"content":"older text"}}`)
	ack := readUntil(t, ws, "turn.steer_ack")
	if inputIDOf(t, ack) != "older" {
		t.Fatalf("ack input = %q, want the command requestId", inputIDOf(t, ack))
	}
	sendCommand(t, ws, `{"type":"turn.steer","requestId":"newer","conversationId":"c_job","turnId":"`+turnID+`","payload":{"content":"newer text"}}`)
	if id := inputIDOf(t, readUntil(t, ws, "turn.steer_ack")); id != "newer" {
		t.Fatalf("second ack input = %q", id)
	}
	if len(got) != 2 || got[0].Text != "older text" || got[0].InputID != "older" || got[1].Text != "newer text" || got[1].InputID != "newer" {
		t.Fatalf("forwarded inputs = %+v, want both texts with their request ids", got)
	}

	sendCommand(t, ws, `{"type":"turn.steer_recall","requestId":"rc","conversationId":"c_job","turnId":"`+turnID+`","payload":{"inputId":"older"}}`)
	recalled := readUntil(t, ws, "turn.steer_recalled")
	if inputIDOf(t, recalled) != "older" {
		t.Fatalf("recalled input = %q, want older", inputIDOf(t, recalled))
	}
	if !box.RecallID("newer") {
		t.Fatal("recall withdrew the newest input instead of the named one")
	}
	if box.RecallID("older") {
		t.Fatal("the named input was still pending after recall")
	}
}

// A worker that refuses the forward stores nothing and does not ack.
func TestRefusedJobSteerStoresNothing(t *testing.T) {
	calls := 0
	h := newHub(t, stubSender{})
	h.UseJobControl(nil, func(string, valueobject.UserInput) (*appjob.SteeringReceipt, bool) {
		calls++
		return nil, false
	}, func(string, string) bool {
		t.Fatal("recall ran for a steer the worker refused")
		return false
	})
	ws := dial(t, h)
	handler, seal := h.Open("c_job", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	defer seal(true, nil)
	turnID := activityTurn(t, ws, "c_job")
	sendCommand(t, ws, `{"type":"turn.steer","requestId":"s1","conversationId":"c_job","turnId":"`+turnID+`","payload":{"content":"no"}}`)
	f := readFrame(t, ws)
	if f.Type != "error" || f.Code != "invalid_argument" || !strings.Contains(f.Message, "does not take steering") {
		t.Fatalf("refusal = %+v, want invalid_argument that the turn does not take steering", f)
	}
	if calls != 1 {
		t.Fatalf("forward calls = %d, want 1 — dropping the call stores nothing only if it was attempted and refused", calls)
	}
	sendCommand(t, ws, `{"type":"turn.steer_recall","requestId":"rc","conversationId":"c_job","turnId":"`+turnID+`","payload":{"inputId":"s1"}}`)
	f = readFrame(t, ws)
	if f.Type != "error" || f.Code != "not_found" {
		t.Fatalf("recall after refusal = %+v, want not_found — the steer was stored", f)
	}
}

// steerable is written on turn.opened and on the active resync snapshot,
// including when the answer is no. A missing field would look like an
// older server, which the client treats as unknown.
func TestLiveTurnWritesSteerableEvenWhenFalse(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := newHub(t, stubSender{fn: func(ctx context.Context, id, _, _, _ string, _ event.TurnEventHandler) (appconversation.SendResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return appconversation.SendResult{ConversationID: id, UserMessagePersisted: true}, nil
	}})
	h.UseJobControl(nil, func(string, valueobject.UserInput) (*appjob.SteeringReceipt, bool) {
		return &appjob.SteeringReceipt{}, true
	}, nil)
	ws := dial(t, h)
	sendCommand(t, ws, `{"type":"turn.send","requestId":"req-1","payload":{"providerId":"gw1","modelId":"m1","content":"hi"}}`)
	opened := readUntil(t, ws, "turn.opened")
	if !strings.Contains(string(opened.Payload), `"steerable":true`) {
		t.Fatalf("admitted send opened %s, want steerable true", opened.Payload)
	}
	sendCommand(t, ws, `{"type":"turn.watch","requestId":"w-send","conversationId":"`+opened.ConversationID+`","turnId":"`+opened.TurnID+`","afterSeq":1000}`)
	resync := readUntil(t, ws, "turn.resync_required")
	if !strings.Contains(string(resync.Payload), `"steerable":true`) {
		t.Fatalf("active send resync %s, want steerable true", resync.Payload)
	}

	handler, seal := h.Open("c_edit", "edit", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the edit")
	}
	defer seal(true, nil)
	editID := activityTurn(t, ws, "c_edit")
	sendCommand(t, ws, `{"type":"turn.watch","requestId":"w-edit","conversationId":"c_edit","turnId":"`+editID+`","afterSeq":-1}`)
	editOpened := readUntilTurn(t, ws, "turn.opened", editID)
	if !strings.Contains(string(editOpened.Payload), `"steerable":false`) {
		t.Fatalf("edit opened %s, want steerable false written", editOpened.Payload)
	}
	sendCommand(t, ws, `{"type":"turn.watch","requestId":"w-edit2","conversationId":"c_edit","turnId":"`+editID+`","afterSeq":1000}`)
	editResync := readUntilTurn(t, ws, "turn.resync_required", editID)
	if !strings.Contains(string(editResync.Payload), `"steerable":false`) {
		t.Fatalf("active edit resync %s, want steerable false written", editResync.Payload)
	}

	bare := newHub(t, stubSender{})
	ws2 := dial(t, bare)
	handler, seal2 := bare.Open("c_bare", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the bare send")
	}
	defer seal2(true, nil)
	bareID := activityTurn(t, ws2, "c_bare")
	sendCommand(t, ws2, `{"type":"turn.watch","requestId":"w-bare","conversationId":"c_bare","turnId":"`+bareID+`","afterSeq":-1}`)
	bareOpened := readUntil(t, ws2, "turn.opened")
	if !strings.Contains(string(bareOpened.Payload), `"steerable":false`) {
		t.Fatalf("unwired worker send opened %s, want steerable false", bareOpened.Payload)
	}
	sendCommand(t, ws2, `{"type":"turn.watch","requestId":"w-bare2","conversationId":"c_bare","turnId":"`+bareID+`","afterSeq":1000}`)
	bareResync := readUntil(t, ws2, "turn.resync_required")
	if !strings.Contains(string(bareResync.Payload), `"steerable":false`) {
		t.Fatalf("unwired worker send resync %s, want steerable false", bareResync.Payload)
	}
}
