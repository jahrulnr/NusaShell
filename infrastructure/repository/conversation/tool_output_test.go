package repoconversation

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestToolOutputBelongsToConversation(t *testing.T) {
	root := t.TempDir()
	repo, err := NewToolOutputRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Reserve(context.Background(), "missing", "call-1"); err == nil {
		t.Fatal("missing conversation accepted")
	}
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Reserve(context.Background(), "../c1", "call-1"); err == nil {
		t.Fatal("unsafe conversation id accepted")
	}
	spill, err := repo.Reserve(context.Background(), "c1", "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spill.Reference()); !os.IsNotExist(err) {
		t.Fatalf("reserve created a file before output spilled: %v", err)
	}
	w, err := spill.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("raw output")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := spill.Open(); !os.IsExist(err) {
		t.Fatalf("spill reopened and could overwrite existing output: %v", err)
	}
	if got, err := os.ReadFile(spill.Reference()); err != nil || string(got) != "raw output" {
		t.Fatalf("spill = %q, %v", got, err)
	}
	if err := (&ConversationRepository{path: root}).Delete(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spill.Reference()); !os.IsNotExist(err) {
		t.Fatalf("spill survived conversation deletion: %v", err)
	}
}

func TestToolOutputRootIsAbsoluteAndRequired(t *testing.T) {
	if _, err := NewToolOutputRepository(""); err == nil {
		t.Fatal("empty output root accepted")
	}
	repo, err := NewToolOutputRepository("relative-output")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(repo.(*ToolOutputRepository).root) {
		t.Fatalf("root = %q, want absolute", repo.(*ToolOutputRepository).root)
	}
}

// A conversation id that resolves to a directory outside the store — ".."
// reaches the store's parent, which exists — must be refused by validation
// before any filesystem call, not by a failed stat.
func TestToolOutputReserveRejectsEscapingConversationIDs(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "store")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "sibling"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := NewToolOutputRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"..", "../sibling", ".", "a/b", `a\b`, ""} {
		if _, err := repo.Reserve(context.Background(), id, "call-1"); err == nil {
			t.Fatalf("conversation id %q accepted", id)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "tool-output")); !os.IsNotExist(err) {
		t.Fatalf("reserve wrote outside the store via ..: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "sibling", "tool-output")); !os.IsNotExist(err) {
		t.Fatalf("reserve wrote into a sibling directory: %v", err)
	}
}

// Tool output is conversation-owned. A symlink planted at the spill
// directory must not redirect a write outside the conversation; reserve
// must reject the non-real directory instead of treating EEXIST as fine.
func TestToolOutputReserveRejectsSymlinkedSpillDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	base := t.TempDir()
	root := filepath.Join(base, "store")
	room := filepath.Join(root, "c1")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(room, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(room, "tool-output")); err != nil {
		t.Fatal(err)
	}
	repo, err := NewToolOutputRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	spill, err := repo.Reserve(context.Background(), "c1", "call-1")
	if err != nil {
		return // rejecting the symlinked spill directory is the safe outcome
	}
	w, err := spill.Open()
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("escaped"))
	_ = w.Close()
	if _, err := os.Stat(filepath.Join(outside, filepath.Base(spill.Reference()))); err == nil {
		t.Fatal("tool output was written through a symlinked spill directory")
	}
}

// The symlink guard covers the tool-output child — but Stat on the
// conversation directory itself follows links, so a planted
// <root>/<id> symlink reaches the same escape one component earlier:
// Mkdir lands tool-output at the link's target and the exclusive write
// happens outside the store entirely.
func TestToolOutputReserveRejectsSymlinkedConversationDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	base := t.TempDir()
	root := filepath.Join(base, "store")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "c1")); err != nil {
		t.Fatal(err)
	}
	repo, err := NewToolOutputRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	spill, err := repo.Reserve(context.Background(), "c1", "call-1")
	if err == nil {
		resolved, evalErr := filepath.EvalSymlinks(spill.Reference())
		if evalErr == nil && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			t.Fatalf("spill path escapes the store root through a symlinked conversation dir: %q", resolved)
		}
		t.Fatalf("symlinked conversation dir accepted: %q", spill.Reference())
	}
	if _, err := os.Stat(filepath.Join(outside, "tool-output")); err == nil {
		t.Fatal("reserve created tool-output outside the store root")
	}
}

// Kept media is a file of the thread named for its type, pinned to the
// bytes written, and gone with the thread.
func TestToolOutputKeepPinsMedia(t *testing.T) {
	root := t.TempDir()
	repo, err := NewToolOutputRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Keep(context.Background(), "missing", "call-1", "image/png", []byte("x")); err == nil {
		t.Fatal("missing conversation accepted")
	}
	if err := os.Mkdir(filepath.Join(root, "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Keep(context.Background(), "c1", "call-1", "not a type", []byte("x")); err == nil {
		t.Fatal("invalid media type accepted")
	}
	data := []byte("\x89PNG fake")
	att, err := repo.Keep(context.Background(), "c1", "call/1", "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimPrefix(att.URI(), "file://")
	if !strings.HasPrefix(path, filepath.Join(root, "c1", "tool-output")+string(filepath.Separator)) {
		t.Fatalf("kept at %q, outside the thread's tool output", path)
	}
	if filepath.Ext(path) != ".png" || att.Name() != filepath.Base(path) || strings.Contains(att.Name(), "/") {
		t.Fatalf("name = %q, path = %q", att.Name(), path)
	}
	if att.MediaType() != "image/png" || len(att.SHA256()) != 64 {
		t.Fatalf("attachment = %s %q", att.MediaType(), att.SHA256())
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("kept = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
		}
	}
	other, err := repo.Keep(context.Background(), "c1", "call/1", "audio/mpeg", data)
	if err != nil || other.URI() == att.URI() || filepath.Ext(other.URI()) != ".mp3" {
		t.Fatalf("second keep = %v, %v", other.URI(), err)
	}
}
