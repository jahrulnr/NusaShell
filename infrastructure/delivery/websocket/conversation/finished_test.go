package wsconversation

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

type finishedCall struct {
	convID, kind string
	origin       valueobject.TurnOrigin
	external     bool
	err          error
}

func finishRecorder(h *Hub) chan finishedCall {
	calls := make(chan finishedCall, 8)
	h.OnFinished(func(convID, kind string, origin valueobject.TurnOrigin, external bool, err error) {
		calls <- finishedCall{convID, kind, origin, external, err}
	})
	return calls
}

func oneCall(t *testing.T, calls chan finishedCall) finishedCall {
	t.Helper()
	select {
	case c := <-calls:
		select {
		case extra := <-calls:
			t.Fatalf("one turn answered two finished calls: %+v then %+v", c, extra)
		case <-time.After(150 * time.Millisecond):
		}
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("the finished hook never fired for a sealed turn")
	}
	return finishedCall{}
}

// A turn a room job ran — opened through the live surface, sealed by the
// job — answers exactly one finished call, with its own kind and no error.
func TestFinishedFiresOnceForAJobTurn(t *testing.T) {
	h := newHub(t, stubSender{})
	calls := finishRecorder(h)
	handler, seal := h.Open("c_job", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	seal(true, nil)
	c := oneCall(t, calls)
	if c.convID != "c_job" || c.kind != "send" {
		t.Fatalf("call = %+v, want convID c_job kind send", c)
	}
	if c.external {
		t.Fatal("a journal-only turn is not external — no caller waits on it")
	}
	if c.err != nil {
		t.Fatalf("a clean seal must report nil err, got %v", c.err)
	}
}

// A turn the hub admitted answers exactly one finished call too — the hook
// lives in the seal, and runTurn must not add a second one.
func TestFinishedFiresOnceForAnAdmittedTurn(t *testing.T) {
	b := newBlockedTurn()
	h := newHub(t, stubSender{fn: b.send})
	calls := finishRecorder(h)
	ws := dial(t, h)
	sendCommand(t, ws, `{"type":"turn.send","requestId":"s1","payload":{"content":"x"}}`)
	f := readFrame(t, ws)
	if f.Type != "turn.opened" {
		t.Fatalf("%+v", f)
	}
	<-b.started
	close(b.release)
	c := oneCall(t, calls)
	if c.err != nil {
		t.Fatalf("the admitted turn ended clean, hook err = %v", c.err)
	}
}

// The error the turn actually ended with reaches the hook: a canceled job
// turn must not look like a success to AutoTitle, chats, and alerts.
func TestFinishedCarriesTheTurnsRealError(t *testing.T) {
	h := newHub(t, stubSender{})
	calls := finishRecorder(h)
	handler, seal := h.Open("c_err", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	seal(true, fmt.Errorf("send canceled: %w", errors.New("interrupted")))
	c := oneCall(t, calls)
	if c.err == nil {
		t.Fatal("a turn that ended in error must not report nil to the hook")
	}
}

// A duplicate seal — the job's deferred seal racing a shutdown seal — still
// answers one call: the terminal guard is what makes the count exact.
func TestFinishedSurvivesADuplicateSeal(t *testing.T) {
	h := newHub(t, stubSender{})
	calls := finishRecorder(h)
	handler, seal := h.Open("c_dup", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	seal(true, nil)
	h.sealTurn(mustTurn(t, h, "c_dup"), true, nil)
	oneCall(t, calls)
}

func mustTurn(t *testing.T, h *Hub, convID string) *turn {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, tr := range h.turns {
		if tr.conversationID == convID {
			return tr
		}
	}
	t.Fatal("turn not found")
	return nil
}

// The origin the job carried reaches the hook unchanged — a completion
// wake must arrive as completion, or the consumers' filters decide on a
// fact the hook never had.
func TestFinishedCarriesTheTurnsOrigin(t *testing.T) {
	h := newHub(t, stubSender{})
	calls := finishRecorder(h)
	handler, seal := h.Open("c_org", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginCompletion})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	seal(true, nil)
	c := oneCall(t, calls)
	if c.origin.Kind != enum.TurnOriginCompletion {
		t.Fatalf("origin = %q, want completion", c.origin.Kind)
	}
}
