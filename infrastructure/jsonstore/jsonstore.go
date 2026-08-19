// Package jsonstore implements the application persistence ports on JSON /
// JSONL files. Credentials never live here; they go to the SQLite store.
package jsonstore

import (
	"encoding/json"
	"errors"
	"fmt"
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

	mu            sync.RWMutex
	conversations map[string]*domain.Conversation
	providers     []*domain.Provider
	acpAgents     []*domain.AcpAgent
	skills        []*domain.Skill
	memories      []*domain.MemoryEntry
	learningEdges []*domain.LearningEdge
	settings      domain.Settings

	logMu sync.Mutex
}

var ErrNotFound = errors.New("not found")

func New(dir string) (*Store, error) {
	s := &Store{
		dir:           dir,
		conversations: map[string]*domain.Conversation{},
		settings:      domain.DefaultSettings(),
	}
	for _, sub := range []string{"conversations"} {
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
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(convDir, e.Name()))
		if err != nil {
			return err
		}
		var c domain.Conversation
		if err := json.Unmarshal(b, &c); err != nil {
			return fmt.Errorf("conversation %s: %w", e.Name(), err)
		}
		s.conversations[c.ID] = &c
	}

	if err := s.loadJSON("providers.json", &s.providers); err != nil {
		return err
	}
	s.migrateProviderKinds()
	if err := s.loadJSON("acp-agents.json", &s.acpAgents); err != nil {
		return err
	}
	if err := s.loadJSON("skills.json", &s.skills); err != nil {
		return err
	}
	if err := s.loadJSON("settings.json", &s.settings); err != nil {
		return err
	}
	s.settings = domain.NormalizeSettings(s.settings)
	// memories: JSONL
	if b, err := os.ReadFile(filepath.Join(s.dir, "memories.jsonl")); err == nil {
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
	} else if !os.IsNotExist(err) {
		return err
	}
	// learning_edges: JSONL
	if b, err := os.ReadFile(filepath.Join(s.dir, "learning_edges.jsonl")); err == nil {
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

func getAtomicWriter(path string) *atomicWriter {
	atomicWritersMu.Lock()
	defer atomicWritersMu.Unlock()

	if w, ok := atomicWriters[path]; ok {
		return w
	}

	w := &atomicWriter{}
	atomicWriters[path] = w
	return w
}

// atomicWrite writes via a unique temp file + rename so readers never see torn
// files and concurrent writers of the same path cannot collide on a shared
// temp name (which would race the rename and fail with "no such file").
func atomicWrite(path string, b []byte) error {
	w := getAtomicWriter(path)
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
	}
	if changed {
		_ = s.writeJSON("providers.json", s.providers)
	}
}

// ---- providers ----

func (s *Store) ListProviders() []*domain.Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Provider, len(s.providers))
	for i, p := range s.providers {
		out[i] = clone(p)
	}
	return out
}

func (s *Store) GetProvider(id string) (*domain.Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.providers {
		if p.ID == id {
			return clone(p), nil
		}
	}
	return nil, fmt.Errorf("%w: provider %s", ErrNotFound, id)
}

func (s *Store) SaveProvider(p *domain.Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := clone(p)
	for i, existing := range s.providers {
		if existing.ID == p.ID {
			s.providers[i] = stored
			return s.writeJSON("providers.json", s.providers)
		}
	}
	s.providers = append(s.providers, stored)
	return s.writeJSON("providers.json", s.providers)
}

func (s *Store) DeleteProvider(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.providers {
		if p.ID == id {
			s.providers = append(s.providers[:i], s.providers[i+1:]...)
			return s.writeJSON("providers.json", s.providers)
		}
	}
	return fmt.Errorf("%w: provider %s", ErrNotFound, id)
}

// ---- ACP agents ----

func (s *Store) ListAcpAgents() []*domain.AcpAgent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.AcpAgent, len(s.acpAgents))
	for i, a := range s.acpAgents {
		out[i] = clone(a)
	}
	return out
}

func (s *Store) GetAcpAgent(id string) (*domain.AcpAgent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.acpAgents {
		if a.ID == id {
			return clone(a), nil
		}
	}
	return nil, fmt.Errorf("%w: acp agent %s", ErrNotFound, id)
}

func (s *Store) SaveAcpAgent(a *domain.AcpAgent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := clone(a)
	for i, existing := range s.acpAgents {
		if existing.ID == a.ID {
			s.acpAgents[i] = stored
			return s.writeJSON("acp-agents.json", s.acpAgents)
		}
	}
	s.acpAgents = append(s.acpAgents, stored)
	return s.writeJSON("acp-agents.json", s.acpAgents)
}

func (s *Store) DeleteAcpAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.acpAgents {
		if a.ID == id {
			s.acpAgents = append(s.acpAgents[:i], s.acpAgents[i+1:]...)
			return s.writeJSON("acp-agents.json", s.acpAgents)
		}
	}
	return fmt.Errorf("%w: acp agent %s", ErrNotFound, id)
}

// ---- skills ----

func (s *Store) ListSkills() []*domain.Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Skill, len(s.skills))
	for i, sk := range s.skills {
		out[i] = clone(sk)
	}
	return out
}

func (s *Store) GetSkill(id string) (*domain.Skill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sk := range s.skills {
		if sk.ID == id {
			return clone(sk), nil
		}
	}
	return nil, fmt.Errorf("%w: skill %s", ErrNotFound, id)
}

func (s *Store) SaveSkill(sk *domain.Skill) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := clone(sk)
	for i, existing := range s.skills {
		if existing.ID == sk.ID {
			s.skills[i] = stored
			return s.writeJSON("skills.json", s.skills)
		}
	}
	s.skills = append(s.skills, stored)
	return s.writeJSON("skills.json", s.skills)
}

func (s *Store) DeleteSkill(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sk := range s.skills {
		if sk.ID == id {
			s.skills = append(s.skills[:i], s.skills[i+1:]...)
			return s.writeJSON("skills.json", s.skills)
		}
	}
	return fmt.Errorf("%w: skill %s", ErrNotFound, id)
}

// ---- memory ----

func (s *Store) ListMemories() []*domain.MemoryEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.MemoryEntry, len(s.memories))
	for i, e := range s.memories {
		out[i] = clone(e)
	}
	return out
}

func (s *Store) SaveMemory(e *domain.MemoryEntry) error {
	if e.Target == "" {
		e.Target = domain.MemoryTargetMemory
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	used := s.targetChars(e.Target)
	limit := domain.MemoryLimit(e.Target)
	if used+len(e.Content) > limit {
		return fmt.Errorf("memory target %q at %d/%d chars; adding %d would exceed the limit — use memory_replace to merge or remove stale entries first", e.Target, used, limit, len(e.Content))
	}
	stored := clone(e)
	s.memories = append(s.memories, stored)
	return s.appendJSONL("memories.jsonl", stored)
}

func (s *Store) DeleteMemory(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.memories {
		if e.ID == id {
			s.memories = append(s.memories[:i], s.memories[i+1:]...)
			return s.writeJSONL("memories.jsonl", s.memories)
		}
	}
	return fmt.Errorf("%w: memory %s", ErrNotFound, id)
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
	return s.writeJSONL("memories.jsonl", s.memories)
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
		s.rewriteLogTailLocked()
	}
}

func (s *Store) rewriteLogTailLocked() {
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
	return s.writeJSON("settings.json", settings)
}

// ---- learning edges ----

func (s *Store) ListLearningEdges() []*domain.LearningEdge {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.LearningEdge, len(s.learningEdges))
	for i, e := range s.learningEdges {
		out[i] = clone(e)
	}
	return out
}

func (s *Store) SaveLearningEdge(e *domain.LearningEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := clone(e)
	s.learningEdges = append(s.learningEdges, stored)
	return s.appendJSONL("learning_edges.jsonl", stored)
}

func (s *Store) DeleteLearningEdge(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.learningEdges {
		if e.ID == id {
			s.learningEdges = append(s.learningEdges[:i], s.learningEdges[i+1:]...)
			return s.writeJSONL("learning_edges.jsonl", s.learningEdges)
		}
	}
	return fmt.Errorf("%w: learning edge %s", ErrNotFound, id)
}
