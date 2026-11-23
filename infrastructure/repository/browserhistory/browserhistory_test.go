package repobrowserhistory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

func TestHistory(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := New(db)
	at := time.UnixMilli(1_000)
	visit := func(profile, url, title string) {
		t.Helper()
		at = at.Add(time.Second)
		if err := r.Record(ctx, profile, url, title, at); err != nil {
			t.Fatal(err)
		}
	}
	visit("default", "https://example.com/github-notes", "Notes")
	visit("default", "https://github.com/", "GitHub")
	visit("default", "https://example.com/a_b", "")
	visit("default", "https://example.com/github-notes", "")
	visit("work", "https://intranet.test/", "Intranet")

	urls := func(q string) []string {
		t.Helper()
		got, err := r.Search(ctx, "default", q, 8)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, v := range got {
			out = append(out, v.URL)
		}
		return out
	}
	if got := urls(""); len(got) != 3 || got[0] != "https://example.com/github-notes" {
		t.Fatalf("most recent first, this profile only: %v", got)
	}
	if got := urls("git"); len(got) != 2 || got[0] != "https://github.com/" {
		t.Fatalf("a page whose host starts with the query comes first: %v", got)
	}
	if got := urls("a_b"); len(got) != 1 {
		t.Fatalf("an underscore matches itself: %v", got)
	}
	if got := urls("%"); len(got) != 0 {
		t.Fatalf("a percent sign matches itself: %v", got)
	}
	notes, _ := r.Search(ctx, "default", "notes", 1)
	if len(notes) != 1 || notes[0].Visits != 2 || notes[0].Title != "Notes" {
		t.Fatalf("a revisit counts and keeps the known title: %+v", notes)
	}
}
