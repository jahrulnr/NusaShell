package jsonstore

import (
	"os"
	"path/filepath"
	"testing"

	"nusashell/domain"
)

func TestTodoStorePersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todos.json")

	store := NewTodoStore(path)
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
	store2 := NewTodoStore(path)
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

	store := NewTodoStore(path)
	store.Set("conv_1", []domain.TodoItem{{ID: "1", Content: "Task", Status: domain.TodoPending}})
	store.Set("conv_2", []domain.TodoItem{{ID: "1", Content: "Other", Status: domain.TodoPending}})

	store.Clear("conv_1")

	store2 := NewTodoStore(path)
	if items := store2.Get("conv_1"); items != nil {
		t.Errorf("expected nil for cleared conv, got %+v", items)
	}
	if items := store2.Get("conv_2"); len(items) != 1 {
		t.Errorf("expected 1 item for conv_2, got %d", len(items))
	}
}

func TestTodoStoreSetReplacesNotAppends(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "todos.json"))

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

func TestTodoStoreMissingFile(t *testing.T) {
	dir := t.TempDir()
	store := NewTodoStore(filepath.Join(dir, "nonexistent.json"))
	if items := store.Get("conv_1"); items != nil {
		t.Errorf("expected nil for missing file, got %+v", items)
	}
}

func TestTodoStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todos.json")
	os.WriteFile(path, []byte("{not valid json"), 0o600)

	store := NewTodoStore(path)
	if items := store.Get("conv_1"); items != nil {
		t.Errorf("expected nil for corrupt file, got %+v", items)
	}
}
