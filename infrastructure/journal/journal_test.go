package journal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
)

func TestWrapMutation_declaredWritePatchDelete(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_decl"
	file := filepath.Join(workspace, "hello.txt")

	req := func(tool, tc string, paths ...string) application.MutationRequest {
		return application.MutationRequest{
			ConversationID: conv,
			RunID:          "run1",
			ToolCallID:     tc,
			ToolName:       tool,
			Class:          domain.MutationDeclared,
			WorkspaceRoot:  workspace,
			Paths:          paths,
		}
	}

	// Create file
	if err := j.WrapMutation(context.Background(), req("file_write", "tc1", file), func() error {
		return os.WriteFile(file, []byte("v1\n"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}

	// Modify same file — baseline should remain v1
	if err := j.WrapMutation(context.Background(), req("file_write", "tc2", file), func() error {
		return os.WriteFile(file, []byte("v2\n"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}

	// Delete
	if err := j.WrapMutation(context.Background(), req("file_delete", "tc3", file), func() error {
		return os.Remove(file)
	}); err != nil {
		t.Fatal(err)
	}

	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) < 3 {
		t.Fatalf("expected at least 3 changes, got %d", len(st.Changes))
	}
	last := st.Changes[len(st.Changes)-1]
	if last.Kind != domain.ChangeDeleted {
		t.Fatalf("last kind: got %q", last.Kind)
	}
}

func TestWrapMutation_declaredMove(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_move"
	src := filepath.Join(workspace, "src.txt")
	dst := filepath.Join(workspace, "dst.txt")
	_ = os.WriteFile(src, []byte("moveme"), 0o644)

	req := application.MutationRequest{
		ConversationID: conv,
		RunID:          "run1",
		ToolCallID:     "tc_move",
		ToolName:       "file_move",
		Class:          domain.MutationDeclared,
		WorkspaceRoot:  workspace,
		Paths:          []string{src, dst},
	}
	if err := j.WrapMutation(context.Background(), req, func() error {
		return os.Rename(src, dst)
	}); err != nil {
		t.Fatal(err)
	}
	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	var sawDeleted, sawAdded bool
	for _, c := range st.Changes {
		if c.Path == src && c.Kind == domain.ChangeDeleted {
			sawDeleted = true
		}
		if c.Path == dst && c.Kind == domain.ChangeAdded {
			sawAdded = true
		}
	}
	if !sawDeleted || !sawAdded {
		t.Fatalf("changes: %+v", st.Changes)
	}
}

func TestWrapMutation_opaqueExec(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_opaque"
	file := filepath.Join(workspace, "created.txt")

	req := application.MutationRequest{
		ConversationID: conv,
		RunID:          "run1",
		ToolCallID:     "tc_exec",
		ToolName:       "exec",
		Class:          domain.MutationOpaque,
		WorkspaceRoot:  workspace,
		Command:        "touch",
	}
	if err := j.WrapMutation(context.Background(), req, func() error {
		return os.WriteFile(file, []byte("from exec"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range st.Changes {
		if c.Path == file && c.Kind == domain.ChangeAdded {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected added change for %s: %+v", file, st.Changes)
	}
}

func TestWrapMutation_unobservedRecorded(t *testing.T) {
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_unobs"
	req := application.MutationRequest{
		ConversationID: conv,
		RunID:          "run1",
		ToolCallID:     "tc_mcp",
		ToolName:       "mcp_call",
		Class:          domain.MutationUnobserved,
	}
	if err := j.WrapMutation(context.Background(), req, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	st := newStore(dataDir)
	events, err := st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != eventTypeUnobserved {
		t.Fatalf("events: %+v", events)
	}
}

func TestWrapMutation_execErrorUnchanged(t *testing.T) {
	workspace := t.TempDir()
	// dataDir not writable — journaling fails but exec error must pass through
	dataDir := filepath.Join(t.TempDir(), "missing", "nested")
	j := New(dataDir)
	want := errors.New("tool failed")
	got := j.WrapMutation(context.Background(), application.MutationRequest{
		ConversationID: "conv_err",
		RunID:          "run1",
		ToolCallID:     "tc_err",
		ToolName:       "file_write",
		Class:          domain.MutationDeclared,
		WorkspaceRoot:  workspace,
		Paths:          []string{filepath.Join(workspace, "x.txt")},
	}, func() error {
		return want
	})
	if !errors.Is(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSessionState_missingSidecarEmpty(t *testing.T) {
	j := New(t.TempDir())
	st, err := j.SessionState(context.Background(), "conv_missing", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) != 0 {
		t.Fatalf("expected empty changes, got %d", len(st.Changes))
	}
}

func TestWrapMutation_declaredNoOpSkipped(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_noop"
	file := filepath.Join(workspace, "same.txt")
	if err := os.WriteFile(file, []byte("identical\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write identical content: filesystem effect is empty (spec §14).
	if err := j.WrapMutation(context.Background(), application.MutationRequest{
		ConversationID: conv, RunID: "r", ToolCallID: "tc_noop", ToolName: "file_write",
		Class: domain.MutationDeclared, WorkspaceRoot: workspace, Paths: []string{file},
	}, func() error {
		return os.WriteFile(file, []byte("identical\n"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}

	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) != 0 {
		t.Fatalf("no-op write should record no change, got %+v", st.Changes)
	}
}

func TestWrapMutation_opaqueIgnoresMetadataDirs(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_ignore"

	for _, dir := range []string{".git", "node_modules", ".nusashell", ".cache"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	req := application.MutationRequest{
		ConversationID: conv, RunID: "r", ToolCallID: "tc_exec", ToolName: "exec",
		Class: domain.MutationOpaque, WorkspaceRoot: workspace, Command: "test",
	}
	if err := j.WrapMutation(context.Background(), req, func() error {
		// Mutations inside ignored dirs must not be recorded.
		_ = os.WriteFile(filepath.Join(workspace, ".git", "HEAD"), []byte("ref"), 0o644)
		_ = os.WriteFile(filepath.Join(workspace, "node_modules", "pkg.js"), []byte("js"), 0o644)
		// Real workspace mutation must still be recorded.
		return os.WriteFile(filepath.Join(workspace, "real.txt"), []byte("real"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}

	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, c := range st.Changes {
		paths = append(paths, c.Path)
		if strings.Contains(c.Path, ".git") || strings.Contains(c.Path, "node_modules") {
			t.Fatalf("ignored dir leaked into changes: %s", c.Path)
		}
	}
	found := false
	for _, p := range paths {
		if p == filepath.Join(workspace, "real.txt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("real.txt missing from changes: %v", paths)
	}
}

func TestWrapMutation_opaqueMetadataOnlyChangeSkipped(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_meta"
	file := filepath.Join(workspace, "touched.txt")
	if err := os.WriteFile(file, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := application.MutationRequest{
		ConversationID: conv, RunID: "r", ToolCallID: "tc_touch", ToolName: "exec",
		Class: domain.MutationOpaque, WorkspaceRoot: workspace, Command: "touch",
	}
	if err := j.WrapMutation(context.Background(), req, func() error {
		// Bump mtime without changing content.
		now := time.Now().Add(2 * time.Second)
		return os.Chtimes(file, now, now)
	}); err != nil {
		t.Fatal(err)
	}

	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) != 0 {
		t.Fatalf("metadata-only change should record no change, got %+v", st.Changes)
	}
}

func TestWrapMutation_opaqueHashCacheAvoidsRehash(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_cache"
	file := filepath.Join(workspace, "cached.txt")
	if err := os.WriteFile(file, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := func(tc string) application.MutationRequest {
		return application.MutationRequest{
			ConversationID: conv, RunID: "r", ToolCallID: tc, ToolName: "exec",
			Class: domain.MutationOpaque, WorkspaceRoot: workspace, Command: "noop",
		}
	}

	// First exec: populates the hash cache.
	if err := j.WrapMutation(context.Background(), req("tc1"), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	j.mu.Lock()
	entry, ok := j.hashCache[file]
	j.mu.Unlock()
	if !ok || entry.hash == "" {
		t.Fatal("hash cache should be populated after first opaque wrap")
	}

	// Second exec without touching the file: cache hit, same hash.
	if err := j.WrapMutation(context.Background(), req("tc2"), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	j.mu.Lock()
	entry2 := j.hashCache[file]
	j.mu.Unlock()
	if entry2.hash != entry.hash {
		t.Fatalf("cache hash changed: %q -> %q", entry.hash, entry2.hash)
	}

	// Modify the file: cache must refresh.
	if err := os.WriteFile(file, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := j.WrapMutation(context.Background(), req("tc3"), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	j.mu.Lock()
	entry3 := j.hashCache[file]
	j.mu.Unlock()
	if entry3.hash == entry.hash {
		t.Fatal("cache hash should refresh after content change")
	}
}

func TestArchive_multiMemberPreservesHistory(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_multi_archive"
	fileA := filepath.Join(workspace, "a.txt")
	fileB := filepath.Join(workspace, "b.txt")

	wrap := func(tc, file, content string) {
		t.Helper()
		if err := j.WrapMutation(context.Background(), application.MutationRequest{
			ConversationID: conv, RunID: "r", ToolCallID: tc, ToolName: "file_write",
			Class: domain.MutationDeclared, WorkspaceRoot: workspace, Paths: []string{file},
		}, func() error {
			return os.WriteFile(file, []byte(content), 0o644)
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Turn 1: write a.txt, archive.
	wrap("tc1", fileA, "alpha\n")
	if err := j.Archive(conv); err != nil {
		t.Fatal(err)
	}

	// Turn 2: write b.txt, archive again (second gzip member).
	wrap("tc2", fileB, "beta\n")
	if err := j.Archive(conv); err != nil {
		t.Fatal(err)
	}

	// Turn 3: modify a.txt (live tail, not yet archived).
	wrap("tc3", fileA, "alpha\nmore\n")

	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	// Both files must appear: a.txt from gz member 1 + tail, b.txt from gz member 2.
	var sawA, sawB bool
	for _, c := range st.Changes {
		if c.Path == fileA {
			sawA = true
		}
		if c.Path == fileB {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Fatalf("multi-member archive lost history: sawA=%v sawB=%v changes=%+v", sawA, sawB, st.Changes)
	}
}

func TestRemove_deletesSidecar(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_remove"
	file := filepath.Join(workspace, "x.txt")

	if err := j.WrapMutation(context.Background(), application.MutationRequest{
		ConversationID: conv, RunID: "r", ToolCallID: "tc1", ToolName: "file_write",
		Class: domain.MutationDeclared, WorkspaceRoot: workspace, Paths: []string{file},
	}, func() error {
		return os.WriteFile(file, []byte("x\n"), 0o644)
	}); err != nil {
		t.Fatal(err)
	}

	sidecar := filepath.Join(dataDir, "conversations", conv+".journal")
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar should exist: %v", err)
	}

	if err := j.Remove(conv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatal("sidecar should be deleted after Remove")
	}

	// SessionState after Remove returns empty, not an error.
	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) != 0 {
		t.Fatalf("changes after Remove = %d, want 0", len(st.Changes))
	}
}

func TestArchive_sessionStateReadsGzip(t *testing.T) {
	workspace := t.TempDir()
	dataDir := t.TempDir()
	j := New(dataDir)
	conv := "conv_archive"
	file := filepath.Join(workspace, "a.txt")

	_ = j.WrapMutation(context.Background(), application.MutationRequest{
		ConversationID: conv, RunID: "r", ToolCallID: "tc1", ToolName: "file_write",
		Class: domain.MutationDeclared, WorkspaceRoot: workspace, Paths: []string{file},
	}, func() error {
		return os.WriteFile(file, []byte("archived\n"), 0o644)
	})

	_ = j.WrapMutation(context.Background(), application.MutationRequest{
		ConversationID: conv, RunID: "r", ToolCallID: "tc2", ToolName: "file_write",
		Class: domain.MutationDeclared, WorkspaceRoot: workspace, Paths: []string{file},
	}, func() error {
		return os.WriteFile(file, []byte("archived\nmore\n"), 0o644)
	})

	if err := j.Archive(conv); err != nil {
		t.Fatal(err)
	}

	st, err := j.SessionState(context.Background(), conv, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) == 0 {
		t.Fatal("expected changes after archive")
	}
}
