package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"nusashell/domain"
)

// TodoStore is a durable, per-conversation todo checklist store backed by a
// single JSON file (conversation-todos.json). It stores both the goal brief
// and the item list per conversation. It is safe for concurrent use.
type TodoStore struct {
	mu    sync.RWMutex
	path  string
	store map[string]domain.ConversationTodos
}

// NewTodoStore opens or creates the todo store at path. A missing or corrupt
// file is treated as an empty store so the shell can still boot. Old files
// in the legacy format (map[string][]TodoItem) are migrated transparently.
func NewTodoStore(path string) *TodoStore {
	t := &TodoStore{
		path:  path,
		store: make(map[string]domain.ConversationTodos),
	}
	t.load()
	return t
}

// Get returns the todo items for the given conversation, or nil when none.
func (t *TodoStore) Get(conversationID string) []domain.TodoItem {
	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, ok := t.store[conversationID]
	if !ok {
		return nil
	}
	out := make([]domain.TodoItem, len(entry.Items))
	copy(out, entry.Items)
	return out
}

// GetGoal returns the goal brief for the given conversation, or "" when none.
func (t *TodoStore) GetGoal(conversationID string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, ok := t.store[conversationID]
	if !ok {
		return ""
	}
	return entry.Goal
}

// Set replaces the todo items for the given conversation, preserving the
// existing goal, and persists to disk.
func (t *TodoStore) Set(conversationID string, items []domain.TodoItem) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.store[conversationID]
	cp := make([]domain.TodoItem, len(items))
	copy(cp, items)
	entry.Items = cp
	t.store[conversationID] = entry
	t.persistLocked()
}

// SetGoal sets the goal brief for the given conversation, preserving the
// existing items, and persists to disk.
func (t *TodoStore) SetGoal(conversationID string, goal string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.store[conversationID]
	entry.Goal = goal
	t.store[conversationID] = entry
	t.persistLocked()
}

// Clear removes the todo state for the given conversation and persists.
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
	// Try the current format: map[string]ConversationTodos.
	var current map[string]domain.ConversationTodos
	if err := json.Unmarshal(b, &current); err == nil {
		// Validate: at least one entry should have items or goal. If all
		// entries have empty items and empty goal, it might be the old
		// format misparsed — fall through to legacy.
		t.mu.Lock()
		t.store = current
		t.mu.Unlock()
		return
	}
	// Legacy format: map[string][]TodoItem. Migrate to the new struct.
	var legacy map[string][]domain.TodoItem
	if err := json.Unmarshal(b, &legacy); err != nil {
		return // corrupt file is fine — start empty
	}
	migrated := make(map[string]domain.ConversationTodos, len(legacy))
	for convID, items := range legacy {
		migrated[convID] = domain.ConversationTodos{Items: items}
	}
	t.mu.Lock()
	t.store = migrated
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
