package repochannel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

func link(t *testing.T, channel, chat, conv, display string) aggregate.ChannelLink {
	t.Helper()
	ref, err := valueobject.NewChatRef(channel, chat, "")
	if err != nil {
		t.Fatal(err)
	}
	l, err := aggregate.NewChannelLink(ref, conv, display, false, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLinks(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := New(db)

	for _, l := range []aggregate.ChannelLink{
		link(t, "whatsapp", "62811", "c1", "Zed"),
		link(t, "telegram", "42", "c2", "Bob"),
		link(t, "whatsapp", "62812", "c1", "Ann"),
	} {
		if err := r.Save(ctx, l); err != nil {
			t.Fatal(err)
		}
	}

	all, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range all {
		got = append(got, l.Chat().Channel()+"/"+l.Display())
	}
	want := []string{"telegram/Bob", "whatsapp/Ann", "whatsapp/Zed"}
	if len(got) != len(want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List = %v, want %v", got, want)
		}
	}

	room, err := r.ListByConversation(ctx, "c1")
	if err != nil || len(room) != 2 {
		t.Fatalf("ListByConversation(c1) = %d links, %v; want 2", len(room), err)
	}

	moved := link(t, "whatsapp", "62811", "c2", "Zed")
	if err := r.Save(ctx, moved); err != nil {
		t.Fatal(err)
	}
	found, err := r.Find(ctx, moved.Chat())
	if err != nil || found.ConversationID() != "c2" {
		t.Fatalf("Find after relink = %v, %v; want c2", found, err)
	}

	if err := r.DeleteByConversation(ctx, "c2"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Find(ctx, moved.Chat()); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("Find after delete = %v, want 404", err)
	}
}
