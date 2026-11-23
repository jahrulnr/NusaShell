package repochatindex_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repochatindex "github.com/jahrulnr/nusashell/infrastructure/repository/chatindex"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

func TestChatIndex(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ix := repochatindex.New(db)
	base := time.UnixMilli(1_700_000_000_000)
	msg := func(id, text string, at int, live bool) drepo.ChatMessage {
		return drepo.ChatMessage{Channel: "whatsapp", Chat: "1@s.whatsapp.net", ID: id, Sender: "1@s.whatsapp.net",
			At: base.Add(time.Duration(at) * time.Second), Text: text, Live: live, Raw: []byte(`{"conversation":"` + text + `"}`)}
	}
	for _, m := range []drepo.ChatMessage{
		msg("a", "Selamat pagi, rapat jam sembilan", 1, true),
		msg("b", "rapat dipindah", 2, true),
		msg("c", "makan siang?", 3, true),
		{Channel: "telegram", Chat: "9", ID: "a", At: base, Text: "rapat telegram", Live: true},
	} {
		if err := ix.Save(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	// A history copy never replaces the live one.
	if err := ix.Save(ctx, msg("b", "stale history", 9, false)); err != nil {
		t.Fatal(err)
	}
	page, err := ix.Search(ctx, drepo.ChatSearch{Channel: "whatsapp", Query: "RAPAT", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].ID != "b" || page.NextCursor == "" || page.Messages[0].Raw != nil {
		t.Fatalf("first page = %+v", page)
	}
	page, err = ix.Search(ctx, drepo.ChatSearch{Channel: "whatsapp", Query: "rapat", Cursor: page.NextCursor, Limit: 5, Raw: true})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != "a" || page.NextCursor != "" || page.Messages[0].Raw == nil {
		t.Fatalf("second page = %+v, %v", page, err)
	}
	// Quotes fall back to a substring scan.
	if page, _ = ix.Search(ctx, drepo.ChatSearch{Channel: "whatsapp", Query: `siang?"`, Limit: 5}); len(page.Messages) != 0 {
		t.Fatalf("quoted miss = %+v", page)
	}
	if page, _ = ix.Search(ctx, drepo.ChatSearch{Channel: "whatsapp", Query: `"makan siang"`, Limit: 5}); len(page.Messages) != 1 {
		t.Fatalf("phrase = %+v", page)
	}
	if _, err := ix.Search(ctx, drepo.ChatSearch{Channel: "whatsapp", Limit: 0}); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("limit 0 err = %v", err)
	}

	// Edit, receipts that only move forward, revoke.
	if err := ix.UpdateText(ctx, "whatsapp", "1@s.whatsapp.net", "c", "makan malam?"); err != nil {
		t.Fatal(err)
	}
	if page, _ = ix.Search(ctx, drepo.ChatSearch{Channel: "whatsapp", Query: "malam", Limit: 5}); len(page.Messages) != 1 {
		t.Fatalf("edited text not searchable: %+v", page)
	}
	out := msg("d", "sent by me", 4, true)
	out.FromMe, out.Explicit = true, true
	if err := ix.Save(ctx, out); err != nil {
		t.Fatal(err)
	}
	for _, r := range []valueobject.Receipt{valueobject.ReceiptRead, valueobject.ReceiptDelivered} {
		if err := ix.MarkReceipt(ctx, "whatsapp", "1@s.whatsapp.net", []string{"d", "a", "missing"}, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ix.Get(ctx, "whatsapp", "1@s.whatsapp.net", "d")
	if err != nil || got.Receipt != valueobject.ReceiptRead || !got.Explicit {
		t.Fatalf("d = %+v, %v", got, err)
	}
	// A later save keeps the receipt and the explicit mark.
	out.Explicit = false
	out.At = out.At.Add(time.Second)
	if err := ix.Save(ctx, out); err != nil {
		t.Fatal(err)
	}
	if got, _ = ix.Get(ctx, "whatsapp", "1@s.whatsapp.net", "d"); got.Receipt != valueobject.ReceiptRead || !got.Explicit {
		t.Fatalf("after resave d = %+v", got)
	}
	if got, _ = ix.Get(ctx, "whatsapp", "1@s.whatsapp.net", "a"); got.Receipt != valueobject.ReceiptNone {
		t.Fatal("a receipt landed on an incoming message")
	}
	if err := ix.Revoke(ctx, "whatsapp", "1@s.whatsapp.net", "a"); err != nil {
		t.Fatal(err)
	}
	if page, _ = ix.Search(ctx, drepo.ChatSearch{Channel: "whatsapp", Query: "pagi", Limit: 5}); len(page.Messages) != 0 {
		t.Fatalf("revoked message still found: %+v", page)
	}
	if _, err := ix.Get(ctx, "whatsapp", "x", "y"); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("missing err = %v", err)
	}

	// Purge is per channel.
	n, err := ix.Purge(ctx, "whatsapp", base.Add(3*time.Second))
	if err != nil || n != 2 {
		t.Fatalf("purged %d, %v", n, err)
	}
	if page, _ = ix.Search(ctx, drepo.ChatSearch{Channel: "telegram", Limit: 5}); len(page.Messages) != 1 {
		t.Fatalf("telegram purged too: %+v", page)
	}
}
