package repoconversation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// completionRoom stores one empty room and answers its handle plus the tree root.
func completionRoom(t *testing.T) (*ConversationRepository, string) {
	t.Helper()
	root := t.TempDir()
	r := NewConversationRepository(root)
	conv, err := aggregate.NewConversation("c_finish")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	return r, root
}

// The finishes wait in their own file beside the record — a turn's Save rewrites
// the record wholesale, and a process that restarts must still find what it has
// not read.
func TestPendingCompletionsSurviveARestart(t *testing.T) {
	r, root := completionRoom(t)
	recorded, err := r.SaveCompletions(t.Context(), "c_finish", []valueobject.Completion{
		{Source: "session", ID: "7", Status: "exited", Detail: "exit 0"},
	})
	if err != nil || !recorded {
		t.Fatalf("record the finish: recorded=%v err=%v", recorded, err)
	}
	// The turn that ended while the exit was reported saves the record wholesale.
	conv, err := r.Load(t.Context(), "c_finish")
	if err != nil {
		t.Fatal(err)
	}
	conv.SetTitle("a turn that ran while the session finished")
	if err := r.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	// A fresh repository over the same tree is the restart: a new process opens
	// the room's directory and must read the finish it never saw.
	pending, err := NewConversationRepository(root).PendingCompletions(t.Context(), "c_finish")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "7" || pending[0].Detail != "exit 0" || pending[0].At.IsZero() {
		t.Fatalf("pending after restart: %+v", pending)
	}
}

// One exit is one record: the second report of the same source+id is not news,
// and after the room read the first it must not park it again.
func TestCompletionsDedupeByValue(t *testing.T) {
	r, _ := completionRoom(t)
	ctx := t.Context()
	first, err := r.SaveCompletions(ctx, "c_finish", []valueobject.Completion{{Source: "session", ID: "9", Status: "exited"}})
	if err != nil || !first {
		t.Fatalf("first report: recorded=%v err=%v", first, err)
	}
	again, err := r.SaveCompletions(ctx, "c_finish", []valueobject.Completion{{Source: "session", ID: "9", Status: "exited"}})
	if err != nil || again {
		t.Fatalf("a repeated report was recorded again: %v %v", again, err)
	}
	other, err := r.SaveCompletions(ctx, "c_finish", []valueobject.Completion{{Source: "session", ID: "10", Status: "failed"}})
	if err != nil || !other {
		t.Fatalf("a different finish: recorded=%v err=%v", other, err)
	}
	if pending, err := r.PendingCompletions(ctx, "c_finish"); err != nil || len(pending) != 2 || pending[0].ID != "9" || pending[1].ID != "10" {
		t.Fatalf("arrival order: %+v (err %v)", pending, err)
	}
	// The room reads both; a replay of either is still the same record.
	if err := r.ClearCompletions(ctx, "c_finish", []string{"session\x009", "session\x0010"}); err != nil {
		t.Fatal(err)
	}
	if pending, err := r.PendingCompletions(ctx, "c_finish"); err != nil || len(pending) != 0 {
		t.Fatalf("a read record is still pending: %+v (err %v)", pending, err)
	}
	replay, err := r.SaveCompletions(ctx, "c_finish", []valueobject.Completion{{Source: "session", ID: "9", Status: "exited"}})
	if err != nil || replay {
		t.Fatalf("a replayed exit was parked again: recorded=%v err=%v", replay, err)
	}
}

// A delete drops the book with the room: no record outlives the room it belonged
// to.
func TestDropCompletionsRemovesTheBook(t *testing.T) {
	r, root := completionRoom(t)
	ctx := t.Context()
	if _, err := r.SaveCompletions(ctx, "c_finish", []valueobject.Completion{{Source: "session", ID: "3", Status: "exited"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.DropCompletions(ctx, "c_finish"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "c_finish", completionsFile)); !os.IsNotExist(err) {
		t.Fatalf("the book outlived its room: %v", err)
	}
}

// A room that no longer exists has nowhere to park a finish: the report is
// refused, not written into a directory nobody reads.
func TestSaveCompletionsOnAMissingRoomIsRefused(t *testing.T) {
	r, _ := completionRoom(t)
	recorded, err := r.SaveCompletions(t.Context(), "c_gone", []valueobject.Completion{{Source: "session", ID: "1", Status: "exited"}})
	if recorded {
		t.Fatal("a missing room accepted a finish")
	}
	// The refusal is "there is no such room", not "the disk failed": the caller
	// reads it as a deleted room, not as something to retry.
	if apperror.As(err).StatusCode() != apperror.StatusNotFound {
		t.Fatalf("a missing room answered %v, want not found", err)
	}
}

// A corrupt book is said out loud — a finish nobody can read must never look
// like a room that finished nothing.
func TestPendingCompletionsReportsACorruptBook(t *testing.T) {
	r, root := completionRoom(t)
	path := filepath.Join(root, "c_finish", completionsFile)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PendingCompletions(t.Context(), "c_finish"); err == nil {
		t.Fatal("a corrupt book read as empty")
	}
}
