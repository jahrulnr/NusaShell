package conversation

import (
	"context"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	instructionFileName        = "AGENTS.md"
	instructionFilesMax        = 64
	instructionFilesGitTimeout = 3 * time.Second
)

// ListInstructionFiles returns workspace-relative slash paths to AGENTS.md
// files that are not ignored. Git repositories use `git ls-files
// --exclude-standard` (honors .gitignore, .git/info/exclude, and the
// global excludes file). That is the portable equivalent of a filtered
// find: git already knows the ignore rules on Linux, macOS, and Windows.
//
// Plain `find -name AGENTS.md` is not used. It does not read .gitignore
// and would surface node_modules, vendor, and .experimental copies.
//
// Workspaces that are not git checkouts fall back to a WalkDir that skips
// well-known noisy directories and simple directory names from the root
// .gitignore.
func ListInstructionFiles(workspace string) []string {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return nil
	}
	abs, err := filepath.Abs(ws)
	if err != nil {
		return nil
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil
	}
	var files []string
	if gitFiles, ok := listInstructionFilesGit(abs); ok {
		files = gitFiles
	} else {
		files = listInstructionFilesWalk(abs)
	}
	return normalizeInstructionFiles(files)
}

func listInstructionFilesGit(ws string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), instructionFilesGitTimeout)
	defer cancel()
	if err := gitCommand(ctx, ws, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return nil, false
	}
	topOut, err := gitCommand(ctx, ws, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, false
	}
	top := strings.TrimSpace(string(topOut))
	if top == "" {
		return nil, false
	}
	// The git toplevel can spell the same directory differently than the
	// caller's path: macOS resolves /var → /private/var and Windows
	// canonicalizes short names, so workspace membership is decided on
	// resolved paths (EvalSymlinks both sides; case-insensitive on
	// Windows).
	root, err := filepath.EvalSymlinks(top)
	if err != nil {
		root = top
	}
	wsRoot, err := filepath.EvalSymlinks(ws)
	if err != nil {
		wsRoot = ws
	}
	out, err := gitCommand(ctx, root, "ls-files", "-z",
		"--cached", "--others", "--exclude-standard", "--",
		instructionFileName, "**/"+instructionFileName).Output()
	if err != nil {
		return nil, false
	}
	var files []string
	total := 0
	for _, rel := range strings.Split(string(out), "\x00") {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		total++
		abs := filepath.Join(root, filepath.FromSlash(rel))
		wsRel := wsRelativeTo(wsRoot, abs)
		if wsRel == "" || filepath.Base(wsRel) != instructionFileName {
			continue
		}
		files = append(files, wsRel)
	}
	if total > 0 && len(files) == 0 {
		// Every ls-files entry landed outside the workspace: a
		// path-spelling mismatch must never masquerade as an empty
		// catalog, so fall back to the walker.
		return nil, false
	}
	return files, true
}

func gitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Stderr = io.Discard
	return cmd
}

// wsRelativeTo returns the slash-separated path of abs relative to dir when
// abs lies inside dir, and "" otherwise. Windows paths are compared
// case-insensitively because filepath.Rel itself is case-sensitive and a
// casing difference would fake a "../" escape.
func wsRelativeTo(dir, abs string) string {
	d, f := dir, abs
	if runtime.GOOS == "windows" {
		d = strings.ToLower(d)
		f = strings.ToLower(f)
	}
	rel, err := filepath.Rel(d, f)
	if err != nil {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func listInstructionFilesWalk(ws string) []string {
	ignore := loadInstructionIgnore(ws)
	var files []string
	_ = filepath.WalkDir(ws, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(files) >= instructionFilesMax {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path == ws {
				return nil
			}
			if shouldSkipInstructionDir(ws, path, d.Name(), ignore) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != instructionFileName {
			return nil
		}
		rel, err := filepath.Rel(ws, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files
}

type instructionIgnore struct {
	names map[string]struct{}
	root  map[string]struct{}
}

func loadInstructionIgnore(ws string) instructionIgnore {
	ignore := instructionIgnore{
		names: map[string]struct{}{
			"node_modules": {},
			"vendor":       {},
			"dist":         {},
			"build":        {},
			"target":       {},
			"__pycache__":  {},
			"venv":         {},
			"coverage":     {},
		},
		root: map[string]struct{}{},
	}
	data, err := os.ReadFile(filepath.Join(ws, ".gitignore"))
	if err != nil {
		return ignore
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		if strings.ContainsAny(line, "*?[]") {
			continue
		}
		rooted := strings.HasPrefix(line, "/")
		line = strings.TrimPrefix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if line == "" || strings.Contains(line, "/") {
			continue
		}
		if rooted {
			ignore.root[line] = struct{}{}
			continue
		}
		ignore.names[line] = struct{}{}
	}
	return ignore
}

func shouldSkipInstructionDir(ws, path, name string, ignore instructionIgnore) bool {
	if name != "." && strings.HasPrefix(name, ".") {
		return true
	}
	if _, ok := ignore.names[name]; ok {
		return true
	}
	rel, err := filepath.Rel(ws, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	_, ok := ignore.root[rel]
	return ok
}

func normalizeInstructionFiles(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, file := range files {
		file = filepath.ToSlash(strings.TrimSpace(file))
		file = strings.TrimPrefix(file, "./")
		if file == "" || filepath.Base(file) != instructionFileName {
			continue
		}
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		out = append(out, file)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i] == instructionFileName {
			return true
		}
		if out[j] == instructionFileName {
			return false
		}
		return out[i] < out[j]
	})
	if len(out) > instructionFilesMax {
		out = out[:instructionFilesMax]
	}
	return out
}
