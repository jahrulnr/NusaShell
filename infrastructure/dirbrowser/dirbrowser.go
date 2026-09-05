// Package dirbrowser reads the host filesystem for the in-app workspace
// folder picker. It lists subdirectories only, so the browser walks the
// server's folders without transferring file metadata it does not render.
package dirbrowser

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"nusashell/application"
	"nusashell/contracts"
)

// maxEntries caps one listing so a huge directory cannot flood the wire or
// the picker list. Truncated is reported so the UI can say the list is cut.
const maxEntries = 1000

// OS is the real-host adapter. It resolves an empty path to the user home
// directory and rejects relative paths before touching the filesystem.
type OS struct{}

func (OS) ListDirs(_ context.Context, path string) (application.DirListing, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return application.DirListing{}, err
		}
		path = home
	}
	if !filepath.IsAbs(path) {
		return application.DirListing{}, errors.New("workspace path must be absolute")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return application.DirListing{}, err
	}
	dirs := make([]contracts.WorkspaceDirEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirs = append(dirs, contracts.WorkspaceDirEntry{
			Name: entry.Name(),
			Path: filepath.Join(path, entry.Name()),
		})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	truncated := false
	if len(dirs) > maxEntries {
		dirs = dirs[:maxEntries]
		truncated = true
	}
	return application.DirListing{
		Path:      path,
		Parent:    filepath.Dir(path),
		Entries:   dirs,
		Truncated: truncated,
	}, nil
}

func (OS) EnsureDir(_ context.Context, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fs.ErrInvalid
	}
	return nil
}
