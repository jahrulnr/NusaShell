package toolfile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

// A project's other folders are reachable by absolute path through every
// file tool; a relative path stays in the workspace dir, and an absolute
// path outside all of the thread's folders is refused.
func TestFileToolsReachExtraFolders(t *testing.T) {
	primary, extra, outside := t.TempDir(), t.TempDir(), t.TempDir()
	ctx := service.WithExtraFolders(service.WithWorkspaceDir(context.Background(), primary), []string{extra})
	run := func(tool service.Tool, input map[string]any) (string, error) {
		t.Helper()
		raw, _ := json.Marshal(input)
		tc, err := entity.NewToolCall("call_1", tool.Schema().Name(), string(raw), enum.TooltypeOrigin)
		if err != nil {
			t.Fatal(err)
		}
		res, err := tool.Execute(ctx, tc, nil)
		return res.Text, err
	}
	write, read := mustWriteTool(t, primary), mustReadTool(t, primary)
	patch, err := NewFilePatchTool(repoworkspace.NewLocal(), primary)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(extra, "sub", "notes.txt")
	if _, err := run(write, map[string]any{"path": target, "content": "one two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := run(patch, map[string]any{"path": target, "old_string": "two", "new_string": "three"}); err != nil {
		t.Fatal(err)
	}
	if got, err := run(read, map[string]any{"path": target}); err != nil || got != "one three" {
		t.Fatalf("read extra = %q, %v", got, err)
	}
	if _, err := run(write, map[string]any{"path": "here.txt", "content": "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(primary, "here.txt")); err != nil {
		t.Fatal("a relative path must land in the workspace dir")
	}
	if got, err := run(read, map[string]any{"path": filepath.Join(primary, "here.txt")}); err != nil || got != "x" {
		t.Fatalf("absolute path in the primary folder = %q, %v", got, err)
	}
	if _, err := run(write, map[string]any{"path": filepath.Join(outside, "no.txt"), "content": "x"}); err == nil {
		t.Fatal("a path outside the thread's folders must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "no.txt")); err == nil {
		t.Fatal("the refused write landed")
	}
}
