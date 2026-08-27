package journal

import (
	"os"
	"path/filepath"
	"sort"
)

const maxListingEntries = 50000

// ignoredDirs are directory names skipped by the workspace walker (spec §21).
// Only workspace metadata and known transient caches are ignored; generated
// artifacts are NOT ignored because they may be real agent effects.
var ignoredDirs = map[string]struct{}{
	".git":          {},
	".hg":           {},
	".svn":          {},
	".nusashell":    {},
	".agent":        {},
	"node_modules":  {},
	"__pycache__":   {},
	".cache":        {},
	".tmp":          {},
	".venv":         {},
	".pytest_cache": {},
	".mypy_cache":   {},
	".tox":          {},
	".ruff_cache":   {},
}

func isIgnoredDir(name string) bool {
	_, ok := ignoredDirs[name]
	return ok
}

type fileMeta struct {
	Size    int64
	ModTime int64
}

func snapshotDir(root string) (map[string]fileMeta, error) {
	out := make(map[string]fileMeta)
	if root == "" {
		return out, nil
	}
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if count >= maxListingEntries {
			return filepath.SkipAll
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			if isIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		out[rel] = fileMeta{
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
		}
		count++
		return nil
	})
	return out, err
}

func diffListing(before, after map[string]fileMeta) (added, modified, deleted []string) {
	seen := make(map[string]struct{})
	for p, meta := range after {
		seen[p] = struct{}{}
		prev, ok := before[p]
		if !ok {
			added = append(added, p)
			continue
		}
		if prev.Size != meta.Size || prev.ModTime != meta.ModTime {
			modified = append(modified, p)
		}
	}
	for p := range before {
		if _, ok := seen[p]; !ok {
			deleted = append(deleted, p)
		}
	}
	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)
	return added, modified, deleted
}

func absPath(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}
