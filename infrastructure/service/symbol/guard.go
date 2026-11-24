// guard.go holds the big-workspace bounds every consumer of a
// SymbolRoot shares: the project-root gate, the filesystem-root OS-dir
// skip, and the hard file/depth/time caps. Without them a conversation
// bound to a home directory or a drive root would walk tens of thousands
// of files every turn and index a map nobody reads.
package symbol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

const (
	// GateFileLimit is the file-count gate for roots without a project
	// marker: a root with no manifest and more than this many indexable
	// files is almost certainly a home dir or worse. The probe stops the
	// moment the count tips past it.
	GateFileLimit = 2000
	// MaxIndexedFiles is the hard cap on files hashed and indexed per
	// root — past it the map keeps the lexical head and says so.
	MaxIndexedFiles = 10000
	// MaxDepth bounds path depth in segments: files deeper than ten
	// parts are almost always generated or vendored trees.
	MaxDepth = 10
	// ProbeTimeout bounds the per-turn file-list walk — the guard's own
	// cost, not the index build's.
	ProbeTimeout = 2 * time.Second
	// IndexTimeout bounds one background index build; on timeout the
	// partial result is kept and a continuation build resumes it. Turns
	// never wait on it — a stale map serves in the meantime.
	IndexTimeout = 30 * time.Second
	// SkipCacheTTL is how long a "not a project root" verdict holds
	// before the root earns a fresh probe — long enough that a skipped
	// home dir costs nothing per turn, short enough to notice a newly
	// created manifest.
	SkipCacheTTL = 10 * time.Minute
)

// ProjectMarkers lists the root-level names whose presence proves a folder
// is a project. The list itself lives with the SymbolRoot port so the
// hydration gate (application/job) reads the same definition without the
// application depending on this adapter.
func ProjectMarkers() []string { return svc.ProjectMarkers() }

// ProjectMarkerGlobs lists the root-level name patterns that prove a
// project — ReadFile cannot glob, so these are matched against the names
// the probe happens to see (root files are not guaranteed early in
// lexical order).
func ProjectMarkerGlobs() []string { return svc.ProjectMarkerGlobs() }

// osRootDirs are skipped only when the indexed root IS a filesystem
// root — a project's own var/ or usr/ subfolder stays indexable. Keys are
// lowercase: Windows dirs match case-insensitively because NTFS is.
var osRootDirs = map[string]bool{
	"proc": true, "sys": true, "dev": true, "run": true, "snap": true,
	"usr": true, "var": true, "opt": true, "boot": true,
	"windows": true, "program files": true, "program files (x86)": true,
	"programdata": true, "$recycle.bin": true,
	"system volume information": true, "recovery": true,
}

// isFilesystemRoot reports whether dir is a filesystem root — "/" on
// POSIX, a drive root or UNC share on Windows. The check is deliberately
// not filepath-bound: a Windows-style root must be recognized even when
// the reporting workspace sits on another platform.
func isFilesystemRoot(dir string) bool {
	if isWindowsRoot(dir) {
		return true
	}
	clean := filepath.Clean(dir)
	vol := filepath.VolumeName(clean) // "C:" / "\\server\share" / "" (unix)
	return strings.TrimPrefix(clean, vol) == string(filepath.Separator)
}

// isWindowsRoot recognizes "X:\" or "X:/" for any drive letter, and a
// bare "\\server\share" (either separator) — without depending on the
// host platform's path parser.
func isWindowsRoot(dir string) bool {
	if len(dir) >= 2 && isDriveLetter(dir[0]) && dir[1] == ':' {
		rest := dir[2:]
		return rest == "" || rest == `\` || rest == "/"
	}
	if len(dir) >= 2 && (dir[0] == '\\' || dir[0] == '/') && (dir[1] == '\\' || dir[1] == '/') {
		parts := strings.FieldsFunc(dir, func(r rune) bool { return r == '\\' || r == '/' })
		return len(parts) == 2
	}
	return false
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// skipAtFsRoot reports whether rel is a top-level OS directory to prune
// when the root is a filesystem root — and only then.
func skipAtFsRoot(rel string, isDir, fsRoot bool) bool {
	if !fsRoot || !isDir || strings.ContainsRune(rel, '/') {
		return false
	}
	return osRootDirs[strings.ToLower(rel)]
}

// SkipReason classifies why rel is not indexable, or "" when it is.
// ShouldIndexWithin is this predicate's boolean form; the reason string
// is what the map footers count by.
func SkipReason(rel string, size, maxBytes int64) string {
	if size > maxBytes || size == 0 {
		return "oversize"
	}
	for part := range strings.SplitSeq(rel, "/") {
		if strings.HasPrefix(part, ".") || skipDirNames[part] {
			return "dir"
		}
	}
	if strings.Count(rel, "/")+1 > MaxDepth {
		return "depth"
	}
	return ""
}

// hasMarker probes the fixed project markers through ReadFile — the
// only port operation that can hit a file without listing a directory. A
// ".git" directory answers through its HEAD; as a worktree file it
// answers directly.
func hasMarker(ctx context.Context, root svc.SymbolRoot) bool {
	for _, name := range ProjectMarkers() {
		if _, err := root.ReadFile(ctx, name); err == nil {
			return true
		}
		if name == ".git" {
			if _, err := root.ReadFile(ctx, ".git/HEAD"); err == nil {
				return true
			}
		}
	}
	return false
}

// markerMatch applies the glob markers to a root-level file name.
func markerMatch(name string) bool {
	for _, g := range ProjectMarkerGlobs() {
		if ok, _ := filepath.Match(g, name); ok {
			return true
		}
	}
	return false
}

// probeResult is what one bounded walk learned about a root.
type probeResult struct {
	hash         string // rel:size digest of the indexable files seen
	indexable    int    // files that passed every filter
	skippedLang  int    // indexable-looking files no language claims
	skippedDepth int    // files past MaxDepth
	marker       bool   // a project marker was found
	truncated    string // "": full walk | "file cap" | "time cap"
}

// probe runs the gate + hash walk in one pass under a timeout: marker
// probes first, then a file-list fold that counts the same files the
// index would see. A marker makes the root indexable regardless of
// count; without one, more than GateFileLimit indexable files fails the
// gate — the probe still stops early so the walk itself stays cheap.
func probe(ctx context.Context, root svc.SymbolRoot, scope svc.SymbolIndexScope, maxBytes int64, timeout time.Duration) (probeResult, error) {
	var pr probeResult
	pr.marker = hasMarker(ctx, root)
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	h := sha256.New()
	err := root.Walk(wctx, func(rel string, size int64) error {
		reason := SkipReason(rel, size, maxBytes)
		switch reason {
		case "depth":
			pr.skippedDepth++
			return nil
		case "":
		default:
			return nil
		}
		if !pr.marker && !strings.ContainsRune(rel, '/') && markerMatch(rel) {
			pr.marker = true
		}
		if scope != nil && !scope.IndexesFile(rel) {
			pr.skippedLang++
			return nil
		}
		if pr.indexable >= MaxIndexedFiles {
			pr.truncated = "file cap"
			return fs.SkipAll
		}
		pr.indexable++
		fmt.Fprintf(h, "%s:%d\n", rel, size)
		if !pr.marker && pr.indexable > GateFileLimit {
			// Gate already failed; nothing more to count or hash.
			return fs.SkipAll
		}
		return nil
	})
	// A walk aborted by the deadline — or one that finished just past it —
	// is a time-cap truncation, not a walk failure.
	if err != nil && err != fs.SkipAll && wctx.Err() == nil {
		return pr, err
	}
	if wctx.Err() != nil {
		pr.truncated = "time cap"
	}
	pr.hash = hex.EncodeToString(h.Sum(nil)[:16])
	return pr, nil
}

// boundedRoot wraps a root so the index build sees exactly the file set
// the probe hashed — same filters, same file cap — keeping map and
// change-detector in agreement.
type boundedRoot struct {
	svc.SymbolRoot
	scope    svc.SymbolIndexScope
	maxBytes int64
}

func (r boundedRoot) Walk(ctx context.Context, visit func(rel string, size int64) error) error {
	seen := 0
	return r.SymbolRoot.Walk(ctx, func(rel string, size int64) error {
		if SkipReason(rel, size, r.maxBytes) != "" {
			return nil
		}
		if r.scope != nil && !r.scope.IndexesFile(rel) {
			return nil
		}
		if seen >= MaxIndexedFiles {
			return fs.SkipAll
		}
		seen++
		return visit(rel, size)
	})
}

// OrderFiles sorts an index work list breadth-first: shallower paths
// come before deeper ones, and inside a depth the files round-robin
// across directories — a few files per dir before any dir's tail. Pure
// lexical order makes a time-capped build stop inside the first big
// directory; this order means a truncated map still covers every
// top-level area. Deterministic, which the index contract requires.
func OrderFiles(files []string) {
	byDepth := map[int][]string{}
	maxDepth := 0
	for _, f := range files {
		d := strings.Count(f, "/")
		byDepth[d] = append(byDepth[d], f)
		if d > maxDepth {
			maxDepth = d
		}
	}
	out := files[:0]
	for depth := 0; depth <= maxDepth; depth++ {
		bucket := byDepth[depth]
		if len(bucket) == 0 {
			continue
		}
		// Bucket arrives in lexical order, so each dir's queue is sorted.
		dirs := map[string][]string{}
		var dirOrder []string
		for _, f := range bucket {
			parent := f[:strings.LastIndexByte(f, '/')+1]
			if _, ok := dirs[parent]; !ok {
				dirOrder = append(dirOrder, parent)
			}
			dirs[parent] = append(dirs[parent], f)
		}
		sort.Strings(dirOrder)
		for len(dirOrder) > 0 {
			next := dirOrder[:0]
			for _, d := range dirOrder {
				q := dirs[d]
				out = append(out, q[0])
				if q = q[1:]; len(q) > 0 {
					dirs[d] = q
					next = append(next, d)
				}
			}
			dirOrder = next
		}
	}
}
