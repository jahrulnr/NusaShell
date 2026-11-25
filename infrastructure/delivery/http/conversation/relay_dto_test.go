package conversation

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// The wire carries the provenance beside userType, so a surface draws the header
// from data instead of parsing text.
func TestMessageDTOCarriesTheRelay(t *testing.T) {
	m, err := entity.NewMessage(enum.RoleUser, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if toMessage(m).Relay != nil {
		t.Fatal("an ordinary message carries a relay")
	}
	m.SetUserType(enum.LLMUserTypeRelay)
	m.SetRelay(valueobject.NewRelay("c_sender", "otter", "Planner"))
	out := toMessage(m)
	if out.Relay == nil || out.Relay.ID != "c_sender" || out.Relay.Label != "otter" || out.Relay.Title != "Planner" {
		t.Fatalf("relay = %+v", out.Relay)
	}
	if out.Content != "ship it" {
		t.Fatalf("dto content = %q, want the stored body alone", out.Content)
	}
}
