package journal

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nusashell/domain"
)

// JSONL event schema (one object per line, lowercase-camel field names):
//
//	{"type":"change","ts":"2006-01-02T15:04:05.999999999Z","eventId":"...","runId":"...","tool":"...","change":{...domain.FileChange...}}
//	{"type":"unobserved","ts":"2006-01-02T15:04:05.999999999Z","eventId":"...","runId":"...","tool":"..."}

const (
	eventTypeChange     = "change"
	eventTypeUnobserved = "unobserved"
)

type domainFileChange struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Origin     string `json:"origin"`
	BeforeHash string `json:"beforeHash,omitempty"`
	AfterHash  string `json:"afterHash,omitempty"`
	BeforeSize int64  `json:"beforeSize,omitempty"`
	AfterSize  int64  `json:"afterSize,omitempty"`
	EventID    string `json:"eventId,omitempty"`
}

func (c domainFileChange) toDomain() domain.FileChange {
	return domain.FileChange{
		Path:       c.Path,
		Kind:       domain.ChangeKind(c.Kind),
		Origin:     domain.ChangeOrigin(c.Origin),
		BeforeHash: c.BeforeHash,
		AfterHash:  c.AfterHash,
		BeforeSize: c.BeforeSize,
		AfterSize:  c.AfterSize,
		EventID:    c.EventID,
	}
}

func fileChangeFromDomain(c domain.FileChange) domainFileChange {
	return domainFileChange{
		Path:       c.Path,
		Kind:       string(c.Kind),
		Origin:     string(c.Origin),
		BeforeHash: c.BeforeHash,
		AfterHash:  c.AfterHash,
		BeforeSize: c.BeforeSize,
		AfterSize:  c.AfterSize,
		EventID:    c.EventID,
	}
}

type journalEvent struct {
	Type    string            `json:"type"`
	TS      time.Time         `json:"ts"`
	EventID string            `json:"eventId"`
	RunID   string            `json:"runId,omitempty"`
	Tool    string            `json:"tool,omitempty"`
	Change  *domainFileChange `json:"change,omitempty"`
}

type store struct {
	dataDir string
	mu      sync.Mutex
	writers map[string]*sync.Mutex
}

func newStore(dataDir string) *store {
	return &store{
		dataDir: dataDir,
		writers: make(map[string]*sync.Mutex),
	}
}

func (s *store) convMu(conversationID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.writers[conversationID]; ok {
		return m
	}
	m := &sync.Mutex{}
	s.writers[conversationID] = m
	return m
}

func (s *store) sidecarDir(conversationID string) (string, error) {
	return sidecarPath(s.dataDir, conversationID)
}

func (s *store) journalPath(conversationID string) (string, error) {
	dir, err := s.sidecarDir(conversationID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "journal.jsonl"), nil
}

func (s *store) journalGzipPath(conversationID string) (string, error) {
	dir, err := s.sidecarDir(conversationID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "journal.jsonl.gz"), nil
}

func (s *store) append(conversationID string, ev journalEvent) error {
	if ev.TS.IsZero() {
		ev.TS = time.Now().UTC()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	mu := s.convMu(conversationID)
	mu.Lock()
	defer mu.Unlock()

	dir, err := ensureSidecarDir(s.dataDir, conversationID)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "journal.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// readAll returns the full event history: archived gzip members first
// (oldest), then the live JSONL tail. Crash-duplicate change events
// (same eventId + path) are dropped so a replayed append never
// double-counts an effect.
func (s *store) readAll(conversationID string) ([]journalEvent, error) {
	var out []journalEvent
	gzPath, err := s.journalGzipPath(conversationID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(gzPath); err == nil {
		gzEvents, err := readJSONLGzip(gzPath)
		if err != nil {
			return nil, err
		}
		out = append(out, gzEvents...)
	}
	path, err := s.journalPath(conversationID)
	if err != nil {
		return nil, err
	}
	tail, err := readJSONLFile(path)
	if err != nil {
		return nil, err
	}
	out = append(out, tail...)
	return dedupeEvents(out), nil
}

// dedupeEvents drops a change event when an earlier event with the same
// eventId and path already exists. Unobserved events are never deduplicated:
// each mcp_call is a distinct gap even when tool call IDs repeat.
func dedupeEvents(events []journalEvent) []journalEvent {
	seen := make(map[string]struct{}, len(events))
	out := make([]journalEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type == eventTypeChange && ev.Change != nil {
			key := ev.EventID + "\x00" + ev.Change.Path
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, ev)
	}
	return out
}

func readJSONLFile(path string) ([]journalEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return parseJSONL(f)
}

// readJSONLGzip reads every gzip member in the file. Archive appends one
// member per call, so a long-lived sidecar contains many concatenated
// members; gzip.Reader reaches the next member via Reset after io.EOF.
func readJSONLGzip(path string) ([]journalEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	var out []journalEvent
	for {
		events, err := parseJSONL(gz)
		out = append(out, events...)
		if err != nil {
			return out, err
		}
		if err := gz.Reset(f); err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}

func parseJSONL(r io.Reader) ([]journalEvent, error) {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	var out []journalEvent
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev journalEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// archive compresses the live journal.jsonl as a new gzip member appended
// to journal.jsonl.gz (multi-member), then removes the original. Events
// archived by earlier turns stay readable through readAll.
func (s *store) archive(conversationID string) error {
	mu := s.convMu(conversationID)
	mu.Lock()
	defer mu.Unlock()

	path, err := s.journalPath(conversationID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	gzPath, err := s.journalGzipPath(conversationID)
	if err != nil {
		return err
	}

	src, err := os.Open(path)
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(gzPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		src.Close()
		return err
	}
	gw := gzip.NewWriter(dst)
	_, copyErr := io.Copy(gw, src)
	// Close the source before removing it: the copy is complete here, and
	// Windows refuses to delete a file that is still open.
	src.Close()
	if copyErr != nil {
		gw.Close()
		dst.Close()
		return copyErr
	}
	if err := gw.Close(); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Remove(path)
}

// remove deletes the entire journal sidecar (events + blobs) for a
// conversation. Called when the conversation itself is deleted.
func (s *store) remove(conversationID string) error {
	mu := s.convMu(conversationID)
	mu.Lock()
	defer mu.Unlock()

	dir, err := s.sidecarDir(conversationID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.writers, conversationID)
	s.mu.Unlock()
	return nil
}

var errInvalidPathSegment = errors.New("id contains characters unsafe for use as a file name")

func safeSegment(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || strings.ContainsRune(id, 0) {
		return fmt.Errorf("%q: %w", id, errInvalidPathSegment)
	}
	return nil
}
