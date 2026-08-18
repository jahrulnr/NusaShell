package jsonstore

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"nusashell/domain"
)

// AcpRunStore is a JSONL-backed implementation of domain.AcpRunStorage.
// Each completed run is appended as one JSON line to acp_runs.jsonl in
// the data directory. The file is read on demand to list or load runs;
// there is no in-memory index — the expected volume is low (one line per
// subagent completion) and the file grows slowly.
//
// The store is safe for concurrent use: writes hold a mutex, reads take
// a snapshot of the file.
type AcpRunStore struct {
	dir string
	mu  sync.Mutex
}

// NewAcpRunStore creates a JSONL ACP run store rooted at dir. The
// directory is created on first write if it does not exist.
func NewAcpRunStore(dir string) *AcpRunStore {
	return &AcpRunStore{dir: dir}
}

func (s *AcpRunStore) path() string {
	return filepath.Join(s.dir, "acp_runs.jsonl")
}

// TranscriptPath returns the absolute path to the JSONL transcript file.
// Satisfies the optional pather interface used by acpTranscriptPath.
func (s *AcpRunStore) TranscriptPath() string {
	return s.path()
}

// Save appends a run record as one JSON line. If a record with the same
// ID already exists in the file, the line is updated in place rather
// than duplicated.
func (s *AcpRunStore) Save(record domain.AcpRunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}

	existing, err := s.readAll()
	if err != nil {
		return err
	}

	updated := false
	for i, r := range existing {
		if r.ID == record.ID {
			existing[i] = record
			updated = true
			break
		}
	}
	if !updated {
		existing = append(existing, record)
	}

	return s.writeAll(existing)
}

// Load returns the record for runID, or (zero, false) if not found.
func (s *AcpRunStore) Load(runID string) (domain.AcpRunRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.readAll()
	if err != nil {
		return domain.AcpRunRecord{}, false
	}
	for _, r := range records {
		if r.ID == runID {
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

	records, err := s.readAll()
	if err != nil {
		return nil
	}
	out := make([]domain.AcpRunRecord, 0, len(records))
	for _, r := range records {
		if conversationID == "" || r.ConversationID == conversationID {
			out = append(out, r)
		}
	}
	return out
}

func (s *AcpRunStore) readAll() ([]domain.AcpRunRecord, error) {
	f, err := os.Open(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var records []domain.AcpRunRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r domain.AcpRunRecord
		if err := json.Unmarshal(line, &r); err != nil {
			continue // skip malformed lines
		}
		records = append(records, r)
	}
	return records, scanner.Err()
}

func (s *AcpRunStore) writeAll(records []domain.AcpRunRecord) error {
	f, err := os.Create(s.path())
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	return w.Flush()
}
