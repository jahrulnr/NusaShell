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

// AcpRunStore persists completed ACP runs as one JSON document per run,
// linked to the parent conversation:
//
//	<dir>/conversations/<conversationID>.acp/<runID>.json
//
// Each run owns its file, so concurrent completions of parallel subagent
// spawns never rewrite shared state: there is no global append log to
// interleave or lose updates in, and an atomic write (temp file + rename)
// means a crash mid-write can only ever damage the run being written.
// The layout mirrors the existing <conversationID>.chunks sidecar
// convention; jsonstore's loader only treats conv_*.json as
// conversations, so the .acp directories are invisible to it.
//
// Stores created before this layout kept every run as JSONL lines in
// conversations/acp_runs.jsonl, where every completion rewrote the whole
// shared file. On first use the store migrates that legacy file into the
// per-run layout and renames it to acp_runs.jsonl.imported so the
// original bytes stay recoverable.
type AcpRunStore struct {
	dir      string
	mu       sync.Mutex
	migrated bool
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
	return filepath.Join(s.dir, "conversations")
}

func (s *AcpRunStore) conversationDir(conversationID string) (string, error) {
	if err := safeSegment(conversationID); err != nil {
		return "", fmt.Errorf("acp run store: conversation %w", err)
	}
	return filepath.Join(s.conversationsDir(), conversationID+".acp"), nil
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

// Save writes the record as its own JSON file, creating or replacing it
// atomically. Saving the same run ID again replaces that run's file —
// other runs are untouched by construction.
func (s *AcpRunStore) Save(record domain.AcpRunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.migrateLegacyLocked(); err != nil {
		return err
	}
	path, err := s.runPath(record.ConversationID, record.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeJSONAtomic(path, record)
}

// Load returns the record for runID, or (zero, false) if not found.
func (s *AcpRunStore) Load(runID string) (domain.AcpRunRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.migrateLegacyLocked(); err != nil {
		return domain.AcpRunRecord{}, false
	}
	entries, err := os.ReadDir(s.conversationsDir())
	if err != nil {
		return domain.AcpRunRecord{}, false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".acp") {
			if r, ok := readRun(filepath.Join(s.conversationsDir(), e.Name(), runID+".json")); ok {
				return r, true
			}
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

	var out []domain.AcpRunRecord
	if conversationID == "" {
		entries, err := os.ReadDir(s.conversationsDir())
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasSuffix(e.Name(), ".acp") {
				out = append(out, readConversationRuns(filepath.Join(s.conversationsDir(), e.Name()))...)
			}
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

// migrateLegacyLocked imports a pre-existing global acp_runs.jsonl into
// the per-run layout, then renames it to acp_runs.jsonl.imported. It must
// run under s.mu. Failures block further saves rather than letting new
// writes fall back into the shared legacy file; malformed lines and lines
// with unusable IDs are skipped (the .imported copy keeps the originals).
func (s *AcpRunStore) migrateLegacyLocked() error {
	if s.migrated {
		return nil
	}
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
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
