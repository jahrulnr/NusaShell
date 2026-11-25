package mediadescription

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newStore roots one store in a temp directory.
func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "media-descriptions")
	return New(dir), dir
}

func readEntry(t *testing.T, dir, convID, key string) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, convID, key+".txt"))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// A description is remembered per room and served from there: the same key
// answers a hit in its own room.
func TestGetIsPerRoom(t *testing.T) {
	store, dir := newStore(t)
	if err := store.Put("c_room1", "abc123", "a screenshot of a failing test"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := store.Get("c_room1", "abc123"); err != nil || !ok || got != "a screenshot of a failing test" {
		t.Fatalf("get = %q, %v", got, ok)
	}
	if _, ok := readEntry(t, dir, "c_room1", "abc123"); !ok {
		t.Fatal("the entry is not on disk")
	}

	// Another room with the same key is a miss: text lifted from one room's
	// files must never surface in another.
	if got, ok, _ := store.Get("c_room2", "abc123"); ok || got != "" {
		t.Fatalf("cross-room get = %q, %v, want a miss", got, ok)
	}
}

// Purge forgets one room and leaves the others alone.
func TestPurgeClearsOneRoomOnly(t *testing.T) {
	store, dir := newStore(t)
	for _, convID := range []string{"c_room1", "c_room2"} {
		if err := store.Put(convID, "k1", "text of "+convID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Purge("c_room1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get("c_room1", "k1"); ok {
		t.Fatal("the purged room still answers")
	}
	if _, ok := readEntry(t, dir, "c_room1", "k1"); ok {
		t.Fatal("the purged room's file survived")
	}
	if got, ok, _ := store.Get("c_room2", "k1"); !ok || got != "text of c_room2" {
		t.Fatalf("the other room = %q, %v, want it intact", got, ok)
	}
}

// PurgeAll empties every room and leaves the root usable.
func TestPurgeAllClearsEverything(t *testing.T) {
	store, dir := newStore(t)
	for _, convID := range []string{"c_room1", "c_room2"} {
		if err := store.Put(convID, "k1", "text"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PurgeAll(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("root gone: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("PurgeAll left %d entries", len(entries))
	}
	// And the store still works after the sweep.
	if err := store.Put("c_room1", "k2", "again"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get("c_room1", "k2"); !ok {
		t.Fatal("the store stopped working after PurgeAll")
	}
}

// The budget trims the least-recently-used entries of a room first, and only
// that room.
func TestPutTrimsTheRoomsOldestEntries(t *testing.T) {
	store, dir := newStore(t)
	store.WithLimit(40)

	if err := store.Put("c_room1", "old", strings.Repeat("o", 30)); err != nil {
		t.Fatal(err)
	}
	// Mark the first entry as the least recently used, then add a second that
	// pushes the room over its budget.
	older := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "c_room1", "old.txt"), older, older); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("c_room2", "kept", strings.Repeat("k", 20)); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("c_room1", "new", strings.Repeat("n", 30)); err != nil {
		t.Fatal(err)
	}
	if _, ok := readEntry(t, dir, "c_room1", "old"); ok {
		t.Fatal("the room's oldest entry survived the trim")
	}
	if _, ok := readEntry(t, dir, "c_room1", "new"); !ok {
		t.Fatal("the newest entry was trimmed")
	}
	if _, ok := readEntry(t, dir, "c_room2", "kept"); !ok {
		t.Fatal("the trim reached another room")
	}
}

// An id or key shaped like a path addresses nothing: the store never writes or
// reads outside its own tree.
func TestPathShapedNamesAreRefused(t *testing.T) {
	store, dir := newStore(t)
	for _, name := range []string{"../escape", "a/b", `a\b`, "..", ""} {
		if err := store.Put(name, "k", "text"); err != nil {
			t.Fatalf("put %q: %v", name, err)
		}
		if _, ok, _ := store.Get(name, "k"); ok {
			t.Fatalf("get %q answered", name)
		}
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) != 0 {
		t.Fatalf("a path-shaped name created %v", entries)
	}
}
