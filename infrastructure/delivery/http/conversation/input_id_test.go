package conversation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// A message a steer wrote carries the submitting request id on the wire, at the
// message's top level; every other message omits the field, so a client can
// match a reloaded line against the live steering bubble by name alone.
func TestInputIDInHistoryProjection(t *testing.T) {
	steer, err := entity.NewUserMessage(valueobject.UserInput{Text: "steer me", InputID: "req-9"})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := entity.NewUserMessage(valueobject.UserInput{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := entity.NewMessage(enum.RoleAssistant, "hello")
	if err != nil {
		t.Fatal(err)
	}
	// Even a hand-built assistant message cannot leak the field onto the wire.
	reply.SetInputID("req-9")

	raw, err := json.Marshal(toMessage(steer))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["inputId"] != "req-9" {
		t.Fatalf("steer message projection = %s", raw)
	}

	for name, m := range map[string]entity.Message{"sent": sent, "assistant": reply} {
		raw, err := json.Marshal(toMessage(m))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "inputId") {
			t.Fatalf("%s message carries inputId: %s", name, raw)
		}
	}
}
