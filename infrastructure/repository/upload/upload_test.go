package repoupload

import (
	"os"
	"testing"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Purge leaves a live, empty store: every upload dir is gone, the root is
// recreated private, and a new Save lands there.
func TestPurge(t *testing.T) {
	root := t.TempDir() + "/uploads"
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(t.Context(), "a.txt", "text/plain", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Save(t.Context(), "b.png", "image/png", []byte("png")); err != nil {
		t.Fatal(err)
	}
	if err := store.Purge(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("upload root holds %d entries: %v", len(entries), err)
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("root mode = %v: %v", info.Mode().Perm(), err)
	}
	if _, _, err := store.Save(t.Context(), "c.txt", "text/plain", []byte("y")); err != nil {
		t.Fatalf("save after purge: %v", err)
	}
}

// Delete's address is an upload id and nothing else. Ids the store never minted
// — a room, a path, an absolute path, a traversal — answer not-found and remove
// nothing, so a caller cannot reach a file it was never handed.
func TestDeleteOnlyTakesAnUploadID(t *testing.T) {
	root := t.TempDir() + "/uploads"
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	keep, _, err := store.Save(t.Context(), "keep.txt", "text/plain", []byte("mine"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c_k4j9xm2p8q1vz7w0t6nb5r3yhd", "../uploads", "/etc", "u_../../etc", "", "meta.json"} {
		if err := store.Delete(t.Context(), id); apperror.As(err).StatusCode() != 404 {
			t.Fatalf("delete %q = %v, want not-found", id, err)
		}
	}
	if _, _, err := store.Read(t.Context(), keep); err != nil {
		t.Fatalf("a refused delete took the upload anyway: %v", err)
	}
}

// A cleanup takes back exactly the id it was handed, and repeats safely: the
// store mints every id, so one refused send cannot take another room's copy,
// and its own earlier cleanup is not an error.
func TestDeleteTakesBackOnlyItsOwnID(t *testing.T) {
	root := t.TempDir() + "/uploads"
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	mine, _, err := store.Save(t.Context(), "a.txt", "text/plain", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	theirs, _, err := store.Save(t.Context(), "b.txt", "text/plain", []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), mine); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Read(t.Context(), mine); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("the deleted upload still reads: %v", err)
	}
	if err := store.Delete(t.Context(), mine); err != nil {
		t.Fatalf("a repeated cleanup must be the requested state, not an error: %v", err)
	}
	if _, _, err := store.Read(t.Context(), theirs); err != nil {
		t.Fatalf("another upload went with it: %v", err)
	}
	// An upload id this store never minted is still the requested state: a
	// retried cleanup must not fail on work an earlier attempt already did.
	unknown, _, err := store.Save(t.Context(), "c.txt", "text/plain", []byte("c"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), unknown); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(t.Context(), unknown); err != nil {
		t.Fatalf("a repeated cleanup of its own id must answer nil: %v", err)
	}
}
