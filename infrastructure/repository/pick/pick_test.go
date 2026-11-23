package repopick

import (
	"os"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

func TestSaveGet(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(t.Context(), valueobject.Pick{ConversationID: "c1", Marker: 2, Upload: "u_1",
		Descriptor: valueobject.AnnotationDescriptor{Kind: enum.AnnotationElement, TagName: "a", Selector: "nav > a"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context(), saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != saved.ID || got.ConversationID != "c1" || got.Marker != 2 || got.Upload != "u_1" || got.Descriptor.Selector != "nav > a" || got.Descriptor.Kind != enum.AnnotationElement {
		t.Fatalf("got %+v", got)
	}
	for _, id := range []string{"pk_missing", "../pk_x", "u_1", ""} {
		if _, err := store.Get(t.Context(), id); apperror.As(err).StatusCode() != apperror.StatusNotFound {
			t.Errorf("Get(%q) err = %v, want 404", id, err)
		}
	}
}

// Purge leaves a live, empty store: every pick JSON is gone, the root is
// recreated private, and a new Save lands there.
func TestPurge(t *testing.T) {
	root := t.TempDir() + "/picks"
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(t.Context(), valueobject.Pick{ConversationID: "c1", Marker: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Purge(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("pick root holds %d entries: %v", len(entries), err)
	}
	if info, err := os.Stat(root); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("root mode = %v: %v", info.Mode().Perm(), err)
	}
	if _, err := store.Save(t.Context(), valueobject.Pick{ConversationID: "c2", Marker: 2}); err != nil {
		t.Fatalf("save after purge: %v", err)
	}
}
