// Package symbolmap persists rendered symbol indexes under
// <storage>/symbols/, one JSON file per workspace root keyed by the
// root path's hash. A record holds the file-list hash that produced the
// index plus the extracted symbols, so a restart (or a second
// conversation on the same root) serves the map without re-parsing.
package symbolmap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Store is a directory of CachedIndex files. It is safe for concurrent
// use: writes are whole-file atomic replacements.
type Store struct {
	dir string
}

// New returns the store rooted at dir, creating it on first save.
func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) file(rootKey string) string {
	sum := sha256.Sum256([]byte(rootKey))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:])+".json")
}

// Load returns the cached index for rootKey — found=false on a missing
// or unreadable file, which callers treat as a cold cache.
func (s *Store) Load(rootKey string) (svc.CachedIndex, bool) {
	body, err := os.ReadFile(s.file(rootKey))
	if err != nil {
		return svc.CachedIndex{}, false
	}
	var idx svc.CachedIndex
	if err := json.Unmarshal(body, &idx); err != nil || idx.FileHash == "" {
		return svc.CachedIndex{}, false
	}
	return idx, true
}

// Save writes idx atomically: a temp file in the same directory renamed
// over the target, so a crash mid-write never leaves a torn cache.
func (s *Store) Save(rootKey string, idx svc.CachedIndex) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".symbols-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.file(rootKey)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
