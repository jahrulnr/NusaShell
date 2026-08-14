package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"nusashell/domain"
)

// TodoStore is a durable, per-conversation todo checklist store backed by a
// single JSON file (conversation-todos.json). It is safe for concurrent use.
type TodoStore struct {
	mu    sync.RWMutex
	path  string
	store map[string][]domain.TodoItem
}

// NewTodoStore opens or creates the todo store at path. A missing or corrupt
// file is treated as an empty store so the shell can still boot.
func NewTodoStore(path string) *TodoStore {
	t := &TodoStore{
		path:  path,
		store: make(map[string][]domain.TodoItem),
	}
	t.load()
	return t
}

// Get returns the todo items for the given conversation, or nil when none.
func (t *TodoStore) Get(conversationID string) []domain.TodoItem {
	t.mu.RLock()
	defer t.mu.RUnlock()
	items, ok := t.store[conversationID]
	if !ok {
		return nil
	}
	out := make([]domain.TodoItem, len(items))
	copy(out, items)
	return out
}

// Set replaces the todo items for the given conversation and persists to disk.
func (t *TodoStore) Set(conversationID string, items []domain.TodoItem) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]domain.TodoItem, len(items))
	copy(cp, items)
	t.store[conversationID] = cp
	t.persistLocked()
}

// Clear removes the todo items for the given conversation and persists.
func (t *TodoStore) Clear(conversationID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.store[conversationID]; !ok {
		return
	}
	delete(t.store, conversationID)
	t.persistLocked()
}

func (t *TodoStore) load() {
	b, err := os.ReadFile(t.path)
	if err != nil {
		return // missing file is fine
	}
	var raw map[string][]domain.TodoItem
	if err := json.Unmarshal(b, &raw); err != nil {
		return // corrupt file is fine — start empty
	}
	t.mu.Lock()
	t.store = raw
	t.mu.Unlock()
}

func (t *TodoStore) persistLocked() {
	b, err := json.MarshalIndent(t.store, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(t.path), 0o755)
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, t.path)
}
