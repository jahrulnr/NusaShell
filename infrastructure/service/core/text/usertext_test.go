package coretext

import (
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// A request reads the sender's head above the body; the message itself keeps the
// body alone, so history and the wire never hold the same text twice.
func TestUserTextRendersTheHeadAtRequestTime(t *testing.T) {
	m, err := entity.NewMessage(enum.RoleUser, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if got := UserText(m); got != "ship it" {
		t.Fatalf("an ordinary message = %q, want its content", got)
	}
	m.SetRelay(valueobject.NewRelay("c_sender", "otter", "Planner"))
	got := UserText(m)
	if !strings.HasPrefix(got, "[From thread @otter Planner (c_sender)]\n") || !strings.HasSuffix(got, "ship it") {
		t.Fatalf("rendered = %q", got)
	}
	if strings.Contains(m.Content(), "From thread") {
		t.Fatalf("rendering touched the stored body: %q", m.Content())
	}
	// A relayed message whose body is empty still renders: the head is text.
	empty, _ := entity.NewMessage(enum.RoleUser, "")
	empty.SetRelay(valueobject.NewRelay("c_sender", "", ""))
	if got := UserText(empty); got == "" {
		t.Fatal("a relayed message with no body rendered nothing")
	}
}
