package appconversation

import (
	"os"
	"path/filepath"
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	"github.com/jahrulnr/nusashell/infrastructure/repository/mediadescription"
	repoupload "github.com/jahrulnr/nusashell/infrastructure/repository/upload"
)

// A single-room delete leaves that room's uploads behind: uploads are
// owner-scoped, not room-scoped, so nothing in a room delete knows which of
// them only it pointed at. Delete-all is the sweep that reclaims them, and
// this test keeps the bound visible — an orphan lives until the next
// delete-all, and no longer.
//
// The other two dimensions are already pinned in delete_test.go:
// TestDeleteAllPurgesArtifacts (an upload nothing references is purged) and
// TestDeleteAllFailureKeepsArtifacts (a room that would not delete keeps every
// artifact, so no surviving room loses a file it points at).
func TestDeletingOneRoomLeavesItsUploadsForDeleteAll(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	uploads, err := repoupload.New(root)
	if err != nil {
		t.Fatal(err)
	}
	app, _ := fixtureWith(t, okHandler, nil)
	WithUploads(uploads, nil)(app)

	if _, err := app.Send(t.Context(), "room", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	id, _, err := uploads.Save(t.Context(), "kept.png", "image/png", []byte("png"))
	if err != nil {
		t.Fatal(err)
	}

	if err := app.Delete(t.Context(), "room"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != id {
		t.Fatalf("after a room delete the store holds %v, want the orphan %q", entries, id)
	}
	// Orphaned, not corrupted: the reference still resolves until the sweep.
	if _, err := uploads.Get(t.Context(), id); err != nil {
		t.Fatalf("orphaned upload unreadable: %v", err)
	}

	// Delete-all closes the bound.
	if _, err := app.DeleteAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("delete-all left %d upload entries: %v", len(entries), err)
	}
}

var _ = repoconversation.NewConversationRepository

// Deleting one room forgets the descriptions its files produced: that text is
// the room's, and it does not wait for a delete-all to leave.
func TestDeletePurgesTheRoomsMediaDescriptions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media-descriptions")
	descriptions := mediadescription.New(root)
	app, _ := fixtureWith(t, okHandler, nil)
	app.UseMediaDescriptions(descriptions)

	for _, room := range []string{"room", "other"} {
		if _, err := app.Send(t.Context(), room, "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
			t.Fatal(err)
		}
		if err := descriptions.Put(room, "key", "text of "+room); err != nil {
			t.Fatal(err)
		}
	}

	if err := app.Delete(t.Context(), "room"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := descriptions.Get("room", "key"); ok {
		t.Fatal("the deleted room still answers with its description")
	}
	if entries, err := os.ReadDir(filepath.Join(root, "room")); err == nil && len(entries) != 0 {
		t.Fatalf("the deleted room left %d description files", len(entries))
	}
	if _, ok, _ := descriptions.Get("other", "key"); !ok {
		t.Fatal("the other room's description was purged with it")
	}

	// Delete-all sweeps what is left.
	if _, err := app.DeleteAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("delete-all left %d description entries: %v", len(entries), err)
	}
}
