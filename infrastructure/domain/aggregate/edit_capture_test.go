package aggregate

import (
	"errors"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// editRoom is a room where an edit of m1 drops the reply and the line after it.
func editRoom(t *testing.T) *Conversation {
	t.Helper()
	c := mustConv(t)
	for _, line := range []struct {
		id   string
		role enum.Role
		text string
	}{{"m1", enum.RoleUser, "old"}, {"a1", enum.RoleAssistant, "answer"}, {"m2", enum.RoleUser, "follow-up"}} {
		m, err := entity.NewMessage(line.role, line.text)
		if err != nil {
			t.Fatal(err)
		}
		m.SetID(line.id)
		c.Append(m)
	}
	return c
}

func TestEditCaptureReadsWhatRewriteFromDrops(t *testing.T) {
	c := editRoom(t)
	previous, tail, ok := c.EditCapture("m1")
	if !ok || previous != "old" || len(tail) != 2 || tail[0].ID() != "a1" || tail[1].Content() != "follow-up" {
		t.Fatalf("capture = %q, %+v, %v", previous, tail, ok)
	}
	if len(c.Messages()) != 3 || c.Messages()[0].Content() != "old" {
		t.Fatal("EditCapture changed the conversation")
	}
	if err := c.RewriteFrom("m1", "new"); err != nil {
		t.Fatal(err)
	}
	if len(c.Messages()) != 1 || c.Messages()[0].Content() != "new" {
		t.Fatalf("after RewriteFrom the room holds %d messages", len(c.Messages()))
	}
}

// RewriteFrom clears the messages it drops, and a later append reuses their
// slots: a capture that aliased the backing array would read the new lines.
func TestEditCaptureSurvivesTheRewriteAndLaterAppends(t *testing.T) {
	c := editRoom(t)
	_, tail, _ := c.EditCapture("m1")
	if err := c.RewriteFrom("m1", "new"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"x", "y"} {
		m, err := entity.NewMessage(enum.RoleUser, text)
		if err != nil {
			t.Fatal(err)
		}
		c.Append(m)
	}
	if len(tail) != 2 || tail[0].Content() != "answer" || tail[1].Content() != "follow-up" {
		t.Fatalf("captured tail was overwritten: %+v", tail)
	}
}

func TestEditCaptureRefusesWhereRewriteFromRefuses(t *testing.T) {
	c := editRoom(t)
	relay, err := entity.NewMessage(enum.RoleUser, "from another thread")
	if err != nil {
		t.Fatal(err)
	}
	relay.SetID("r1")
	relay.SetUserType(enum.LLMUserTypeRelay)
	c.Append(relay)
	for id, want := range map[string]error{"a1": ErrNotUserMessage, "r1": ErrNotUserMessage, "nope": ErrMessageNotFound} {
		if _, _, ok := c.EditCapture(id); ok {
			t.Fatalf("EditCapture accepted %q", id)
		}
		if err := c.RewriteFrom(id, "x"); !errors.Is(err, want) {
			t.Fatalf("RewriteFrom(%q) = %v, want %v", id, err, want)
		}
	}
}
