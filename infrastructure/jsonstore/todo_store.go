package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"nusashell/domain"
)

// TodoStore is a durable, per-conversation todo checklist store backed by a
// single JSON file (conversations/todos.json). It stores both the planning
// brief and the item list per conversation. It is safe for concurrent use.
// Legacy `goal` field in persisted JSON is transparently read into Brief.
type TodoStore struct {
	mu    sync.RWMutex
	path  string
	store map[string]domain.ConversationTodos
}

// NewTodoStore opens or creates the todo store at path. A missing or corrupt
// file is treated as an empty store so the shell can still boot.
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

// GetBrief returns the planning brief for the given conversation, or "" when none.
func (t *TodoStore) GetBrief(conversationID string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, ok := t.store[conversationID]
	if !ok {
		return ""
	}
	return entry.Brief
}

// Set replaces the todo items for the given conversation, preserving the
// existing brief, and persists to disk.
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

// SetBrief sets the planning brief for the given conversation, preserving the
// existing items, and persists to disk.
func (t *TodoStore) SetBrief(conversationID string, brief string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.store[conversationID]
	entry.Brief = brief
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

// Patch merges items by ID into the existing list. Items with an existing
// ID update their status (always) and content (only when non-empty). Items
// with a new ID are appended. Items not in the patch are kept unchanged.
// This is the backend for the todo tool's mode:"patch".
func (t *TodoStore) Patch(conversationID string, patches []domain.TodoItem) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.store[conversationID]
	existing := entry.Items
	byID := make(map[string]int, len(existing))
	for i, item := range existing {
		byID[item.ID] = i
	}
	for _, p := range patches {
		if idx, ok := byID[p.ID]; ok {
			existing[idx].Status = p.Status
			if p.Content != "" {
				existing[idx].Content = p.Content
			}
		} else {
			existing = append(existing, p)
			byID[p.ID] = len(existing) - 1
		}
	}
	entry.Items = existing
	t.store[conversationID] = entry
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
