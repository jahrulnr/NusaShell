package repothreadview

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// Purge drops every row — the fresh-room document under the "" key is a
// row like any other and must go too.
func TestPurge(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := New(db)

	for _, key := range []string{"c_1", "c_2", ""} {
		if _, err := r.Put(ctx, key, json.RawMessage(`{"x":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM thread_views`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("thread_views holds %d rows (err=%v)", n, err)
	}
	if _, err := r.Put(ctx, "", json.RawMessage(`{"x":2}`)); err != nil {
		t.Fatalf("put after purge: %v", err)
	}
}
