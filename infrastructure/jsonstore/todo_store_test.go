package jsonstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestTodoStorePersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todos.json")

	store := NewTodoStore(path, dir, nil)
	store.Set("conv_1", []domain.TodoItem{
		{ID: "1", Content: "Task A", Status: domain.TodoCompleted},
		{ID: "2", Content: "Task B", Status: domain.TodoInProgress},
		{ID: "3", Content: "Task C", Status: domain.TodoPending},
	})

	// Verify file was written
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}

	// Reload from disk
	store2 := NewTodoStore(path, dir, nil)
	items := store2.Get("conv_1")
	if len(items) != 3 {
		t.Fatalf("expected 3 items after reload, got %d", len(items))
	}
	if items[0].Content != "Task A" || items[1].Status != domain.TodoInProgress {
		t.Errorf("item mismatch after reload: %+v", items)
	}
}

func TestTodoStoreClearPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todos.json")

	store := NewTodoStore(path, dir, nil)
	store.Set("conv_1", []domain.TodoItem{{ID: "1", Content: "Task", Status: domain.TodoPending}})
	store.Set("conv_2", []domain.TodoItem{{ID: "1", Content: "Other", Status: domain.TodoPending}})

	store.Clear("conv_1")

	store2 := NewTodoStore(path, dir, nil)
	if items := store2.Get("conv_1"); items != nil {
		t.Errorf("expected nil for cleared conv, got %+v", items)
	}
	if items := store2.Get("conv_2"); len(items) != 1 {
		t.Errorf("expected 1 item for conv_2, got %d", len(items))
	}
}

func TestTodoStoreSetReplacesNotAppends(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"), dir, nil)

	store.Set("conv_1", []domain.TodoItem{{ID: "1", Content: "Old", Status: domain.TodoPending}})
	store.Set("conv_1", []domain.TodoItem{{ID: "2", Content: "New", Status: domain.TodoCompleted}})

	items := store.Get("conv_1")
	if len(items) != 1 {
		t.Fatalf("expected 1 item (replace), got %d", len(items))
	}
	if items[0].ID != "2" {
		t.Errorf("expected replacement item ID=2, got %s", items[0].ID)
	}
}

func TestTodoStoreConcurrentSetLastWriteWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todos.json")
	store := NewTodoStore(path, dir, nil)

	done := make(chan struct{}, 2)
	go func() {
		store.Set("conv_1", []domain.TodoItem{{ID: "a", Content: "A", Status: domain.TodoPending}})
		done <- struct{}{}
	}()
	go func() {
		store.Set("conv_1", []domain.TodoItem{{ID: "b", Content: "B", Status: domain.TodoCompleted}})
		done <- struct{}{}
	}()
	<-done
	<-done

	reloaded := NewTodoStore(path, dir, nil)
	items := reloaded.Get("conv_1")
	if len(items) != 1 {
		t.Fatalf("expected 1 item after concurrent sets, got %d", len(items))
	}
	if items[0].ID != "a" && items[0].ID != "b" {
		t.Fatalf("unexpected item %+v", items[0])
	}
	if got := store.Get("conv_1"); len(got) != 1 || got[0].ID != items[0].ID {
		t.Fatalf("memory/disk mismatch: mem=%+v disk=%+v", got, items)
	}
}

func TestTodoStoreMissingFile(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "nonexistent.json"), dir, nil)
	if items := store.Get("conv_1"); items != nil {
		t.Errorf("expected nil for missing file, got %+v", items)
	}
}

func TestTodoStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todos.json")
	os.WriteFile(path, []byte("{not valid json"), 0o600)

	store := NewTodoStore(path, dir, nil)
	if items := store.Get("conv_1"); items != nil {
		t.Errorf("expected nil for corrupt file, got %+v", items)
	}
}

func TestTodoStoreSetBriefMirrorsPlanFileInWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"), dir, func(string) string { return ws })

	brief := "## Objective\nDo the thing\n\n## Done when\nTests pass"
	store.SetBrief("conv_1", brief)

	want := filepath.Join(ws, ".nusashell", "plans", "conv_1.plan.md")
	if got := store.PlanPath("conv_1"); got != want {
		t.Fatalf("PlanPath = %q, want %q", got, want)
	}
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("plan file not written: %v", err)
	}
	content := string(b)
	for _, part := range []string{"conversation_id: conv_1", "updated_at:", brief} {
		if !strings.Contains(content, part) {
			t.Errorf("plan file missing %q\ngot:\n%s", part, content)
		}
	}
}

func TestTodoStoreSetBriefFallsBackToDataDir(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"), dir, func(string) string { return "" })

	store.SetBrief("conv_1", "## Objective\nX\n## Done when\nY")

	want := filepath.Join(dir, "conversations", "conv_1", "plan.md")
	if got := store.PlanPath("conv_1"); got != want {
		t.Fatalf("PlanPath = %q, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("fallback plan file not written: %v", err)
	}
}

func TestTodoStoreSetBriefOverwritesMirror(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"), dir, nil)

	store.SetBrief("conv_1", "## Objective\nFirst\n## Done when\nA")
	store.SetBrief("conv_1", "## Objective\nSecond\n## Done when\nB")

	b, err := os.ReadFile(store.PlanPath("conv_1"))
	if err != nil {
		t.Fatalf("plan file missing: %v", err)
	}
	if strings.Contains(string(b), "First") || !strings.Contains(string(b), "Second") {
		t.Errorf("mirror not overwritten:\n%s", b)
	}
}

func TestTodoStoreClearBriefKeepsItemsAndDeletesFile(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"), dir, nil)

	store.Set("conv_1", []domain.TodoItem{{ID: "1", Content: "Task", Status: domain.TodoPending}})
	store.SetBrief("conv_1", "## Objective\nX\n## Done when\nY")
	planPath := store.PlanPath("conv_1")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("plan file should exist after SetBrief: %v", err)
	}

	if err := store.ClearBrief("conv_1"); err != nil {
		t.Fatalf("ClearBrief: %v", err)
	}
	if got := store.GetBrief("conv_1"); got != "" {
		t.Errorf("brief should be empty after ClearBrief, got %q", got)
	}
	if got := store.PlanPath("conv_1"); got != "" {
		t.Errorf("PlanPath should be empty after ClearBrief, got %q", got)
	}
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Errorf("plan file should be deleted after ClearBrief")
	}
	if items := store.Get("conv_1"); len(items) != 1 {
		t.Errorf("items must survive ClearBrief, got %d", len(items))
	}
	// Persisted state survives a reload.
	reloaded := NewTodoStore(filepath.Join(dir, "todos.json"), dir, nil)
	if got := reloaded.GetBrief("conv_1"); got != "" {
		t.Errorf("brief should stay empty after reload, got %q", got)
	}
	if items := reloaded.Get("conv_1"); len(items) != 1 {
		t.Errorf("items should survive reload, got %d", len(items))
	}
}

func TestTodoStoreClearBriefNoopWithoutBrief(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"), dir, nil)
	if err := store.ClearBrief("conv_missing"); err != nil {
		t.Fatalf("ClearBrief on unknown conversation: %v", err)
	}
}

func TestTodoStoreClearDeletesPlanFile(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"), dir, nil)
	store.SetBrief("conv_1", "## Objective\nX\n## Done when\nY")
	planPath := store.PlanPath("conv_1")

	store.Clear("conv_1")

	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Errorf("plan file should be deleted after Clear")
	}
}

func TestTodoStorePlanPathEmptyWithoutBrief(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"), dir, nil)
	store.Set("conv_1", []domain.TodoItem{{ID: "1", Content: "Task", Status: domain.TodoPending}})
	if got := store.PlanPath("conv_1"); got != "" {
		t.Errorf("PlanPath without brief = %q, want empty", got)
	}
}
