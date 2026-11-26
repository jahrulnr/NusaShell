package wsconversation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// A tab adopting a live turn from the activity notice must see the same
// startedAt a watcher's replayed turn.opened carries — the server's
// admission wall-clock, not the moment the tab happened to look.
func TestActivityCarriesTheOpenedStartedAt(t *testing.T) {
	h := newHub(t, stubSender{})
	ws := dial(t, h)
	handler, seal := h.Open("c_time", "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	defer seal(true, nil)

	var act activityPayload
	tid := ""
	for tid == "" {
		f := readAnyFrame(t, ws)
		if f.Type == "conversation.activity" {
			tid = f.TurnID
			if err := json.Unmarshal(f.Payload, &act); err != nil {
				t.Fatal(err)
			}
		}
	}
	if act.StartedAt == 0 {
		t.Fatal("activity carries no startedAt — an adopting tab falls back to blank")
	}
	// The wall-clock is the admission moment: before the open, after test start.
	now := time.Now().UnixMilli()
	if act.StartedAt > now || now-act.StartedAt > 10_000 {
		t.Fatalf("startedAt %d is not the admission wall-clock (now %d)", act.StartedAt, now)
	}

	// The watcher's replayed turn.opened must agree exactly.
	sendCommand(t, ws, `{"type":"turn.watch","requestId":"w1","conversationId":"c_time","turnId":"`+tid+`","afterSeq":-1}`)
	go handler(event.NewDeltaEvent("x"))
	for {
		f := readFrame(t, ws)
		if f.Type != "turn.opened" {
			continue
		}
		var op openedPayload
		if err := json.Unmarshal(f.Payload, &op); err != nil {
			t.Fatal(err)
		}
		if op.StartedAt != act.StartedAt {
			t.Fatalf("opened startedAt %d != activity startedAt %d — two clocks for one turn", op.StartedAt, act.StartedAt)
		}
		return
	}
}
