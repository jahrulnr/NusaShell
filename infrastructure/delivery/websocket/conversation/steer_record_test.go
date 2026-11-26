package wsconversation

import (
	"encoding/json"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

func watchSteerFrame(t *testing.T, h *Hub, convID string, ev event.TurnEvent) steerPayload {
	t.Helper()
	// The socket must be attached before Open: the activity edge is a live
	// broadcast, and a dial that lands after it never learns the turn id.
	ws := dial(t, h)
	handler, seal := h.Open(convID, "send", valueobject.TurnOrigin{Kind: enum.TurnOriginConversation})
	if handler == nil {
		t.Fatal("surface refused the turn")
	}
	defer seal(true, nil)
	tid := ""
	for tid == "" {
		f := readAnyFrame(t, ws)
		if f.Type == "conversation.activity" {
			tid = f.TurnID
		}
	}
	sendCommand(t, ws, `{"type":"turn.watch","requestId":"w1","conversationId":"`+convID+`","turnId":"`+tid+`","afterSeq":-1}`)
	go handler(ev)
	for {
		f := readFrame(t, ws)
		if f.Type != "turn.steer" {
			continue
		}
		var p steerPayload
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
}

// A relayed steer carries the id of the line accept already wrote, so the
// watcher pairs the fold with that row instead of rendering it twice.
func TestRecordedSteerFrameCarriesTheRecordID(t *testing.T) {
	h := newHub(t, stubSender{})
	p := watchSteerFrame(t, h, "c_rec", event.NewRecordedSteerEvent("relay text", "m_record42"))
	if p.InputID != "m_record42" {
		t.Fatalf("inputId = %q, want the record id", p.InputID)
	}
}

// An owner steer has no record: inputId stays empty exactly as before —
// the receipt correlation is the only source, and nothing may change for
// the non-relay path.
func TestUnrecordedSteerFrameKeepsInputIDEmpty(t *testing.T) {
	h := newHub(t, stubSender{})
	p := watchSteerFrame(t, h, "c_own", event.NewSteerEvent("owner text"))
	if p.InputID != "" {
		t.Fatalf("inputId = %q, want empty for an unrecorded steer", p.InputID)
	}
}
