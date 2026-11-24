package toolfile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

// runCall drives one tool with a JSON input, the way the turn does.
func runCall(t *testing.T, ctx context.Context, tool service.Tool, input map[string]any) (service.ToolResult, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	call, err := entity.NewToolCall("call_1", tool.Schema().Name(), string(raw), enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(ctx, call, nil)
}

// A ~/… path reaches the owner's home through every file tool instead of
// creating a literal "~" folder in the workspace — the regression this
// test locks down.
func TestFileToolsExpandHomePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	ctx := service.WithWorkspaceDir(context.Background(), workspace)
	store := repoworkspace.NewLocal()
	write, read := mustWriteTool(t, workspace), mustReadTool(t, workspace)
	patch, err := NewFilePatchTool(store, workspace)
	if err != nil {
		t.Fatal(err)
	}
	search, err := NewFileSearchTool(store, workspace)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runCall(t, ctx, write, map[string]any{"path": "~/notes/a.md", "content": "one two"}); err != nil {
		t.Fatalf("write ~/notes/a.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "notes", "a.md")); err != nil {
		t.Fatalf("the file must land in the home folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "~")); err == nil {
		t.Fatal(`write_file created a "./~" folder in the workspace`)
	}
	if got, err := runCall(t, ctx, read, map[string]any{"path": "~/notes/a.md"}); err != nil || got.Text != "one two" {
		t.Fatalf("read ~/notes/a.md = %q, %v", got.Text, err)
	}
	if _, err := runCall(t, ctx, patch, map[string]any{"path": "~/notes/a.md", "old_string": "two", "new_string": "three"}); err != nil {
		t.Fatalf("patch ~/notes/a.md: %v", err)
	}
	if got, err := runCall(t, ctx, read, map[string]any{"path": "~/notes/a.md"}); err != nil || got.Text != "one three" {
		t.Fatalf("read after patch = %q, %v", got.Text, err)
	}
	got, err := runCall(t, ctx, search, map[string]any{"pattern": "three", "glob": "*.md", "path": "~/notes"})
	if err != nil {
		t.Fatalf("search ~/notes: %v", err)
	}
	if !strings.Contains(got.Text, "a.md:1: one three") {
		t.Fatalf("search ~/notes = %q", got.Text)
	}

	// A ~ anywhere but the front is part of a name, not a home marker.
	if _, err := runCall(t, ctx, write, map[string]any{"path": "mid/~/b.txt", "content": "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "mid", "~", "b.txt")); err != nil {
		t.Fatalf("a mid-path ~ must stay a literal folder: %v", err)
	}

	// A file: URL is a path, not a folder called "file:".
	target := filepath.Join(workspace, "url.txt")
	if _, err := runCall(t, ctx, write, map[string]any{"path": "file://" + target, "content": "u"}); err != nil {
		t.Fatalf("write file://%s: %v", target, err)
	}
	if got, err := runCall(t, ctx, read, map[string]any{"path": target}); err != nil || got.Text != "u" {
		t.Fatalf("read the file:// target = %q, %v", got.Text, err)
	}

	// %VAR% is a Windows rule only: on this host it is a legal folder name.
	if runtime.GOOS != "windows" {
		if _, err := runCall(t, ctx, write, map[string]any{"path": "%APPDATA%/c.txt", "content": "x"}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(workspace, "%APPDATA%", "c.txt")); err != nil {
			t.Fatalf("a literal %%APPDATA%% folder is the Linux behavior: %v", err)
		}
	}
}

// read_media shares the expansion: a ~/ path opens the file in the home
// folder and the reference it mints names that file.
func TestReadMediaExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	writeFixture(t, home, "pic.png", pngFixture)
	tool := mustMediaTool(t, workspace)

	res, err := runReadMedia(t, tool, `{"path":"~/pic.png"}`)
	if err != nil {
		t.Fatalf("read_media ~/pic.png: %v", err)
	}
	if len(res.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(res.Attachments))
	}
	if want := "file://" + filepath.Join(home, "pic.png"); res.Attachments[0].URI() != want {
		t.Fatalf("uri = %q; want %q", res.Attachments[0].URI(), want)
	}
	if _, err := os.Stat(filepath.Join(workspace, "~")); err == nil {
		t.Fatal(`read_media treated ~/… as a workspace path`)
	}
}
