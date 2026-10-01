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
	"nusashell/pkg/atomicfile"
)

// AcpRunStore persists completed ACP runs as one JSON document per run,
// linked to the parent conversation:
//
//	<dir>/conversations/<conversationID>/acp/<runID>.json
//
// Each run owns its file, so concurrent completions of parallel subagent
// spawns never rewrite shared state: there is no global append log to
// interleave or lose updates in, and an atomic write (temp file + rename)
// means a crash mid-write can only ever damage the run being written.
// The layout mirrors the conversation folder's other sidecar directories
// (chunk/, operation/); jsonstore's loader only treats conv_<id>
// directories as conversations, so they are invisible to it.
//
// Stores created before this layout kept every run as JSONL lines in
// conversations/acp_runs.jsonl, where every completion rewrote the whole
// shared file; still older stores kept per-conversation <id>.acp/
// directories. On first use the store migrates both: the legacy JSONL file
// is imported into the per-run layout and renamed to
// acp_runs.jsonl.imported so the original bytes stay recoverable, and the
// legacy sidecar directories are folded into the conversation folders by
// migrateLegacyConversationLayout.
type AcpRunStore struct {
	dir      string
	mu       sync.Mutex
	migrated bool
	// runIndex maps run ID → record file path so Load is O(1) instead of
	// scanning every conversation folder. It is built lazily on the first
	// Load/List/Save after legacy migration (one walk of the conversation
	// folders), then kept current by Save and Path. Entries are re-checked
	// on read: a vanished file drops the stale entry and falls back to the
	// authoritative scan before reporting a miss.
	runIndex map[string]string
}

// NewAcpRunStore creates a per-conversation ACP run store rooted at dir.
// Directories are created on first write.
func NewAcpRunStore(dir string) *AcpRunStore {
	return &AcpRunStore{dir: dir}
}

var errInvalidACPPathSegment = errors.New("id contains characters unsafe for use as a file name")

// safeSegment rejects strings that cannot be used verbatim as a path
// segment. Real IDs come from domain.NewID ("conv_"/"acprun_" + ULID),
// so this only ever trips on corrupted or hand-edited data.
func safeSegment(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("%q: %w", id, errInvalidACPPathSegment)
	}
	return nil
}

func (s *AcpRunStore) conversationsDir() string {
	return filepath.Join(s.dir, conversationsDirName)
}

func (s *AcpRunStore) conversationDir(conversationID string) (string, error) {
	dir, err := conversationACPDir(s.conversationsDir(), conversationID)
	if err != nil {
		return "", fmt.Errorf("acp run store: %w", err)
	}
	return dir, nil
}

func (s *AcpRunStore) runPath(conversationID, runID string) (string, error) {
	dir, err := s.conversationDir(conversationID)
	if err != nil {
		return "", err
	}
	if err := safeSegment(runID); err != nil {
		return "", fmt.Errorf("acp run store: run %w", err)
	}
	return filepath.Join(dir, runID+".json"), nil
}

// Path resolves the file path a run's record is (or will be) stored at.
// Returns "" for unsafe or unresolvable IDs.
func (s *AcpRunStore) Path(conversationID, runID string) string {
	path, err := s.runPath(conversationID, runID)
	if err != nil {
		return ""
	}
	s.mu.Lock()
	// Recording the predicted path is safe: Load re-checks the file before
	// trusting an index hit, so a Path call for a run that is never saved
	// only costs one rescan.
	s.indexRunLocked(runID, path)
	s.mu.Unlock()
	return path
}

// buildIndexLocked walks the conversation folders once and records each run
// file's path. It must run under s.mu after migrateLegacyLocked so runs
// imported by the migration are indexed too.
func (s *AcpRunStore) buildIndexLocked() {
	if s.runIndex != nil {
		return
	}
	s.runIndex = make(map[string]string)
	for _, name := range s.conversationFolders() {
		dir := filepath.Join(s.conversationsDir(), name, acpDirName)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			s.runIndex[strings.TrimSuffix(e.Name(), ".json")] = filepath.Join(dir, e.Name())
		}
	}
}

// indexRunLocked records one run's path when the index is already built.
// A nil index stays nil so the lazy build still covers pre-index writes.
func (s *AcpRunStore) indexRunLocked(runID, path string) {
	if s.runIndex != nil {
		s.runIndex[runID] = path
	}
}

// Save writes the record as its own JSON file, creating or replacing it
// atomically. Saving the same run ID again replaces that run's file —
// other runs are untouched by construction.
func (s *AcpRunStore) Save(record domain.AcpRunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.migrateLegacyLocked(); err != nil {
		return err
	}
	s.buildIndexLocked()
	path, err := s.runPath(record.ConversationID, record.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeJSONAtomic(path, record); err != nil {
		return err
	}
	s.indexRunLocked(record.ID, path)
	return nil
}

// Load returns the record for runID, or (zero, false) if not found.
func (s *AcpRunStore) Load(runID string) (domain.AcpRunRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.migrateLegacyLocked(); err != nil {
		return domain.AcpRunRecord{}, false
	}
	if safeSegment(runID) != nil {
		return domain.AcpRunRecord{}, false
	}
	s.buildIndexLocked()
	if path, ok := s.runIndex[runID]; ok {
		if r, hit := readRun(path); hit {
			return r, true
		}
		// Stale entry (file removed or moved out of band): drop it and fall
		// back to one authoritative scan before reporting a miss.
		delete(s.runIndex, runID)
	}
	for _, name := range s.conversationFolders() {
		path := filepath.Join(s.conversationsDir(), name, acpDirName, runID+".json")
		if r, ok := readRun(path); ok {
			s.runIndex[runID] = path
			return r, true
		}
	}
	return domain.AcpRunRecord{}, false
}

// List returns all records for the given conversation, sorted by
// StartedAt ascending. An empty conversationID returns all records.
func (s *AcpRunStore) List(conversationID string) []domain.AcpRunRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.migrateLegacyLocked(); err != nil {
		return nil
	}
	// Warm the Load index while the folders are already being walked; the
	// one-time cost is an extra directory listing per conversation.
	s.buildIndexLocked()

	var out []domain.AcpRunRecord
	if conversationID == "" {
		for _, name := range s.conversationFolders() {
			out = append(out, readConversationRuns(filepath.Join(s.conversationsDir(), name, acpDirName))...)
		}
	} else {
		dir, err := s.conversationDir(conversationID)
		if err != nil {
			return nil
		}
		out = readConversationRuns(dir)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// conversationFolders lists the per-conversation directories under the
// conversations root. Only conv_<id> directories are conversation folders;
// flat files (todos.json, acp_runs.jsonl.imported, ...) are not.
func (s *AcpRunStore) conversationFolders() []string {
	entries, err := os.ReadDir(s.conversationsDir())
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && strings.HasPrefix(name, "conv_") && !strings.Contains(name, ".") {
			out = append(out, name)
		}
	}
	return out
}

// migrateLegacyLocked imports a pre-existing global acp_runs.jsonl into
// the per-run layout, then renames it to acp_runs.jsonl.imported. Legacy
// per-conversation <id>.acp/ directories are folded into the conversation
// folders by migrateLegacyConversationLayout. It must run under s.mu.
// Failures block further saves rather than letting new writes fall back
// into the shared legacy file; malformed lines and lines with unusable IDs
// are skipped (the .imported copy keeps the originals).
func (s *AcpRunStore) migrateLegacyLocked() error {
	if s.migrated {
		return nil
	}
	migrateLegacyConversationLayout(s.conversationsDir())
	legacy := filepath.Join(s.conversationsDir(), "acp_runs.jsonl")
	defer func() { s.migrated = true }()

	b, err := os.ReadFile(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh install or already migrated
		}
		return fmt.Errorf("acp run store: read legacy %s: %w", legacy, err)
	}

	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r domain.AcpRunRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		path, err := s.runPath(r.ConversationID, r.ID)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := writeJSONAtomic(path, r); err != nil {
			return err
		}
	}

	// Rename (never delete) so the migration stays reversible.
	return os.Rename(legacy, legacy+".imported")
}

// readConversationRuns loads every decodable run file in a conversation's
// .acp directory. Undecodable files are skipped: one damaged run must not
// hide the rest.
func readConversationRuns(dir string) []domain.AcpRunRecord {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]domain.AcpRunRecord, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if r, ok := readRun(filepath.Join(dir, e.Name())); ok {
			out = append(out, r)
		}
	}
	return out
}

func readRun(path string) (domain.AcpRunRecord, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return domain.AcpRunRecord{}, false
	}
	var r domain.AcpRunRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return domain.AcpRunRecord{}, false
	}
	return r, true
}

// writeJSONAtomic writes v as a single compacted JSON document using
// write-to-temp-then-rename, so readers only ever observe complete files.
func writeJSONAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, b, 0o600)
}
