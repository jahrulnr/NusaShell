package repoworkspace

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// dependencyDirs are left out of a walk Git cannot steer (no worktree,
// or no git): installed dependencies and tool caches, never what anyone
// means by the workspace's files.
var dependencyDirs = map[string]bool{
	"node_modules": true, "__pycache__": true, ".venv": true, "venv": true, ".tox": true,
	".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true, ".next": true, ".nuxt": true,
	".gradle": true, ".cache": true,
}

// Entries walks dir, leaving out what Git ignores when dir is inside a
// worktree. Git decides precedence, nested ignore files and the index; one
// check-ignore process answers the whole walk. Outside a worktree the
// usual dependency and cache folders are left out instead.
func (Local) Entries(ctx context.Context, dir string, visit func(rel string, isDir bool) error) error {
	ignore := openGitIgnore(ctx, dir)
	if ignore != nil {
		defer ignore.close()
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || path == dir {
			return nil // an unreadable entry is left out, not fatal
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		isDir := d.IsDir()
		if d.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(path)
			if err != nil {
				return nil
			}
			isDir = info.IsDir()
			if !isDir && !info.Mode().IsRegular() {
				return nil
			}
		} else if !isDir && !d.Type().IsRegular() {
			return nil
		}
		if ignore == nil && d.IsDir() && dependencyDirs[d.Name()] {
			return fs.SkipDir
		}
		if ignore != nil && ignore.ignored(rel, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		return visit(rel, isDir)
	})
}

type gitIgnore struct {
	cmd         *exec.Cmd
	in          io.WriteCloser
	out         *bufio.Reader
	tracked     map[string]bool
	trackedDirs map[string]bool
	broken      bool
}

// openGitIgnore is nil outside a worktree or without git.
func openGitIgnore(ctx context.Context, root string) *gitIgnore {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	data, err := exec.CommandContext(ctx, git, "-C", root, "ls-files", "--cached", "-z", "--", ".").Output()
	if err != nil {
		return nil
	}
	g := &gitIgnore{tracked: map[string]bool{}, trackedDirs: map[string]bool{}}
	for name := range bytes.SplitSeq(data, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		p := filepath.Clean(filepath.FromSlash(string(name)))
		g.tracked[p] = true
		for dir := filepath.Dir(p); dir != "." && dir != ""; dir = filepath.Dir(dir) {
			g.trackedDirs[dir] = true
		}
	}
	g.cmd = exec.CommandContext(ctx, git, "-C", root, "check-ignore", "--stdin", "-z", "--verbose", "--non-matching")
	g.cmd.Env = append(os.Environ(), "GIT_FLUSH=1")
	if g.in, err = g.cmd.StdinPipe(); err != nil {
		return nil
	}
	stdout, err := g.cmd.StdoutPipe()
	if err != nil {
		_ = g.in.Close()
		return nil
	}
	g.out = bufio.NewReader(stdout)
	if err := g.cmd.Start(); err != nil {
		_ = g.in.Close()
		return nil
	}
	return g
}

// ignored asks git about one path. Tracked files and directories holding
// them are never ignored; once git stops answering, nothing more is.
func (g *gitIgnore) ignored(rel string, isDir bool) bool {
	if g.tracked[rel] || (isDir && g.trackedDirs[rel]) || g.broken {
		return false
	}
	path := filepath.ToSlash(rel)
	if isDir {
		path += "/"
	}
	if _, err := io.WriteString(g.in, path+"\x00"); err != nil {
		g.broken = true
		return false
	}
	// --verbose --non-matching answers four NUL-terminated fields:
	// source, line number, pattern, path. An empty source is no match.
	var fields [4]string
	for i := range fields {
		v, err := g.out.ReadString(0)
		if err != nil {
			g.broken = true
			return false
		}
		fields[i] = strings.TrimSuffix(v, "\x00")
	}
	return fields[0] != "" && !strings.HasPrefix(fields[2], "!")
}

func (g *gitIgnore) close() {
	_ = g.in.Close()
	_ = g.cmd.Wait()
}
