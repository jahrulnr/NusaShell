// Package mediadescription stores read_media's descriptions and transcripts
// under <storage>/media-descriptions/, one directory per conversation. Both
// are text lifted from a file, so the room is the namespace: nothing here ever
// reads across one. Entries are whole files named by the caller's opaque key;
// the port's contract lives in service.MediaDescriptions.
package mediadescription

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// DefaultLimit bounds one conversation's stored descriptions; the trim drops
// the least-recently-used entries first.
const DefaultLimit int64 = 64 << 20

// Store is a directory of cached descriptions. Every write is atomic
// (temp + rename), so a concurrent reader never sees a half-written entry.
// An empty directory disables it: Get misses and Put does nothing, which is
// the answer a build without storage gets.
type Store struct {
	dir   string
	limit int64
	now   func() time.Time
}

// New returns the store rooted at dir. An empty dir is a disabled store.
func New(dir string) *Store {
	return &Store{dir: dir, limit: DefaultLimit, now: time.Now}
}

var _ dservice.MediaDescriptions = (*Store)(nil)

// WithLimit sets the per-room budget; a non-positive limit keeps the default.
func (s *Store) WithLimit(limit int64) *Store {
	if limit > 0 {
		s.limit = limit
	}
	return s
}

// path is one room's entry. The conversation id is validated by its caller (a
// valid domain id has no separators), and the key is hex, so neither can climb
// out of the directory.
func (s *Store) path(convID, key string) string {
	return filepath.Join(s.dir, convID, key+".txt")
}

// Get returns the stored description and marks it as used. A miss is a miss: an
// unreadable or corrupt entry never fails the call.
func (s *Store) Get(convID, key string) (string, bool, error) {
	if s.dir == "" || convID == "" || !safePart(convID) || !safePart(key) {
		return "", false, nil
	}
	path := s.path(convID, key)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		// An unreadable entry is a cold cache, not a failed description: the
		// reader describes the file again.
		return "", false, err
	}
	now := s.now()
	_ = os.Chtimes(path, now, now)
	return string(raw), true, nil
}

// Put stores one description and trims the room's directory back to its budget.
func (s *Store) Put(convID, key, text string) error {
	if s.dir == "" || convID == "" {
		return nil
	}
	if !safePart(convID) || !safePart(key) {
		return nil // an id or key shaped like a path names no entry here
	}
	dir := filepath.Join(s.dir, convID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".description-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.path(convID, key)); err != nil {
		os.Remove(name)
		return err
	}
	s.trim(dir)
	return nil
}

// Purge forgets one room's descriptions.
func (s *Store) Purge(convID string) error {
	if s.dir == "" || convID == "" || !safePart(convID) {
		return nil
	}
	return os.RemoveAll(filepath.Join(s.dir, convID))
}

// PurgeAll forgets every room's descriptions and leaves the root in place.
func (s *Store) PurgeAll() error {
	if s.dir == "" {
		return nil
	}
	if err := os.RemoveAll(s.dir); err != nil {
		return err
	}
	return os.MkdirAll(s.dir, 0o700)
}

// trim drops the room's oldest-used entries until the directory fits the
// budget. A trim that loses a race with another write costs one more trim,
// never correctness.
func (s *Store) trim(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type entry struct {
		path    string
		size    int64
		modTime time.Time
	}
	files := make([]entry, 0, len(entries))
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, entry{path: filepath.Join(dir, e.Name()), size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
	}
	if total <= s.limit {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for _, f := range files {
		if total <= s.limit {
			return
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size
		}
	}
}

// safePart reports a name that stays one path element: a conversation id or key
// that carries a separator would address another room's directory or the root.
func safePart(v string) bool {
	return v != "" && v != "." && v != ".." && !strings.ContainsAny(v, `/\`) && !strings.ContainsRune(v, 0)
}
