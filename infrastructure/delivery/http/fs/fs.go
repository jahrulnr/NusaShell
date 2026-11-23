// Package fs is the WebUI's local-filesystem browse boundary — the
// workspace picker's directory listing (browse, directories only) and the
// side pane's folder view (list, directories and files with sizes). It
// never serves file contents. The default directory is the
// owner's home; navigation ascends to the filesystem root so targets
// outside home (mounted storage, /apps, Windows drives) stay reachable.
// The path must be absolute; every entry returned is a joined child of
// the requested directory — the API never interprets `..` segments or
// serves file contents.
package fs

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// maxEntries bounds one directory listing — picker feeds stay small.
const maxEntries = 1000

type browseDTO struct {
	Path      string     `json:"path"`
	Parent    string     `json:"parent,omitempty"`
	Roots     []rootDTO  `json:"roots"`
	Entries   []entryDTO `json:"entries"`
	Truncated bool       `json:"truncated,omitempty"`
}

type rootDTO struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type entryDTO struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden,omitempty"`
}

// Register mounts this boundary's routes under /api.
func Register(api *gin.RouterGroup, tree drepo.FileTree) {
	api.GET("/fs/browse", func(c *gin.Context) { browse(c, tree) })
	api.GET("/fs/list", func(c *gin.Context) { list(c, tree) })
}

type listDTO struct {
	Path      string     `json:"path"`
	Parent    string     `json:"parent,omitempty"`
	Dirs      []entryDTO `json:"dirs"`
	Files     []fileDTO  `json:"files"`
	Truncated bool       `json:"truncated,omitempty"`
}

type fileDTO struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// list answers one directory's folders and files, each sorted by name.
func list(c *gin.Context, tree drepo.FileTree) {
	dir := strings.TrimSpace(c.Query("path"))
	if !filepath.IsAbs(dir) {
		transport.WriteProblem(c, 400, "invalid_argument", "path must be absolute")
		return
	}
	dir = filepath.Clean(dir)
	ents, err := tree.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			transport.WriteProblem(c, 404, "not_found", "no such directory")
			return
		}
		transport.WriteProblem(c, 403, "forbidden_origin", "directory is not readable")
		return
	}
	out := listDTO{Path: dir, Dirs: []entryDTO{}, Files: []fileDTO{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = parent
	}
	for _, e := range ents {
		if len(out.Dirs)+len(out.Files) >= maxEntries {
			out.Truncated = true
			break
		}
		path := filepath.Join(dir, e.Name())
		info, err := tree.Stat(path) // follows symlinks
		if err != nil {
			continue
		}
		switch {
		case info.IsDir():
			out.Dirs = append(out.Dirs, entryDTO{Name: e.Name(), Path: path, Hidden: strings.HasPrefix(e.Name(), ".")})
		case info.Mode().IsRegular():
			out.Files = append(out.Files, fileDTO{Name: e.Name(), Path: path, Size: info.Size()})
		}
	}
	sort.Slice(out.Dirs, func(i, j int) bool { return strings.ToLower(out.Dirs[i].Name) < strings.ToLower(out.Dirs[j].Name) })
	sort.Slice(out.Files, func(i, j int) bool { return strings.ToLower(out.Files[i].Name) < strings.ToLower(out.Files[j].Name) })
	c.JSON(http.StatusOK, out)
}

func browse(c *gin.Context, tree drepo.FileTree) {
	dir := strings.TrimSpace(c.Query("path"))
	if dir == "" {
		home, err := tree.HomeDir()
		if err != nil {
			transport.WriteProblem(c, 500, "internal", "could not resolve the home directory")
			return
		}
		dir = home
	}
	if !filepath.IsAbs(dir) {
		transport.WriteProblem(c, 400, "invalid_argument", "path must be absolute")
		return
	}
	dir = filepath.Clean(dir)
	info, err := tree.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			transport.WriteProblem(c, 403, "forbidden_origin", "directory is not readable")
			return
		}
		transport.WriteProblem(c, 404, "not_found", "no such directory")
		return
	}
	if !info.IsDir() {
		transport.WriteProblem(c, 404, "not_found", "no such directory")
		return
	}
	ents, err := tree.ReadDir(dir)
	if err != nil {
		// The contract's closed code set has no separate "unreadable
		// directory" member; 403's catalog code is forbidden_origin.
		transport.WriteProblem(c, 403, "forbidden_origin", "directory is not readable")
		return
	}
	out := browseDTO{Path: dir, Roots: roots(tree), Entries: make([]entryDTO, 0)}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = parent
	}
	truncated := false
	for _, e := range ents {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue // picker feeds directories; symlinks get statted below
		}
		if e.Type()&os.ModeSymlink != 0 {
			// A symlink counts only if it resolves to a directory — the
			// picker navigates real trees.
			fi, err := tree.Stat(filepath.Join(dir, e.Name()))
			if err != nil || !fi.IsDir() {
				continue
			}
		}
		if len(out.Entries) >= maxEntries {
			truncated = true
			break
		}
		out.Entries = append(out.Entries, entryDTO{
			Name:   e.Name(),
			Path:   filepath.Join(dir, e.Name()),
			Hidden: strings.HasPrefix(e.Name(), "."),
		})
	}
	out.Truncated = truncated
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
	c.JSON(http.StatusOK, out)
}

// roots lists the navigable filesystem roots — "/" on unix, the present
// drive letters on Windows — so the picker can jump to a tree outside
// the current directory's ancestry.
func roots(tree drepo.FileTree) []rootDTO {
	if runtime.GOOS != "windows" {
		return []rootDTO{{Name: "/", Path: "/"}}
	}
	var out []rootDTO
	for d := 'A'; d <= 'Z'; d++ {
		p := string(d) + `:\`
		if _, err := tree.Stat(p); err == nil {
			out = append(out, rootDTO{Name: string(d) + ":", Path: p})
		}
	}
	return out
}
