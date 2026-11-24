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
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
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
	if _, err := os.Stat(filepath.Join(repo.path, room.ID(), "0.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed segment survived the clear: %v", err)
	}
	// The room still loads and is listable: it is empty, not gone.
	if reloaded, err := repo.Load(t.Context(), room.ID()); err != nil || len(reloaded.Messages()) != 0 {
		t.Fatalf("cleared room = %+v, %v", reloaded, err)
	}
	if list, err := repo.List(t.Context()); err != nil || len(list) != 1 {
		t.Fatalf("list after clear = %+v, %v", list, err)
	}
	// The swap leaves nothing staged behind: the cleared room is the only
	// directory that carries the id.
	if leftovers := stagingEntries(t, repo, room.ID()); len(leftovers) != 0 {
		t.Fatalf("staging leftovers after clear: %v", leftovers)
	}
}

// stagingEntries names what the staging area holds for one room id.
func stagingEntries(t *testing.T, repo *ConversationRepository, id string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo.path, stagingRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		if owner, ok := stagedRoomID(entry.Name()); ok && owner == id {
			out = append(out, entry.Name())
		}
	}
	return out
}

// failRename refuses the renames match selects, for the span of one test.
func failRename(t *testing.T, match func(from, to string) bool) {
	t.Helper()
	orig := renamePath
	renamePath = func(from, to string) error {
		if match(from, to) {
			return errors.New("injected rename failure")
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renamePath = orig })
}

// assertRoomUnchanged checks that a room still carries the history and
// counters a failed clear was supposed to leave alone.
func assertRoomUnchanged(t *testing.T, repo *ConversationRepository, id string, before drepo.ConversationInfo, beforeMsgs int) {
	t.Helper()
	after, err := repo.Info(t.Context(), id)
	if err != nil {
		t.Fatalf("room unreadable after a failed clear: %v", err)
	}
	if after.MessageCount != before.MessageCount || after.ActiveCount != before.ActiveCount ||
		after.SealedSegments != before.SealedSegments {
		t.Fatalf("counters changed by a failed clear: before %+v after %+v", before, after)
	}
	msgs, err := repo.LoadAt(t.Context(), id, 0, 0)
	if err != nil || len(msgs) != beforeMsgs {
		t.Fatalf("history after a failed clear = %d messages, %v (want %d)", len(msgs), err, beforeMsgs)
	}
	if list, err := repo.List(t.Context()); err != nil || len(list) != 1 {
		t.Fatalf("list after a failed clear = %+v, %v", list, err)
	}
}

// A clear that cannot write the cleared record fails before the swap: the room
// keeps its history, its segments, its tool output, and its counters.
func TestClearWriteFailureLeavesRoomIntact(t *testing.T) {
	for _, target := range []string{metaFile, activeFile} {
		t.Run(target, func(t *testing.T) {
			repo := newRepo(t).(*ConversationRepository)
			room := roomWithHistory(t, repo, "c_clear_fail")
			before, err := repo.Info(t.Context(), room.ID())
			if err != nil {
				t.Fatal(err)
			}
			total := before.MessageCount

			unblock := blockAtomicWrites(t, target)
			room.ClearHistory()
			if err := repo.Clear(t.Context(), room); err == nil {
				t.Fatal("a clear whose staged write fails must report the error")
			}
			unblock()

			assertRoomUnchanged(t, repo, room.ID(), before, total)
			if _, err := os.Stat(filepath.Join(repo.path, room.ID(), toolOutputDir, "spill.log")); err != nil {
				t.Fatalf("tool output lost by a failed clear: %v", err)
			}
			if leftovers := stagingEntries(t, repo, room.ID()); len(leftovers) != 0 {
				t.Fatalf("staging leftovers after a failed clear: %v", leftovers)
			}
		})
	}
}

// A clear whose publish fails puts the room back: the caller gets the error
// and the room is exactly what it was, still under its own name.
func TestClearPublishFailurePutsRoomBack(t *testing.T) {
	repo := newRepo(t).(*ConversationRepository)
	room := roomWithHistory(t, repo, "c_clear_publish")
	before, err := repo.Info(t.Context(), room.ID())
	if err != nil {
		t.Fatal(err)
	}
	dir := repo.convDir(room.ID())
	failRename(t, func(from, to string) bool {
		return strings.Contains(from, stagedMark) && to == dir
	})

	room.ClearHistory()
	if err := repo.Clear(t.Context(), room); err == nil {
		t.Fatal("a clear whose publish fails must report the error")
	}
	assertRoomUnchanged(t, repo, room.ID(), before, before.MessageCount)
	if leftovers := stagingEntries(t, repo, room.ID()); len(leftovers) != 0 {
		t.Fatalf("staging leftovers after a failed publish: %v", leftovers)
	}
}

// A crash between the swap's two renames leaves the name empty and the room
// parked; the next read restores it, so a failed clear never leaves the room
// missing (and never a rejected archive). Both entry points repair: a read by
// id and a listing.
func TestClearCrashBetweenRenamesIsRecovered(t *testing.T) {
	for _, entry := range []string{"info", "list"} {
		t.Run(entry, func(t *testing.T) {
			repo := newRepo(t).(*ConversationRepository)
			room := roomWithHistory(t, repo, "c_clear_crash")
			before, err := repo.Info(t.Context(), room.ID())
			if err != nil {
				t.Fatal(err)
			}

			// Reproduce the crash state: the cleared room is built and the
			// live room is parked, but the publish never ran.
			stage, err := repo.stageRoom(room.ID())
			if err != nil {
				t.Fatal(err)
			}
			room.ClearHistory()
			m := metaFor(room)
			m.CreatedAt = before.CreatedAt
			m.MessageCount, m.ActiveCount, m.SealedSegments = 0, 0, 0
			if err := writeMetaFile(stage, m); err != nil {
				t.Fatal(err)
			}
			if err := writeFileAtomic(filepath.Join(stage, activeFile), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			parked, err := repo.parkedName(room.ID())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(repo.convDir(room.ID()), parked); err != nil {
				t.Fatal(err)
			}

			switch entry {
			case "info":
				assertRoomUnchanged(t, repo, room.ID(), before, before.MessageCount)
			case "list":
				list, err := repo.List(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if len(list) != 1 || list[0].ID != room.ID() || list[0].MessageCount != before.MessageCount {
					t.Fatalf("list after a crashed clear = %+v", list)
				}
			}
			if leftovers := stagingEntries(t, repo, room.ID()); len(leftovers) != 0 {
				t.Fatalf("staging leftovers after recovery: %v", leftovers)
			}
		})
	}
}

// A room's directory is never renamed while the store is publishing: the meta
// travels inside the staged copy, so the publish is one atomic step.
func TestPublishCarriesMetaBeforeRename(t *testing.T) {
	repo := newRepo(t).(*ConversationRepository)
	room := roomWithHistory(t, repo, "c_publish")
	published := 0
	orig := renamePath
	renamePath = func(from, to string) error {
		if strings.Contains(from, stagedMark) {
			published++
			if _, err := os.Stat(filepath.Join(from, metaFile)); err != nil {
				t.Errorf("publish of %s without its meta: %v", filepath.Base(from), err)
			}
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renamePath = orig })

	room.ClearHistory()
	if err := repo.Clear(t.Context(), room); err != nil {
		t.Fatal(err)
	}
	source := newConv(t, "c_publish_src")
	if err := repo.Save(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	appendMsgs(t, repo, source.ID(), "one", "two")
	loaded, err := repo.Load(t.Context(), source.ID())
	if err != nil {
		t.Fatal(err)
	}
	dst, err := loaded.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Fork(t.Context(), source.ID(), dst); err != nil {
		t.Fatal(err)
	}
	if published != 2 {
		t.Fatalf("publish renames = %d, want one per command", published)
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

// A Save is the one write that creates a directory, so it must restore a
// parked room before it would take the name: otherwise an interrupted clear's
// old contents would be mistaken for debris and dropped.
func TestSaveRestoresParkedRoomBeforeWriting(t *testing.T) {
	repo := newRepo(t).(*ConversationRepository)
	room := roomWithHistory(t, repo, "c_save_parked")
	before, err := repo.Info(t.Context(), room.ID())
	if err != nil {
		t.Fatal(err)
	}
	stage, err := repo.stageRoom(room.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(stage, activeFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	parked, err := repo.parkedName(room.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(repo.convDir(room.ID()), parked); err != nil {
		t.Fatal(err)
	}

	// A save that carries only the id would create an empty directory first.
	if err := repo.Save(t.Context(), newConv(t, room.ID())); err != nil {
		t.Fatal(err)
	}
	after, err := repo.Info(t.Context(), room.ID())
	if err != nil {
		t.Fatalf("room unreadable after a save over a parked copy: %v", err)
	}
	if after.MessageCount != before.MessageCount || after.SealedSegments != before.SealedSegments {
		t.Fatalf("save dropped the parked history: before %+v after %+v", before, after)
	}
	if msgs, err := repo.LoadAt(t.Context(), room.ID(), 0, 0); err != nil || len(msgs) != before.MessageCount {
		t.Fatalf("history after a save over a parked copy = %d messages, %v", len(msgs), err)
	}
	if leftovers := stagingEntries(t, repo, room.ID()); len(leftovers) != 0 {
		t.Fatalf("staging leftovers after recovery: %v", leftovers)
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

// A fork whose record cannot be written fails inside the staging area: no
// room appears under the destination id, nothing is left orphaned, and the
// source is untouched.
func TestForkMetaFailureLeavesNothing(t *testing.T) {
	repo := newRepo(t).(*ConversationRepository)
	src := roomWithHistory(t, repo, "c_src_meta")
	before, err := repo.Info(t.Context(), src.ID())
	if err != nil {
		t.Fatal(err)
	}
	room, err := repo.Load(t.Context(), src.ID())
	if err != nil {
		t.Fatal(err)
	}
	dst, err := room.Fork()
	if err != nil {
		t.Fatal(err)
	}

	unblock := blockAtomicWrites(t, metaFile)
	err = repo.Fork(t.Context(), src.ID(), dst)
	unblock()
	if err == nil {
		t.Fatal("a fork whose meta write fails must report the error")
	}
	if _, err := os.Stat(repo.convDir(dst.ID())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed fork published a directory: %v", err)
	}
	if leftovers := stagingEntries(t, repo, dst.ID()); len(leftovers) != 0 {
		t.Fatalf("staging leftovers after a failed fork: %v", leftovers)
	}
	after, err := repo.Info(t.Context(), src.ID())
	if err != nil {
		t.Fatalf("source unreadable after a failed fork: %v", err)
	}
	if after.MessageCount != before.MessageCount || after.Title != before.Title {
		t.Fatalf("source changed by a failed fork: before %+v after %+v", before, after)
	}
	msgs, err := repo.LoadAt(t.Context(), src.ID(), 0, 0)
	if err != nil || len(msgs) != before.MessageCount {
		t.Fatalf("source history after a failed fork = %d messages, %v", len(msgs), err)
	}
}
