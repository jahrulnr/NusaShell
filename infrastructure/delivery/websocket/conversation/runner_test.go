package wsconversation

import (
	"context"
	"testing"
	"time"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// An externally opened turn is a normal hub turn: the web hears it
// open, the caller observes its events, it can be steered and cancelled,
// and the finished hook fires marked external.
func TestRunIsAWebVisibleTurn(t *testing.T) {
	release := make(chan struct{})
	h := newHub(t, stubSender{
		fn: func(ctx context.Context, id, _, _, _ string, onEvent event.TurnEventHandler) (appconversation.SendResult, error) {
			onEvent(event.NewDeltaEvent("hi"))
			select {
			case <-release:
			case <-ctx.Done():
				return appconversation.SendResult{ConversationID: id, UserMessagePersisted: true}, apperror.New(408, "turn canceled", ctx.Err())
			}
			return appconversation.SendResult{ConversationID: id, UserMessagePersisted: true}, nil
		},
	})
	ws := dial(t, h)
	finished := make(chan bool, 2)
	h.OnFinished(func(_, _ string, _ valueobject.TurnOrigin, external bool, _ error) { finished <- external })

	var seen []enum.TurnEventKind
	done := make(chan error, 1)
	go func() {
		_, _, err := h.Run(context.Background(), "c_ext", "", "", "hello", nil, appjob.SendOptions{}, func(ev event.TurnEvent) {
			seen = append(seen, ev.Kind())
		})
		done <- err
	}()
	// The web hears the room open.
	for f := readAnyFrame(t, ws); f.Type != "conversation.activity"; f = readAnyFrame(t, ws) {
	}
	waitUntil(t, func() bool { return h.Busy("c_ext") })
	if !h.Steer("c_ext", "also this") {
		t.Fatal("a running external turn must take steering")
	}
	h.mu.Lock()
	var got []string
	for _, tr := range h.turns {
		if tr.conversationID == "c_ext" && !tr.terminal {
			for _, rec := range tr.steerRecords {
				got = append(got, rec.content)
			}
		}
	}
	h.mu.Unlock()
	if len(got) != 1 || got[0] != "also this" {
		t.Fatalf("steering = %q", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != enum.TurnEventDelta {
		t.Fatalf("observed = %v", seen)
	}
	if ext := <-finished; !ext {
		t.Fatal("the finished hook must mark the turn external")
	}
	if h.Busy("c_ext") || h.Steer("c_ext", "late") {
		t.Fatal("a finished turn is neither busy nor steerable")
	}

	// Cancel stops a running external turn.
	release = make(chan struct{})
	go func() {
		_, _, err := h.Run(context.Background(), "c_ext", "", "", "again", nil, appjob.SendOptions{}, nil)
		done <- err
	}()
	waitUntil(t, func() bool { return h.Busy("c_ext") })
	if !h.Cancel("c_ext") {
		t.Fatal("cancel found no turn")
	}
	if err := <-done; apperror.As(err).StatusCode() != 408 {
		t.Fatalf("cancelled run err = %v", err)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The finished hook names the command that opened the turn — edit and
// resume listeners can tell a regenerated reply from a new answer.
func TestFinishedReportsTheTurnKind(t *testing.T) {
	h := newHub(t, stubSender{
		efn: func(context.Context, string, string, string, string, string, appjob.SendOptions, event.TurnEventHandler) error {
			return nil
		},
	})
	ws := dial(t, h)
	kind := make(chan string, 1)
	h.OnFinished(func(_, k string, _ valueobject.TurnOrigin, _ bool, _ error) { kind <- k })
	sendCommand(t, ws, `{"type":"turn.edit","requestId":"e","conversationId":"c_1","payload":{"messageId":"m_1","content":"redo"}}`)
	for f := readFrame(t, ws); f.Type != "turn.completed"; f = readFrame(t, ws) {
		if f.Type == "error" || f.Type == "turn.failed" {
			t.Fatalf("turn rejected: %+v", f)
		}
	}
	if k := <-kind; k != "edit" {
		t.Fatalf("kind = %q, want edit", k)
	}
}
