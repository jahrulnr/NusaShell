package tools

import (
	"path/filepath"
	"sort"
	"sync"
)

// filePathLocks serializes in-process read-modify-write file tools that
// target the same path (notably parallel file_patch in one agent round).
// Without this, concurrent patches race on ReadFile → mutate → rename and
// last-writer-wins silently drops earlier edits.
//
// Keys are filepath.Clean paths. Distinct strings that resolve to the same
// inode (symlinks, relative vs absolute) do not share a lock — out of scope.
var filePathLocks sync.Map // string → *sync.Mutex

// lockFilePaths acquires the per-path mutex for every path (deduped by
// filepath.Clean and locked in sorted order) and returns a single unlock
// function that releases them in reverse order. Sorting the keys makes the
// acquisition order independent of caller argument order, so two operations
// that swap the same pair of paths (e.g. file_move(a,b) and file_move(b,a))
// cannot deadlock. Multi-path file tools (file_move, file_copy) lock both
// source and destination so a parallel same-path patch/delete cannot race
// the read-then-mutate section.
func lockFilePaths(paths ...string) (unlock func()) {
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(paths))
	for _, p := range paths {
		k := filepath.Clean(p)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	unlocks := make([]func(), 0, len(keys))
	for _, k := range keys {
		v, _ := filePathLocks.LoadOrStore(k, &sync.Mutex{})
		mu := v.(*sync.Mutex)
		mu.Lock()
		unlocks = append(unlocks, mu.Unlock)
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}

// lockFilePath is a thin wrapper over lockFilePaths for the single-path
// call sites (file_write, file_patch). Existing callers and tests stay valid.
func lockFilePath(path string) (unlock func()) {
	return lockFilePaths(path)
}
