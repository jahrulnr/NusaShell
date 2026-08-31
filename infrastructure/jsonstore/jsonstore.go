// Package jsonstore implements the application persistence ports on JSON /
// JSONL files. Credentials never live here; they go to the SQLite store.
package jsonstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"nusashell/domain"
)

// clone deep-copies an entity so stored objects are private snapshots:
// application code mutates its own copy and Save() publishes a fresh one,
// while concurrent readers never observe in-flight writes (race-safe).
func clone[T any](v *T) *T {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return &out
}

// Store is a file-backed store rooted at dir. It satisfies the
// application persistence ports.
type Store struct {
	dir string

	mu             sync.RWMutex
	conversations  map[string]*domain.Conversation
	providers      []*domain.Provider
	acpAgents      []*domain.AcpAgent
	memories       []*domain.MemoryEntry
	learningEdges  []*domain.LearningEdge
	learnedParams  *domain.LearnedParamRegistry
	modelOverrides *domain.ModelOverrideRegistry
	settings       domain.Settings

	logMu sync.Mutex
}

var ErrNotFound = errors.New("not found")

func New(dir string) (*Store, error) {
	s := &Store{
		dir:           dir,
		conversations: map[string]*domain.Conversation{},
		settings:      domain.DefaultSettings(),
	}
	for _, sub := range []string{"conversations", "config", "memory", "learning"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	// conversations: one JSON file per conversation
	convDir := filepath.Join(s.dir, "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		return err
	}
	// Only plain conv_<id>.json files are conversations. This directory also
	// holds files owned by other stores (todos.json, artifacts.json,
	// acp_runs.jsonl) and legacy sidecars from the retired desktop app
	// (conv_<id>.meta.json / .runtime.json, whose meta "model" is an
	// object). Treating those as conversations produced unmarshal failures
	// that killed startup, so anything that is not exactly conv_<id>.json —
	// or that fails to parse — is skipped with a warning instead.
	var recovered []string
	for _, e := range entries {
		name := e.Name()
		base, ext := strings.CutSuffix(name, ".json")
		if e.IsDir() || !ext || !strings.HasPrefix(base, "conv_") || strings.Contains(base, ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(convDir, name))
		if err != nil {
			slog.Warn("skipping unreadable conversation file", "file", name, "error", err)
			continue
		}
		var c domain.Conversation
		if err := json.Unmarshal(b, &c); err != nil {
			slog.Warn("skipping unparsable conversation file", "file", name, "error", err)
			continue
		}
		if c.ID == "" {
			continue // defensive: a conversation without an ID is unusable
		}
		if c.RecoverAbandonedTurn() {
			recovered = append(recovered, c.ID)
		}
		s.conversations[c.ID] = &c
	}
	for _, id := range recovered {
		if err := s.Save(s.conversations[id]); err != nil {
			return fmt.Errorf("persist recovered conversation %s: %w", id, err)
		}
	}

	if err := s.loadJSON("config/providers.json", &s.providers); err != nil {
		return err
	}
	s.migrateProviderKinds()
	if err := s.loadJSON("config/acp-agents.json", &s.acpAgents); err != nil {
		return err
	}
	if err := s.loadJSON("config/settings.json", &s.settings); err != nil {
		return err
	}
	s.settings = domain.NormalizeSettings(s.settings)
	// memories: JSONL. Primary file is memory/memory.jsonl;
	// memory/legacy.jsonl is still loaded for backward compatibility with
	// stores predating the save-vs-delete file split fix.
	for _, name := range []string{"memory/memory.jsonl", "memory/legacy.jsonl"} {
		b, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var e domain.MemoryEntry
			if err := json.Unmarshal([]byte(line), &e); err == nil {
				if e.Target == "" {
					e.Target = domain.MemoryTargetMemory
				}
				s.memories = append(s.memories, &e)
			}
		}
	}
	// learning_edges: JSONL
	if b, err := os.ReadFile(filepath.Join(s.dir, "learning", "edges.jsonl")); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var e domain.LearningEdge
			if err := json.Unmarshal([]byte(line), &e); err == nil {
				s.learningEdges = append(s.learningEdges, &e)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// learned_params: single JSON registry (dynamic 400-learning)
	s.learnedParams = domain.NewLearnedParamRegistry()
	if err := s.loadJSON("learning/provider_params.json", s.learnedParams); err != nil {
		return err
	}
	// model_overrides: single JSON registry (manual catalog corrections)
	s.modelOverrides = domain.NewModelOverrideRegistry()
	if err := s.loadJSON("learning/model_overrides.json", s.modelOverrides); err != nil {
		return err
	}
	return nil
}

func (s *Store) loadJSON(name string, dst any) error {
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func (s *Store) writeJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.dir, name), b)
}

type atomicWriter struct {
	mu sync.Mutex
}

var (
	atomicWritersMu sync.Mutex
	atomicWriters   = make(map[string]*atomicWriter)
)

// atomicWrite writes via a unique temp file + rename so readers never see torn
// files and concurrent writers of the same path cannot collide on a shared
// temp name (which would race the rename and fail with "no such file").
func atomicWrite(path string, b []byte) error {
	atomicWritersMu.Lock()
	w, ok := atomicWriters[path]
	if !ok {
		w = &atomicWriter{}
		atomicWriters[path] = w
	}
	atomicWritersMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".nusashell-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() {
		_ = os.Remove(name)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// ---- generic ID-keyed collections ----
//
// The slice-backed CRUD families (providers, ACP agents) share one
// shape: a JSON file holding a slice of ID-keyed entities with clone-on-read
// snapshot isolation and atomic whole-file writes. The helpers below are the
// single implementation; the per-entity methods are one-liners over them.
// The JSONL families (memories, learning edges) share List/Delete but keep
// their own append-only Save (no upsert semantics).

// listSlice returns deep copies of every item.
func listSlice[T any](s *Store, items []*T) []*T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*T, len(items))
	for i, it := range items {
		out[i] = clone(it)
	}
	return out
}

// getSlice returns a deep copy of the item with the given ID, or ErrNotFound.
func getSlice[T any](s *Store, items []*T, id string, idOf func(*T) string, kind string) (*T, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range items {
		if idOf(it) == id {
			return clone(it), nil
		}
	}
	return nil, fmt.Errorf("%w: %s %s", ErrNotFound, kind, id)
}

// saveSlice upserts v into items (replace by ID or append), persists the
// whole slice to path, and returns the updated slice.
func saveSlice[T any](s *Store, items []*T, v *T, idOf func(*T) string, path string) ([]*T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := clone(v)
	for i, existing := range items {
		if idOf(existing) == idOf(v) {
			items[i] = stored
			return items, s.writeJSON(path, items)
		}
	}
	items = append(items, stored)
	return items, s.writeJSON(path, items)
}

// removeSlice removes the item with the given ID, persists the remaining
// slice via persist, and returns it. Errors use the kind label ("provider").
func removeSlice[T any](s *Store, items []*T, id string, idOf func(*T) string, kind string, persist func([]*T) error) ([]*T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, it := range items {
		if idOf(it) == id {
			next := append(items[:i], items[i+1:]...)
			return next, persist(next)
		}
	}
	return items, fmt.Errorf("%w: %s %s", ErrNotFound, kind, id)
}

// loadRegistry returns a deep copy of the single-registry pointer, or a
// fresh value via newFn when nothing was loaded yet. Never returns nil.
func loadRegistry[T any](s *Store, ptr **T, newFn func() *T) *T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if *ptr == nil {
		return newFn()
	}
	return clone(*ptr)
}

// saveRegistry persists a deep copy of the registry atomically and swaps
// the in-memory pointer.
func saveRegistry[T any](s *Store, ptr **T, v *T, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := clone(v)
	*ptr = stored
	return s.writeJSON(path, stored)
}

func providerID(p *domain.Provider) string  { return p.ID }
func acpAgentID(a *domain.AcpAgent) string  { return a.ID }
func memoryID(e *domain.MemoryEntry) string { return e.ID }
func edgeID(e *domain.LearningEdge) string  { return e.ID }

// ---- conversations ----

func (s *Store) List() []*domain.Conversation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Conversation, 0, len(s.conversations))
	for _, c := range s.conversations {
		out = append(out, clone(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *Store) Get(id string) (*domain.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.conversations[id]
	if !ok {
		return nil, fmt.Errorf("%w: conversation %s", ErrNotFound, id)
	}
	return clone(c), nil
}

// Path returns the absolute file path where a conversation's JSON is stored.
// Used by the review agent to expose the path in the review_transcript tool
// result so the agent can file_read the full conversation when the bounded
// segment lacks context. Returns "" for unsafe IDs.
func (s *Store) Path(id string) string {
	if err := safeSegment(id); err != nil {
		return ""
	}
	return filepath.Join(s.dir, "conversations", id+".json")
}

func (s *Store) Save(c *domain.Conversation) error {
	stored := clone(c)
	b, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "conversations", c.ID+".json")
	if err := atomicWrite(path, b); err != nil {
		return err
	}
	s.mu.Lock()
	s.conversations[c.ID] = stored
	s.mu.Unlock()
	return nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conversations[id]; !ok {
		return fmt.Errorf("%w: conversation %s", ErrNotFound, id)
	}
	delete(s.conversations, id)
	if err := os.Remove(filepath.Join(s.dir, "conversations", id+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Remove any archived chunks for this conversation.
	chunkDir := filepath.Join(s.dir, "conversations", id+".chunks")
	if entries, err := os.ReadDir(chunkDir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(chunkDir, e.Name()))
		}
		_ = os.Remove(chunkDir)
	}
	return nil
}

// ArchiveChunk persists a slice of messages as an archived pre-compaction
// chunk. The chunk index is sequential (0, 1, 2, ...) and returned to the
// caller so the conversation can track ChunkCount.
func (s *Store) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chunkDir := filepath.Join(s.dir, "conversations", id+".chunks")
	if err := os.MkdirAll(chunkDir, 0o755); err != nil {
		return 0, err
	}
	// Determine the next chunk index by scanning existing files.
	entries, err := os.ReadDir(chunkDir)
	if err != nil {
		return 0, err
	}
	nextIndex := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		nextIndex++
	}
	path := filepath.Join(chunkDir, fmt.Sprintf("chunk-%d.json", nextIndex))
	b, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := atomicWrite(path, b); err != nil {
		return 0, err
	}
	return nextIndex, nil
}

// GetChunk retrieves an archived chunk by index. Returns ErrNotFound if the
// chunk file does not exist.
func (s *Store) GetChunk(id string, index int) ([]domain.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path := filepath.Join(s.dir, "conversations", id+".chunks", fmt.Sprintf("chunk-%d.json", index))
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: chunk %d for conversation %s", ErrNotFound, index, id)
		}
		return nil, err
	}
	var msgs []domain.Message
	if err := json.Unmarshal(b, &msgs); err != nil {
		return nil, fmt.Errorf("chunk %d for %s: %w", index, id, err)
	}
	return msgs, nil
}

// migrateProviderKinds maps the pre-universal kind values (anthropic,
// openai, compatible) onto the current API-shape kinds and persists when
// anything changed.
func (s *Store) migrateProviderKinds() {
	changed := false
	for _, p := range s.providers {
		switch p.Kind {
		case "anthropic":
			p.Kind = domain.ProviderMessages
			changed = true
		case "openai", "compatible":
			p.Kind = domain.ProviderChat
			changed = true
		}
		switch p.ID {
		case "anthropic":
			if p.Driver != domain.ProviderDriverAnthropic {
				p.Driver = domain.ProviderDriverAnthropic
				changed = true
			}
			if p.Kind != domain.ProviderMessages {
				p.Kind = domain.ProviderMessages
				changed = true
			}
		case "openai":
			if p.Driver != domain.ProviderDriverOpenAI {
				p.Driver = domain.ProviderDriverOpenAI
				changed = true
			}
			if p.Kind != domain.ProviderResponses {
				p.Kind = domain.ProviderResponses
				changed = true
			}
		case "openrouter":
			if p.Driver != domain.ProviderDriverOpenRouter {
				p.Driver = domain.ProviderDriverOpenRouter
				changed = true
			}
			if !domain.ValidKind(p.Kind) {
				p.Kind = domain.ProviderChat
				changed = true
			}
		}
	}
	if changed {
		_ = s.writeJSON("config/providers.json", s.providers)
	}
}

// ---- providers ----

func (s *Store) ListProviders() []*domain.Provider { return listSlice(s, s.providers) }

func (s *Store) GetProvider(id string) (*domain.Provider, error) {
	return getSlice(s, s.providers, id, providerID, "provider")
}

func (s *Store) SaveProvider(p *domain.Provider) (err error) {
	s.providers, err = saveSlice(s, s.providers, p, providerID, "config/providers.json")
	return err
}

func (s *Store) DeleteProvider(id string) (err error) {
	s.providers, err = removeSlice(s, s.providers, id, providerID, "provider", func(items []*domain.Provider) error {
		return s.writeJSON("config/providers.json", items)
	})
	return err
}

// ---- ACP agents ----

func (s *Store) ListAcpAgents() []*domain.AcpAgent { return listSlice(s, s.acpAgents) }

func (s *Store) GetAcpAgent(id string) (*domain.AcpAgent, error) {
	return getSlice(s, s.acpAgents, id, acpAgentID, "acp agent")
}

func (s *Store) SaveAcpAgent(a *domain.AcpAgent) (err error) {
	s.acpAgents, err = saveSlice(s, s.acpAgents, a, acpAgentID, "config/acp-agents.json")
	return err
}

func (s *Store) DeleteAcpAgent(id string) (err error) {
	s.acpAgents, err = removeSlice(s, s.acpAgents, id, acpAgentID, "acp agent", func(items []*domain.AcpAgent) error {
		return s.writeJSON("config/acp-agents.json", items)
	})
	return err
}

// ---- memory ----

func (s *Store) ListMemories() []*domain.MemoryEntry { return listSlice(s, s.memories) }

func (s *Store) SaveMemory(e *domain.MemoryEntry) error {
	if e.Target == "" {
		e.Target = domain.MemoryTargetMemory
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	used := s.targetChars(e.Target)
	limit := domain.MemoryLimit(e.Target)
	if used+len(e.Content) > limit {
		return fmt.Errorf("memory target %q at %d/%d chars; adding %d would exceed the limit — use memory with op=replace to merge or remove stale entries first", e.Target, used, limit, len(e.Content))
	}
	stored := clone(e)
	s.memories = append(s.memories, stored)
	return s.appendJSONL("memory/memory.jsonl", stored)
}

func (s *Store) DeleteMemory(id string) (err error) {
	s.memories, err = removeSlice(s, s.memories, id, memoryID, "memory", func(items []*domain.MemoryEntry) error {
		return s.writeJSONL("memory/memory.jsonl", items)
	})
	return err
}

// ReplaceMemory finds the single entry in target whose content contains
// oldText as a substring, replaces its content with content, and preserves
// its ID, Target, Source, and CreatedAt. Returns an error if zero or
// multiple entries match.
func (s *Store) ReplaceMemory(target, oldText, content string) error {
	if target == "" {
		target = domain.MemoryTargetMemory
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	indices := make([]int, 0, 1)
	for i, e := range s.memories {
		if e.Target != target {
			continue
		}
		if strings.Contains(e.Content, oldText) {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return fmt.Errorf("no %q entry contains %q", target, oldText)
	}
	if len(indices) > 1 {
		return fmt.Errorf("multiple %q entries contain %q — use a more specific substring", target, oldText)
	}
	used := s.targetChars(target)
	old := s.memories[indices[0]]
	if used-len(old.Content)+len(content) > domain.MemoryLimit(target) {
		return fmt.Errorf("replacement would exceed the %q char limit (%d/%d)", target, used-len(old.Content)+len(content), domain.MemoryLimit(target))
	}
	s.memories[indices[0]].Content = content
	return s.writeJSONL("memory/memory.jsonl", s.memories)
}

// targetChars returns the total content length across entries in a target.
func (s *Store) targetChars(target string) int {
	total := 0
	for _, e := range s.memories {
		if e.Target == target {
			total += len(e.Content)
		}
	}
	return total
}

func (s *Store) appendJSONL(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *Store) writeJSONL(name string, v any) error {
	var sb strings.Builder
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(b, &arr); err != nil {
		return err
	}
	for _, item := range arr {
		sb.Write(item)
		sb.WriteByte('\n')
	}
	return atomicWrite(filepath.Join(s.dir, name), []byte(sb.String()))
}

// ---- logs ----

const maxLogEntries = 2000

func (s *Store) AppendLog(e *domain.LogEntry) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	line, _ := json.Marshal(e)
	f, err := os.OpenFile(filepath.Join(s.dir, "logs.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
	// keep the file bounded: when it exceeds maxLogEntries * 2 lines, rewrite
	// with only the tail. Cheap enough for a personal shell.
	if fi, err := f.Stat(); err == nil && fi.Size() > maxLogEntries*300 {
		entries := s.readLogsLocked()
		if len(entries) > maxLogEntries {
			entries = entries[len(entries)-maxLogEntries:]
		}
		var sb strings.Builder
		for _, e := range entries {
			b, _ := json.Marshal(e)
			sb.Write(b)
			sb.WriteByte('\n')
		}
		_ = atomicWrite(filepath.Join(s.dir, "logs.jsonl"), []byte(sb.String()))
	}
}

func (s *Store) readLogsLocked() []*domain.LogEntry {
	b, err := os.ReadFile(filepath.Join(s.dir, "logs.jsonl"))
	if err != nil {
		return nil
	}
	var out []*domain.LogEntry
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e domain.LogEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, &e)
		}
	}
	return out
}

func (s *Store) ListLogs(level string, limit int) []*domain.LogEntry {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	entries := s.readLogsLocked()
	// Collect the most recent `limit` matching entries by walking backwards
	// from the end of the on-disk log (which is in append = chronological
	// order). Then reverse so the result is oldest-first: the Logs view
	// appends rows in slice order and "Follow" scrolls to the bottom, so
	// the newest entry must be last for follow-to-latest to work.
	var recent []*domain.LogEntry
	for i := len(entries) - 1; i >= 0 && len(recent) < limit; i-- {
		e := entries[i]
		if level != "" && e.Level != level {
			continue
		}
		recent = append(recent, e)
	}
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	return recent
}

func (s *Store) ClearLogs() {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	_ = os.Remove(filepath.Join(s.dir, "logs.jsonl"))
}

// ---- settings ----

func (s *Store) GetSettings() domain.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Store) SetSettings(settings domain.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
	return s.writeJSON("config/settings.json", settings)
}

// ApplySettings swaps the in-memory settings without writing the file back.
// The settings watcher uses it after an external edit passes validation:
// disk stays the source of truth, memory follows it. Callers must pass
// already-normalized settings.
func (s *Store) ApplySettings(settings domain.Settings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
}

// ---- learning edges ----

func (s *Store) ListLearningEdges() []*domain.LearningEdge { return listSlice(s, s.learningEdges) }

func (s *Store) SaveLearningEdge(e *domain.LearningEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := clone(e)
	s.learningEdges = append(s.learningEdges, stored)
	return s.appendJSONL("learning/edges.jsonl", stored)
}

func (s *Store) DeleteLearningEdge(id string) (err error) {
	s.learningEdges, err = removeSlice(s, s.learningEdges, id, edgeID, "learning edge", func(items []*domain.LearningEdge) error {
		return s.writeJSONL("learning/edges.jsonl", items)
	})
	return err
}

// ---- learned params (dynamic 400-learning) ----

// LoadLearnedParams returns the current learned-param registry. The
// registry is loaded once at startup and kept in memory; callers mutate
// the returned pointer and call SaveLearnedParams to persist. Returns an
// empty (non-nil) registry when no learning file exists yet.
func (s *Store) LoadLearnedParams() *domain.LearnedParamRegistry {
	return loadRegistry(s, &s.learnedParams, domain.NewLearnedParamRegistry)
}

// SaveLearnedParams persists the registry atomically to
// learning/provider_params.json.
func (s *Store) SaveLearnedParams(r *domain.LearnedParamRegistry) error {
	return saveRegistry(s, &s.learnedParams, r, "learning/provider_params.json")
}

// ---- model overrides (manual catalog corrections) ----

func (s *Store) LoadModelOverrides() *domain.ModelOverrideRegistry {
	return loadRegistry(s, &s.modelOverrides, domain.NewModelOverrideRegistry)
}

// SaveModelOverrides persists the registry atomically to
// learning/model_overrides.json.
func (s *Store) SaveModelOverrides(r *domain.ModelOverrideRegistry) error {
	return saveRegistry(s, &s.modelOverrides, r, "learning/model_overrides.json")
}
