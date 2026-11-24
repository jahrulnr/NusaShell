package repoconversation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// roomWithHistory stores a room with a custom identity, three messages of
// which two are sealed, a usage total, and one tool-output file.
func roomWithHistory(t *testing.T, repo *ConversationRepository, id string) *aggregate.Conversation {
	t.Helper()
	room := newConv(t, id)
	room.SetTitle("release notes")
	if err := room.SetLabel("release-notes"); err != nil {
		t.Fatal(err)
	}
	room.SetPinned(true)
	room.SetMemoryOff(true)
	room.SetRouting(&valueobject.Routing{ReplyToOrigin: true, MirrorFinal: true})
	usage, err := valueobject.NewUsage(120, 0, 30, 45)
	if err != nil {
		t.Fatal(err)
	}
	room.AddUsage(usage)
	if err := repo.Save(t.Context(), room); err != nil {
		t.Fatal(err)
	}
	appendMsgs(t, repo, id, "first", "second", "third")

	live, err := repo.Load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := entity.NewMessage(enum.RoleUser, "checkpoint")
	if err != nil {
		t.Fatal(err)
	}
	summary.SetUserType(enum.LLMUserTypeSystem)
	if err := repo.Compact(t.Context(), id, summary, 2, live.Messages()[2:]); err != nil {
		t.Fatal(err)
	}
	spill := filepath.Join(repo.path, id, toolOutputDir, "spill.log")
	if err := os.MkdirAll(filepath.Dir(spill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spill, []byte("tool output"), 0o600); err != nil {
		t.Fatal(err)
	}
	room, err = repo.Load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return room
}

// Clear empties the room's history, folds, and tool output while its identity
// — title, label, bindings, pin, routing, memory setting, and usage — stays.
func TestClearKeepsIdentity(t *testing.T) {
	repo := newRepo(t).(*ConversationRepository)
	room := roomWithHistory(t, repo, "c_clear")
	if err := room.BindAgent("a_external"); err != nil {
		t.Fatal(err)
	}
	if err := room.SetAgentOption("mode", "plan"); err != nil {
		t.Fatal(err)
	}
	room.SetAgentSession("session-1")
	if err := repo.Save(t.Context(), room); err != nil {
		t.Fatal(err)
	}
	before, err := repo.Info(t.Context(), room.ID())
	if err != nil {
		t.Fatal(err)
	}

	room.ClearHistory()
	if err := repo.Clear(t.Context(), room); err != nil {
		t.Fatal(err)
	}

	after, err := repo.Info(t.Context(), room.ID())
	if err != nil {
		t.Fatal(err)
	}
	if after.MessageCount != 0 || after.ActiveCount != 0 || after.SealedSegments != 0 {
		t.Fatalf("counters after clear = %+v", after)
	}
	if after.Title != before.Title || after.Label != before.Label || after.ProviderID != before.ProviderID ||
		after.ModelID != before.ModelID || !after.Pinned || !after.MemoryOff || after.Usage != before.Usage {
		t.Fatalf("identity changed: before %+v after %+v", before, after)
	}
	if after.AgentID != before.AgentID || after.AgentOptions["mode"] != "plan" {
		t.Fatalf("agent binding changed: %+v", after)
	}
	if after.AgentSession != "" {
		t.Fatalf("agent session survived the clear: %q", after.AgentSession)
	}
	if msgs, err := repo.LoadAt(t.Context(), room.ID(), 0, 0); err != nil || len(msgs) != 0 {
		t.Fatalf("history after clear = %d messages, %v", len(msgs), err)
	}
	if _, err := os.Stat(filepath.Join(repo.path, room.ID(), toolOutputDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tool output survived the clear: %v", err)
	}
	// The room still loads and is listable: it is empty, not gone.
	if reloaded, err := repo.Load(t.Context(), room.ID()); err != nil || len(reloaded.Messages()) != 0 {
		t.Fatalf("cleared room = %+v, %v", reloaded, err)
	}
	if list, err := repo.List(t.Context()); err != nil || len(list) != 1 {
		t.Fatalf("list after clear = %+v, %v", list, err)
	}
}

// A fork copies the whole logical history and the files it references, keeps
// the room's bindings, and drops what belongs to one room: label, routing,
// pin, the agent session, the agent title, and accumulated usage. The source is
// never modified, and deleting the copy leaves it intact.
func TestForkCopiesHistoryAndDropsRoomBindings(t *testing.T) {
	repo := newRepo(t).(*ConversationRepository)
	src := roomWithHistory(t, repo, "c_src")

	// A message whose attachment lives in the room's own tool-output folder:
	// the copy must reference the copy's file, not the source's.
	att, err := valueobject.NewAttachment("file://"+filepath.Join(repo.path, src.ID(), toolOutputDir, "spill.log"), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	att.SetName("spill.log")
	withFile, err := entity.NewMessage(enum.RoleUser, "look at this")
	if err != nil {
		t.Fatal(err)
	}
	withFile.AddAttachment(att)
	if _, err := repo.AppendMessage(t.Context(), src.ID(), withFile); err != nil {
		t.Fatal(err)
	}
	if src, err = repo.Load(t.Context(), src.ID()); err != nil {
		t.Fatal(err)
	}
	before, err := repo.Info(t.Context(), src.ID())
	if err != nil {
		t.Fatal(err)
	}

	dst, err := src.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Fork(t.Context(), src.ID(), dst); err != nil {
		t.Fatal(err)
	}

	copied, err := repo.Info(t.Context(), dst.ID())
	if err != nil {
		t.Fatal(err)
	}
	if copied.Title != "release notes (fork)" {
		t.Fatalf("fork title = %q", copied.Title)
	}
	if copied.MessageCount != before.MessageCount || copied.ActiveCount != before.ActiveCount ||
		copied.SealedSegments != before.SealedSegments {
		t.Fatalf("fork counters = %+v, source = %+v", copied, before)
	}
	if copied.Label != "" || copied.Pinned || copied.Routing != nil || copied.AgentSession != "" {
		t.Fatalf("fork inherited a per-room binding: %+v", copied)
	}
	if copied.Usage.Total() != 0 {
		t.Fatalf("fork inherited usage: %+v", copied.Usage)
	}
	if copied.Workspace != before.Workspace || copied.Environment != before.Environment || copied.MemoryOff != before.MemoryOff {
		t.Fatalf("fork lost a shared binding: %+v", copied)
	}

	srcHistory, err := repo.LoadAt(t.Context(), src.ID(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	dstHistory, err := repo.LoadAt(t.Context(), dst.ID(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dstHistory) != len(srcHistory) {
		t.Fatalf("fork history = %d messages, source = %d", len(dstHistory), len(srcHistory))
	}
	for i := range srcHistory {
		if srcHistory[i].ID() != dstHistory[i].ID() || srcHistory[i].Content() != dstHistory[i].Content() {
			t.Fatalf("message %d differs: %+v vs %+v", i, srcHistory[i], dstHistory[i])
		}
	}
	last := dstHistory[len(dstHistory)-1]
	if atts := last.Attachments(); len(atts) != 1 || !strings.HasPrefix(atts[0].URI(), "file://"+filepath.Join(repo.path, dst.ID(), toolOutputDir)) {
		t.Fatalf("fork attachment = %+v", atts)
	}
	if data, err := os.ReadFile(filepath.Join(repo.path, dst.ID(), toolOutputDir, "spill.log")); err != nil || string(data) != "tool output" {
		t.Fatalf("fork tool output = %q, %v", data, err)
	}

	// The copy is its own room: deleting it cannot damage the source.
	if err := repo.Delete(t.Context(), dst.ID()); err != nil {
		t.Fatal(err)
	}
	after, err := repo.Info(t.Context(), src.ID())
	if err != nil {
		t.Fatal(err)
	}
	if after.MessageCount != before.MessageCount || after.Title != before.Title {
		t.Fatalf("source changed by the fork's delete: %+v", after)
	}
	if msgs, err := repo.LoadAt(t.Context(), src.ID(), 0, 0); err != nil || len(msgs) != len(srcHistory) {
		t.Fatalf("source history = %d messages, %v", len(msgs), err)
	}
}

// A fork of an empty room is the repository's 400, and a repeated fork id is
// refused rather than overwriting a room.
func TestForkRefusesExistingDestination(t *testing.T) {
	repo := newRepo(t).(*ConversationRepository)
	src := roomWithHistory(t, repo, "c_src2")
	room, err := repo.Load(t.Context(), src.ID())
	if err != nil {
		t.Fatal(err)
	}
	dst, err := room.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Fork(t.Context(), src.ID(), dst); err != nil {
		t.Fatal(err)
	}
	again, err := room.Fork()
	if err != nil {
		t.Fatal(err)
	}
	err = repo.Fork(t.Context(), src.ID(), dst)
	if err == nil {
		t.Fatal("second fork into the same id must be refused")
	}
	if err := repo.Fork(t.Context(), src.ID(), again); err != nil {
		t.Fatalf("a fresh destination must still work: %v", err)
	}
}
