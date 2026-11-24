package job

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
)

// A steer writes an ordinary user message, and that message names the request
// that submitted it: after a reload, the history line and the journal's live
// steering bubble are the same submission. An ordinary send keeps no such
// name.
func TestSteerMessageCarriesInputID(t *testing.T) {
	base := repoconversation.NewConversationRepository(t.TempDir())
	conv, d := seedSteerRoom(t, base)
	steers := NewSteeringInput()
	steers.Submit(valueobject.UserInput{Text: "steer me", InputID: "req-42"})

	if err := d.runTurn(t.Context(), "room", conv, EnvironmentBundle{}, nil, entity.Model{}, doneSvc{}, nil, steers, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}

	loaded, err := base.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	var steer, sent bool
	for _, m := range loaded.Messages() {
		switch m.Content() {
		case "steer me":
			steer = true
			if m.InputID() != "req-42" {
				t.Fatalf("steer message inputId = %q, want req-42", m.InputID())
			}
		case "hi":
			sent = true
			if m.InputID() != "" {
				t.Fatalf("sent message carries inputId %q", m.InputID())
			}
		}
	}
	if !steer || !sent {
		t.Fatalf("history lacks the sent or the steered line: %+v", loaded.Messages())
	}
}
