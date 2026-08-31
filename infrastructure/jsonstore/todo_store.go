package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// TodoStore is a durable, per-conversation todo checklist store backed by a
// single JSON file (conversations/todos.json). It stores both the planning
// brief and the item list per conversation. It is safe for concurrent use.
// Legacy `goal` field in persisted JSON is transparently read into Brief.
//
// The brief is mirrored to a markdown plan file so the agent (and ACP
// subagents) can file_read it. The mirror path is resolved per conversation:
//   - With workspace: <workspace>/.nusashell/plans/<conversation_id>.plan.md
//   - Without workspace: <datadir>/conversations/<conversation_id>/plan.md
type TodoStore struct {
	mu    sync.RWMutex
	path  string
	store map[string]domain.ConversationTodos
	// dataDir is the NusaShell data directory, used as the fallback plan
	// file location when a conversation has no workspace.
	dataDir string
	// workspaceFor resolves the absolute workspace path for a conversation
	// ID. Returns "" when the conversation has no workspace. Injected by
	// the composition root; nil disables workspace-based plan paths.
	workspaceFor func(conversationID string) string
}

// NewTodoStore opens or creates the todo store at path. A missing or corrupt
// file is treated as an empty store so the shell can still boot.
// dataDir is the NusaShell data directory (fallback plan file location).
// workspaceFor resolves the absolute workspace for a conversation ID; pass
// nil when workspace resolution is unavailable (plan files fall back to
// the dataDir location).
func NewTodoStore(path, dataDir string, workspaceFor func(conversationID string) string) *TodoStore {
	t := &TodoStore{
		path:         path,
		dataDir:      dataDir,
		workspaceFor: workspaceFor,
		store:        make(map[string]domain.ConversationTodos),
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
// existing items, persists to disk, and mirrors the brief to the plan file.
func (t *TodoStore) SetBrief(conversationID string, brief string) {
	t.mu.Lock()
	entry := t.store[conversationID]
	entry.Brief = brief
	t.store[conversationID] = entry
	t.persistLocked()
	planPath := t.planPathLocked(conversationID)
	t.mu.Unlock()
	if brief != "" {
		t.writePlanFile(planPath, conversationID, brief)
	} else {
		t.removePlanFile(planPath)
	}
}

// ClearBrief removes the planning brief for the given conversation (items
// are preserved), persists to disk, and deletes the mirrored plan file.
func (t *TodoStore) ClearBrief(conversationID string) error {
	t.mu.Lock()
	entry, ok := t.store[conversationID]
	if !ok || entry.Brief == "" {
		t.mu.Unlock()
		return nil
	}
	entry.Brief = ""
	t.store[conversationID] = entry
	t.persistLocked()
	planPath := t.planPathLocked(conversationID)
	t.mu.Unlock()
	t.removePlanFile(planPath)
	return nil
}

// PlanPath returns the absolute path of the conversation's mirrored plan
// file, or "" when no brief is set or no path can be resolved. The file
// always carries the latest brief body.
func (t *TodoStore) PlanPath(conversationID string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, ok := t.store[conversationID]
	if !ok || entry.Brief == "" {
		return ""
	}
	return t.planPathLocked(conversationID)
}

// planPathLocked resolves the plan file path for a conversation. Callers
// must hold at least a read lock. Workspace-rooted conversations mirror to
// <workspace>/.nusashell/plans/<id>.plan.md (inside the workspace so ACP
// subagents sandboxed to it can read the file); conversations without a
// workspace fall back to <datadir>/conversations/<id>/plan.md.
func (t *TodoStore) planPathLocked(conversationID string) string {
	if t.workspaceFor != nil {
		if ws := t.workspaceFor(conversationID); ws != "" {
			return filepath.Join(ws, ".nusashell", "plans", conversationID+".plan.md")
		}
	}
	if t.dataDir == "" {
		return ""
	}
	return filepath.Join(t.dataDir, "conversations", conversationID, "plan.md")
}

// writePlanFile mirrors the brief to the plan file atomically (temp file +
// rename). The file is the brief body verbatim plus a thin YAML frontmatter
// (conversation id + update time) so it reads like Cursor's *.plan.md files.
// Write errors are swallowed: the JSON store stays the source of truth and
// a failed mirror must never break the todo tool.
func (t *TodoStore) writePlanFile(path, conversationID, brief string) {
	if path == "" {
		return
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("conversation_id: " + conversationID + "\n")
	sb.WriteString("updated_at: " + clock.NewTime().RFC3339() + "\n")
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimRight(brief, "\n"))
	sb.WriteString("\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// removePlanFile deletes the mirrored plan file. Missing files are fine.
func (t *TodoStore) removePlanFile(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// Clear removes the todo state for the given conversation, persists, and
// deletes the mirrored plan file.
func (t *TodoStore) Clear(conversationID string) {
	t.mu.Lock()
	if _, ok := t.store[conversationID]; !ok {
		t.mu.Unlock()
		return
	}
	planPath := t.planPathLocked(conversationID)
	delete(t.store, conversationID)
	t.persistLocked()
	t.mu.Unlock()
	t.removePlanFile(planPath)
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
