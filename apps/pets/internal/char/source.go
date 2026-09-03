package char

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Source reads a char's files whether it is an unpacked folder or a .zip.
// A zip is opened in memory (no temp extraction); a folder is read from disk.
// Both expose the same Names/Has/Read interface.
type Source struct {
	path     string
	isFolder bool
	zip      *zip.ReadCloser
	namesSet map[string]bool
}

// OpenSource opens a folder or .zip char source. Close it when done.
func OpenSource(path string) (*Source, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("char source: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return &Source{path: path, isFolder: true}, nil
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("char source: open zip %s: %w", path, err)
	}
	names := make(map[string]bool, len(zr.File))
	for _, f := range zr.File {
		names[f.Name] = true
	}
	return &Source{path: path, zip: zr, namesSet: names}, nil
}

// IsFolder reports whether the source is an unpacked folder.
func (s *Source) IsFolder() bool { return s.isFolder }

// Names returns the set of file names (POSIX-relative) in the source.
func (s *Source) Names() []string {
	if s.isFolder {
		var out []string
		_ = filepath.WalkDir(s.path, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(s.path, p)
			if rerr != nil {
				return nil
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		sort.Strings(out)
		return out
	}
	out := make([]string, 0, len(s.namesSet))
	for n := range s.namesSet {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Has reports whether a named file exists in the source.
func (s *Source) Has(name string) bool {
	if s.isFolder {
		_, err := os.Stat(filepath.Join(s.path, name))
		return err == nil
	}
	return s.namesSet[name]
}

// Read returns the bytes of a named file.
func (s *Source) Read(name string) ([]byte, error) {
	if s.isFolder {
		data, err := os.ReadFile(filepath.Join(s.path, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		return data, nil
	}
	if !s.namesSet[name] {
		return nil, fmt.Errorf("read %s: not found", name)
	}
	rc, err := s.zip.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open zip entry %s: %w", name, err)
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Close releases the underlying zip reader.
func (s *Source) Close() error {
	if s.zip != nil {
		return s.zip.Close()
	}
	return nil
}
