package reporecording

import (
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"

	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store keeps browser session recordings under one folder, one directory
// per recording: meta.json, events.jsonl, network.jsonl, summary.txt, and
// artifacts/. Live recorders are tracked so reads see their newest events
// and the retention sweep never removes one in use.
type Store struct {
	root string
	log  *slog.Logger

	mu   sync.Mutex
	live map[string]*Recorder
}

// NewStore opens the recordings folder: recordings left live by a previous
// process are marked interrupted, and ones older than the retention are
// removed.
func NewStore(root string, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	s := &Store{root: root, log: log.With("component", "recordings"), live: map[string]*Recorder{}}
	if err := recoverRecordings(root, time.Now().UnixMilli()); err != nil {
		s.log.Warn("recordings not recovered", "error", err)
	}
	s.Sweep()
	return s
}

// Sweep removes recordings past the retention.
func (s *Store) Sweep() {
	cutoff := time.Now().Add(-recRetention).UnixMilli()
	if err := sweepRecordings(s.root, cutoff, s.active); err != nil {
		s.log.Warn("old recordings not removed", "error", err)
	}
}

// Start opens a recording. mask hides secrets in what is written.
func (s *Store) Start(sessionID, conversationID, profile, env string, mask func(string) string) (drepo.RecordingWriter, error) {
	r, err := newRecorder(s.root, sessionID, conversationID, profile, env, mask, s.log)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.live[r.id] = r
	s.mu.Unlock()
	go func() {
		<-r.Done()
		s.mu.Lock()
		delete(s.live, r.id)
		s.mu.Unlock()
	}()
	return r, nil
}

func (s *Store) active(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.live[id]
	return ok
}

// Get resolves a recording: the live recorder when it runs (its buffered
// tail would otherwise lag), else meta.json on disk.
func (s *Store) Get(id string) (drepo.RecordingReader, error) {
	if !validRecID(id) || s.root == "" {
		return nil, ErrRecordingNotFound
	}
	s.mu.Lock()
	r := s.live[id]
	s.mu.Unlock()
	if r != nil {
		return &Recording{dir: r.dir, live: r}, nil
	}
	meta, err := loadRecMeta(s.root, id)
	if err != nil {
		return nil, ErrRecordingNotFound
	}
	return &Recording{dir: filepath.Join(s.root, id), meta: *meta}, nil
}

// List returns a conversation's recordings ("" for all), newest first,
// live ones current.
func (s *Store) List(conversationID string) []RecMeta {
	out := []RecMeta{}
	index := map[string]int{}
	for _, meta := range listRecMetas(s.root) {
		if conversationID == "" || meta.ConversationID == conversationID {
			index[meta.ID] = len(out)
			out = append(out, meta)
		}
	}
	s.mu.Lock()
	for _, r := range s.live {
		meta := r.Meta()
		if conversationID != "" && meta.ConversationID != conversationID {
			continue
		}
		if i, ok := index[meta.ID]; ok {
			out[i] = meta
		} else {
			out = append(out, meta)
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// Shutdown interrupts the live recordings.
func (s *Store) Shutdown() {
	s.mu.Lock()
	live := make([]*Recorder, 0, len(s.live))
	for _, r := range s.live {
		live = append(live, r)
	}
	s.mu.Unlock()
	for _, r := range live {
		r.Shutdown()
	}
}

func newRecID() string { return "rec_" + strings.ToLower(rand.Text()[:16]) }

// writeFileAtomic writes through a temp file and a rename.
func writeFileAtomic(dir, name string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	dst := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		_ = os.Remove(dst)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// AppendDOMBatch parses one batch of in-page events (a JSON array, or the
// same array inside a JSON string when the binding serializes twice) and
// appends each entry as an L1 "dom" event. The page's own ts becomes the
// event timestamp; seq still records append order. Malformed entries drop.

func (r *Recorder) AppendDOMBatch(payload []byte) {
	var evs []json.RawMessage
	if json.Unmarshal(payload, &evs) != nil {
		var nested string
		if json.Unmarshal(payload, &nested) != nil || json.Unmarshal([]byte(nested), &evs) != nil {
			return
		}
	}
	for _, raw := range evs {
		var head struct {
			T  string `json:"t"`
			TS int64  `json:"ts"`
		}
		if json.Unmarshal(raw, &head) != nil || head.T == "" {
			continue
		}
		r.AppendRaw("dom", head.T, head.TS, raw)
	}
}

func nowMillis() int64 { return time.Now().UnixMilli() }

var _ drepo.Recordings = (*Store)(nil)

// artifactLimit caps an artifact read.
const artifactLimit = 4 << 20

// Artifact reads a file of the recording and guesses its media type.
func (r *Recording) Artifact(name string) ([]byte, string, error) {
	p, err := r.ArtifactPath(name)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, "", ErrRecordingNotFound
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, artifactLimit))
	if err != nil {
		return nil, "", err
	}
	mime := http.DetectContentType(data)
	switch strings.ToLower(filepath.Ext(p)) {
	case ".json", ".jsonl":
		mime = "application/json"
	case ".txt", ".log":
		mime = "text/plain"
	}
	return data, mime, nil
}

// Artifact reads a file of the live recording.
func (r *Recorder) Artifact(name string) ([]byte, string, error) {
	return (&Recording{dir: r.dir, live: r}).Artifact(name)
}

// Purge finalizes every live recorder, then removes the whole store and
// leaves an empty root. Done closes on the recorder's own writer — the
// live map is only a registry, so the wait may run outside s.mu.
func (s *Store) Purge(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	live := make([]*Recorder, 0, len(s.live))
	for _, r := range s.live {
		live = append(live, r)
	}
	s.mu.Unlock()
	for _, r := range live {
		_, _ = r.Stop()
		select {
		case <-r.Done():
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// The Done-watcher goroutine that removes an id from live is
	// scheduled, not awaited — drop the ids here so a List right after
	// Purge cannot see a just-stopped recorder.
	s.mu.Lock()
	for _, r := range live {
		delete(s.live, r.id)
	}
	s.mu.Unlock()
	if err := os.RemoveAll(s.root); err != nil {
		return err
	}
	return os.MkdirAll(s.root, 0o700)
}
