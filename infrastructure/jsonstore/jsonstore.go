// Package jsonstore implements the application persistence ports on JSON /
// JSONL files. Credentials never live here; they go to the SQLite store.
package jsonstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"nusashell/domain"
	"nusashell/pkg/atomicfile"
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
	// written tracks the durably-written transcript of each conversation so
	// Save can append changed message lines to pending.jsonl instead of
	// re-encoding and rewriting the whole epoch.
	written        map[string]*convWriteState
	providers      []*domain.Provider
	acpAgents      []*domain.AcpAgent
	experiences    []*domain.Experience
	memoryRecords  []*domain.MemoryRecord
	learningJobs   []*domain.LearningJob
	learningOps    []*domain.LearningOperation
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
		written:       map[string]*convWriteState{},
		settings:      domain.DefaultSettings(),
	}
	for _, sub := range []string{"conversations", "config", "memory", "learning", "growth"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	if report := migrateLegacyConversationLayout(filepath.Join(dir, conversationsDirName)); report.moved() {
		slog.Info("migrated conversations to per-conversation folders",
			"conversations", report.Conversations, "chunk_dirs", report.ChunkDirs, "acp_dirs", report.ACPDirs)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	// conversations: one folder per conversation, holding index.jsonl plus its
	// meta.json/pending.jsonl sidecars, chunk/, acp/, operation/ subdirs and
	// the mirrored plan.md. Flat files in this directory belong to other
	// stores (todos.json, artifacts.json, acp_runs.jsonl and its .imported
	// copies) or are leftovers from the retired flat layout, so only conv_<id>
	// directories are conversations.
	convDir := filepath.Join(s.dir, conversationsDirName)
	entries, err := os.ReadDir(convDir)
	if err != nil {
		return err
	}
	if s.written == nil {
		s.written = map[string]*convWriteState{}
	}
	var recovered []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "conv_") || strings.Contains(name, ".") {
			continue
		}
		folder := filepath.Join(convDir, name)
		b, err := os.ReadFile(filepath.Join(folder, conversationIndexName))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Warn("skipping unreadable conversation transcript", "id", name, "error", err)
			}
			continue
		}
		c, skipped, err := decodeConversationJSONL(b)
		if err != nil {
			slog.Warn("skipping unparsable conversation transcript", "id", name, "error", err)
			continue
		}
		// meta.json holds the newest header: between flushes the index
		// header line can lag, so prefer the sidecar when it parses and
		// keep the index header otherwise.
		if mb, err := os.ReadFile(filepath.Join(folder, conversationMetaName)); err == nil {
			var meta domain.Conversation
			if err := json.Unmarshal(mb, &meta); err == nil {
				messages := c.Messages
				*c = meta
				c.Messages = messages
			} else {
				slog.Warn("skipping unparsable conversation meta", "id", name, "error", err)
			}
		}
		if c.ID != name {
			slog.Warn("skipping conversation whose id does not match its folder", "folder", name, "id", c.ID)
			continue
		}
		// Replay the pending upsert log over the index: known IDs replace
		// content in place, new IDs append at the tail.
		pendingBytes, pendingLines := 0, 0
		if pb, err := os.ReadFile(filepath.Join(folder, conversationPendingName)); err == nil && len(bytes.TrimSpace(pb)) > 0 {
			var pSkipped int
			pendingLines, pSkipped = replayPendingJSONL(c, pb)
			pendingBytes, skipped = len(pb), skipped+pSkipped
		}
		if skipped > 0 {
			slog.Warn("skipped unparsable transcript lines", "id", name, "lines", skipped)
		}
		s.conversations[c.ID] = c
		st := newConvWriteState(c.Messages)
		st.pendingBytes, st.pendingLines = pendingBytes, pendingLines
		s.written[c.ID] = st
		if c.Status == "running" {
			// Eligibility check matching recoverRunningTurn's trigger. The
			// recovery mutates the stored snapshot in place, so save a
			// pre-recovery baseline as the diff reference — the recovery
			// save then appends just the patched messages to pending.jsonl
			// instead of rewriting index.jsonl on every crash-recovered
			// boot.
			baseline := c.Clone()
			if c.RecoverAbandonedTurn() {
				s.conversations[c.ID] = baseline
				if err := s.SaveSnapshot(c); err != nil {
					return fmt.Errorf("persist recovered conversation %s: %w", c.ID, err)
				}
				recovered = append(recovered, c.ID)
			}
		}
	}
	for _, id := range recovered {
		slog.Info("recovered abandoned conversation turn", "id", id)
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
	if err := s.loadGrowth(); err != nil {
		return err
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

// atomicWrite writes via a unique temp file + rename so readers never see torn
// files and concurrent writers of the same path cannot collide on a shared
// temp name (which would race the rename and fail with "no such file").
func atomicWrite(path string, b []byte) error {
	return atomicfile.Write(path, b, 0o644)
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

func providerID(p *domain.Provider) string { return p.ID }
func acpAgentID(a *domain.AcpAgent) string { return a.ID }
func edgeID(e *domain.LearningEdge) string { return e.ID }

// ---- conversations ----

func (s *Store) List() []*domain.Conversation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Conversation, 0, len(s.conversations))
	for _, c := range s.conversations {
		out = append(out, c.Clone())
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
	return c.Clone(), nil
}

// ConversationPath returns the JSONL transcript path used by file_read for a
// stored conversation. The application uses this only as a read-only learning
// handoff; conversation writes still go through Store.Save.
//
// Between flushes the tail of the transcript may live only in the sibling
// pending.jsonl upsert log, so index.jsonl can lag the live conversation.
func (s *Store) ConversationPath(id string) string {
	if s == nil || safeSegment(id) != nil {
		return ""
	}
	dir, err := filepath.Abs(s.dir)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, conversationsDirName, id, conversationIndexName)
}

// convWriteState is the write-side mirror of one conversation's on-disk
// transcript. order is the message-ID sequence of the index+pending view, so
// Save can detect epoch resets (prefix mismatch) and route changed or new
// messages to pending.jsonl instead of rewriting index.jsonl every time.
// Content dirt is found by DeepEqual against the previous stored snapshot in
// s.conversations — no per-message marshal is needed for unchanged lines.
// pendingBytes/pendingLines measure the upsert log until it is folded back
// into index.jsonl at a flush boundary.
type convWriteState struct {
	order        []string
	pendingBytes int
	pendingLines int
	// dupIDs marks a transcript that carries a duplicated message ID: the
	// upsert-by-ID log cannot represent that faithfully, so the conversation
	// always takes the full-rewrite path.
	dupIDs bool
}

// pending.jsonl flush bounds: the upsert log stays small and replays fast;
// crossing either bound folds it back into index.jsonl. Variables (not
// constants) so tests can lower them.
var (
	pendingFlushMaxBytes = 512 << 10
	pendingFlushMaxLines = 2048
)

// newConvWriteState builds the tracked state from the canonical message
// order.
func newConvWriteState(msgs []domain.Message) *convWriteState {
	st := &convWriteState{
		order: make([]string, len(msgs)),
	}
	seen := make(map[string]struct{}, len(msgs))
	for i := range msgs {
		id := msgs[i].ID
		st.order[i] = id
		if _, dup := seen[id]; dup {
			st.dupIDs = true
		}
		seen[id] = struct{}{}
	}
	return st
}

// convNeedsRewrite reports whether saving c requires a full index.jsonl
// rewrite instead of pending appends: no tracked state (first save or
// post-load flush), a truncated or reordered epoch (compaction reset), an
// ID-less message (upsert needs IDs), or a tail message reusing a known ID
// (an append-line upsert would overwrite the earlier position instead).
func convNeedsRewrite(st *convWriteState, c *domain.Conversation) bool {
	if st == nil || st.dupIDs {
		return true
	}
	if len(c.Messages) < len(st.order) {
		return true
	}
	for i, id := range st.order {
		if c.Messages[i].ID != id {
			return true
		}
	}
	var known map[string]struct{}
	for i := range c.Messages {
		id := c.Messages[i].ID
		if id == "" {
			return true
		}
		// A new tail message reusing an ID — whether an already-written one
		// or another tail message's — would upsert over the earlier position
		// instead of appending — take the rewrite path.
		if i >= len(st.order) {
			if known == nil {
				known = make(map[string]struct{}, len(st.order)+len(c.Messages))
				for _, o := range st.order {
					known[o] = struct{}{}
				}
			}
			if _, dup := known[id]; dup {
				return true
			}
			known[id] = struct{}{}
		}
	}
	return false
}

// Save persists a private clone of c so the stored snapshot is never
// aliased to the caller's object. Callers that already own a private copy
// (e.g. conversation.Repository's snapshot) can use SaveSnapshot to skip
// the extra marshal round-trip.
func (s *Store) Save(c *domain.Conversation) error {
	if c == nil {
		return errors.New("conversation is nil")
	}
	return s.SaveSnapshot(c.Clone())
}

// SaveSnapshot persists c like Save but takes ownership of the object
// instead of deep-cloning it first: the caller hands over a private copy and
// must not mutate it afterwards. That skips one of the full JSON
// round-trips a plain Save performs per call.
//
// The write itself is append-mostly: meta.json receives the newest header on
// every save, changed or new message lines append to pending.jsonl (one
// open/write/fsync pass), and index.jsonl is only rewritten when the epoch
// resets or the pending log crosses a flush bound.
func (s *Store) SaveSnapshot(c *domain.Conversation) error {
	if c == nil {
		return errors.New("conversation is nil")
	}
	conversationsDir := filepath.Join(s.dir, conversationsDirName)
	indexPath, err := conversationIndexPath(conversationsDir, c.ID)
	if err != nil {
		return err
	}
	metaPath, err := conversationMetaPath(conversationsDir, c.ID)
	if err != nil {
		return err
	}
	pendingPath, err := conversationPendingPath(conversationsDir, c.ID)
	if err != nil {
		return err
	}
	// Marshal only the header up front: it is tiny. Message lines are encoded
	// lazily — the whole point of the pending log is that most saves touch a
	// handful of messages, so marshaling the whole transcript per save would
	// keep the old O(transcript) cost alive.
	headerLine, err := json.Marshal(conversationHeader{Conversation: *c})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conversations == nil {
		s.conversations = map[string]*domain.Conversation{}
	}
	if s.written == nil {
		s.written = map[string]*convWriteState{}
	}
	if err := atomicWrite(metaPath, headerLine); err != nil {
		return err
	}

	st := s.written[c.ID]
	prev := s.conversations[c.ID]
	if prev == nil || convNeedsRewrite(st, c) {
		// Full rewrite: remove the stale upsert log before the new epoch
		// lands so a crash can never replay old upserts over a redefined
		// transcript; what a mid-write crash leaves is a consistent,
		// slightly older index.
		if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		msgLines, err := marshalMessageLines(c)
		if err != nil {
			return err
		}
		if err := writeConversationIndex(indexPath, headerLine, msgLines); err != nil {
			return err
		}
		s.written[c.ID] = newConvWriteState(c.Messages)
		s.conversations[c.ID] = c
		return nil
	}

	// Incremental path: prev is the last snapshot this store committed, so a
	// DeepEqual per message is the dirty check — content changes anywhere in
	// the transcript are caught without marshaling unchanged lines.
	var dirty [][]byte
	for i := range c.Messages {
		if i < len(st.order) && i < len(prev.Messages) &&
			prev.Messages[i].ID == c.Messages[i].ID &&
			reflect.DeepEqual(c.Messages[i], prev.Messages[i]) {
			continue
		}
		line, err := json.Marshal(c.Messages[i])
		if err != nil {
			return err
		}
		dirty = append(dirty, line)
	}
	if len(dirty) > 0 {
		n, err := appendPendingLines(pendingPath, dirty)
		if err != nil {
			return err
		}
		st.pendingBytes += n
		st.pendingLines += len(dirty)
		for i := len(st.order); i < len(c.Messages); i++ {
			st.order = append(st.order, c.Messages[i].ID)
		}
		if st.pendingBytes >= pendingFlushMaxBytes || st.pendingLines >= pendingFlushMaxLines {
			// Flush: fold pending back into the canonical index. Rewriting
			// index.jsonl first keeps a mid-flush crash consistent — every
			// pending upsert is already in the new index, so replaying the
			// log over it is idempotent.
			msgLines, merr := marshalMessageLines(c)
			if merr != nil {
				slog.Warn("conversation index flush could not encode transcript; pending log kept", "id", c.ID, "error", merr)
			} else if err := writeConversationIndex(indexPath, headerLine, msgLines); err != nil {
				slog.Warn("conversation index flush failed; pending log kept", "id", c.ID, "error", err)
			} else if err := os.Remove(pendingPath); err != nil && !os.IsNotExist(err) {
				slog.Warn("pending conversation log could not be removed after flush", "id", c.ID, "error", err)
			} else {
				st.pendingBytes = 0
				st.pendingLines = 0
			}
		}
	}
	s.conversations[c.ID] = c
	return nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := safeSegment(id); err != nil {
		return err
	}
	if _, ok := s.conversations[id]; !ok {
		return fmt.Errorf("%w: conversation %s", ErrNotFound, id)
	}
	delete(s.conversations, id)
	delete(s.written, id)
	// Cascade the conversation's whole folder: index.jsonl transcript, the
	// meta.json/pending.jsonl sidecars, chunk/ archive, acp/ run snapshots,
	// operation/ patches, and plan.md.
	// safeSegment above guarantees `id` cannot escape the conversations
	// directory, so this RemoveAll cannot touch siblings.
	dir := filepath.Join(s.dir, conversationsDirName, id)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ArchiveChunk persists a slice of messages as an archived pre-compaction
// chunk. The chunk index is sequential (0, 1, 2, ...) and returned to the
// caller so the conversation can track ChunkCount.
func (s *Store) ArchiveChunk(id string, messages []domain.Message) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	chunkDir, err := conversationChunkDir(filepath.Join(s.dir, conversationsDirName), id)
	if err != nil {
		return 0, err
	}
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
	chunkDir, err := conversationChunkDir(filepath.Join(s.dir, conversationsDirName), id)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(chunkDir, fmt.Sprintf("chunk-%d.json", index))
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
		case "gemini":
			if p.Driver != domain.ProviderDriverGemini {
				p.Driver = domain.ProviderDriverGemini
				changed = true
			}
			if p.Kind != domain.ProviderGemini {
				p.Kind = domain.ProviderGemini
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

func (s *Store) appendJSONL(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	// fsync keeps insert-appends at the same durability level as the atomic
	// whole-file rewrite they replaced.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
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
