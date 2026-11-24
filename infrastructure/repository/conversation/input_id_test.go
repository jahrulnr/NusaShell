package repoconversation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// A steer's submitting request id is part of the stored message: it survives a
// reload, and a record written without one (an ordinary send, or a message
// stored before the field existed) reads back empty.
func TestInputIDRoundTrips(t *testing.T) {
	dir := t.TempDir()
	repo := NewConversationRepository(dir)
	room := newConv(t, "c_input_id")
	if err := repo.Save(t.Context(), room); err != nil {
		t.Fatal(err)
	}

	steer, err := entity.NewUserMessage(valueobject.UserInput{Text: "steer me", InputID: "req-7"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := entity.NewUserMessage(valueobject.UserInput{Text: "typed"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []entity.Message{steer, plain} {
		if _, err := repo.AppendMessage(t.Context(), room.ID(), m); err != nil {
			t.Fatal(err)
		}
	}

	// The on-disk shape: the field is written once, and omitted when empty.
	raw, err := os.ReadFile(filepath.Join(dir, room.ID(), activeFile))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), `"inputId"`); n != 1 {
		t.Fatalf("stored lines carry %d inputId fields, want 1:\n%s", n, raw)
	}
	if !strings.Contains(string(raw), `"inputId":"req-7"`) {
		t.Fatalf("stored steer line lacks its inputId:\n%s", raw)
	}

	// A fresh repository over the same directory reads the same values.
	again := NewConversationRepository(dir)
	loaded, err := again.Load(t.Context(), room.ID())
	if err != nil {
		t.Fatal(err)
	}
	msgs := loaded.Messages()
	if len(msgs) != 2 {
		t.Fatalf("history = %d messages, want 2", len(msgs))
	}
	if msgs[0].InputID() != "req-7" {
		t.Fatalf("steer message inputId = %q, want req-7", msgs[0].InputID())
	}
	if msgs[1].InputID() != "" {
		t.Fatalf("plain message carries inputId %q", msgs[1].InputID())
	}
	at, err := again.LoadAt(t.Context(), room.ID(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(at) != 2 || at[0].InputID() != "req-7" || at[1].InputID() != "" {
		t.Fatalf("ranged history inputIds = %+v", at)
	}
}
