package journal

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"nusashell/application"
	"nusashell/domain"
)

// Journal implements application.ChangeJournal.
type Journal struct {
	dataDir string
	store   *store

	mu        sync.Mutex
	hashCache map[string]cacheEntry
}

// cacheEntry memoizes a content hash keyed by (size, mtime) so opaque
// mutations only re-hash files whose metadata changed (spec §23 Level 1/2).
type cacheEntry struct {
	size    int64
	modTime int64
	hash    string
}

// New creates a Journal rooted at the NusaShell data directory. Journal
// sidecars are created lazily under <dataDir>/conversations/ on first use.
func New(dataDir string) *Journal {
	return &Journal{
		dataDir:   dataDir,
		store:     newStore(dataDir),
		hashCache: make(map[string]cacheEntry),
	}
}

var _ application.ChangeJournal = (*Journal)(nil)

type pathTrack struct {
	baselineHash  string
	lastAfterHash string
	lastTouchTS   time.Time
	touched       bool
}

type replayState struct {
	changes         []domain.FileChange
	paths           map[string]*pathTrack
	unobservedTimes []time.Time
}

func replayEvents(events []journalEvent) replayState {
	rs := replayState{paths: make(map[string]*pathTrack)}
	for _, ev := range events {
		switch ev.Type {
		case eventTypeUnobserved:
			rs.unobservedTimes = append(rs.unobservedTimes, ev.TS)
		case eventTypeChange:
			if ev.Change == nil {
				continue
			}
			c := ev.Change.toDomain()
			rs.changes = append(rs.changes, c)
			pt := rs.paths[c.Path]
			if pt == nil {
				pt = &pathTrack{}
				rs.paths[c.Path] = pt
			}
			if !pt.touched {
				if c.BeforeHash != "" {
					pt.baselineHash = c.BeforeHash
				} else if c.AfterHash != "" {
					pt.baselineHash = c.AfterHash
				}
				pt.touched = true
			}
			pt.lastAfterHash = c.AfterHash
			pt.lastTouchTS = ev.TS
		}
	}
	return rs
}

func (j *Journal) loadReplay(conversationID string) replayState {
	events, err := j.store.readAll(conversationID)
	if err != nil {
		slog.Warn("journal: read failed", "conversation", conversationID, "err", err)
		return replayState{paths: make(map[string]*pathTrack)}
	}
	return replayEvents(events)
}

type fileSnap struct {
	exists bool
	hash   string
	size   int64
}

// snapPath captures the state of one path, reusing a cached content hash
// when the file's size and mtime are unchanged since the last hash.
func (j *Journal) snapPath(path string) fileSnap {
	info, err := os.Stat(path)
	if err != nil {
		return fileSnap{}
	}
	if info.IsDir() {
		return fileSnap{exists: true, size: info.Size()}
	}
	size := info.Size()
	modTime := info.ModTime().UnixNano()

	j.mu.Lock()
	entry, ok := j.hashCache[path]
	j.mu.Unlock()
	if ok && entry.size == size && entry.modTime == modTime {
		return fileSnap{exists: true, hash: entry.hash, size: size}
	}

	h, sz, err := hashFile(path)
	if err != nil {
		return fileSnap{exists: true, size: info.Size()}
	}
	j.mu.Lock()
	j.hashCache[path] = cacheEntry{size: size, modTime: modTime, hash: h}
	j.mu.Unlock()
	return fileSnap{exists: true, hash: h, size: sz}
}

func (j *Journal) resolveOrigin(rs replayState, path, preHash string, preExists bool) domain.ChangeOrigin {
	pt := rs.paths[path]
	if pt == nil || !pt.touched {
		return domain.OriginAgent
	}
	expected := pt.lastAfterHash
	mismatch := (!preExists && expected != "") || (preExists && preHash != expected)
	if !mismatch {
		return domain.OriginAgent
	}
	for _, uts := range rs.unobservedTimes {
		if uts.After(pt.lastTouchTS) {
			return domain.OriginUnobserved
		}
	}
	return domain.OriginExternal
}

func changeKind(preExists, postExists bool) domain.ChangeKind {
	switch {
	case !preExists && postExists:
		return domain.ChangeAdded
	case preExists && !postExists:
		return domain.ChangeDeleted
	default:
		return domain.ChangeModified
	}
}

// noOpChange reports whether the filesystem effect is empty (spec §14: the
// filesystem is authoritative — a write of identical content, a failed
// no-effect command, or a metadata-only touch is not a mutation).
func noOpChange(pre, post fileSnap) bool {
	if pre.exists != post.exists {
		return false
	}
	if !pre.exists {
		return true // never existed: nothing happened
	}
	// Both exist. A directory (empty hash) is only a no-op when size is
	// unchanged; a file is a no-op when its content hash is unchanged.
	if pre.hash == "" || post.hash == "" {
		return pre.size == post.size
	}
	return pre.hash == post.hash
}

func (j *Journal) storeBaseline(conversationID, path, hash string) {
	if hash == "" {
		return
	}
	_, blobs, err := j.sidecar(conversationID)
	if err != nil {
		slog.Warn("journal: sidecar open failed", "conversation", conversationID, "err", err)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("journal: read baseline failed", "path", path, "err", err)
		return
	}
	if err := blobs.put(hash, data); err != nil {
		slog.Warn("journal: baseline blob write failed", "path", path, "err", err)
	}
}

func (j *Journal) sidecar(conversationID string) (string, *blobStore, error) {
	dir, err := ensureSidecarDir(j.dataDir, conversationID)
	if err != nil {
		return "", nil, err
	}
	return dir, newBlobStore(dir), nil
}

func (j *Journal) prepareBaseline(req application.MutationRequest, rs *replayState, path string, pre fileSnap) {
	pt := rs.paths[path]
	if pt != nil && pt.touched {
		return
	}
	switch {
	case pre.exists && pre.hash != "":
		j.storeBaseline(req.ConversationID, path, pre.hash)
	case !pre.exists:
		return
	}
}

func (j *Journal) appendChange(req application.MutationRequest, rs *replayState, path string, pre, post fileSnap) {
	pt := rs.paths[path]
	firstTouch := pt == nil || !pt.touched
	if pt == nil {
		pt = &pathTrack{}
		rs.paths[path] = pt
	}
	if firstTouch {
		switch {
		case pre.exists && pre.hash != "":
			pt.baselineHash = pre.hash
		case post.exists && post.hash != "":
			pt.baselineHash = post.hash
			j.storeBaseline(req.ConversationID, path, post.hash)
		}
		pt.touched = true
	}

	origin := j.resolveOrigin(*rs, path, pre.hash, pre.exists)
	change := domain.FileChange{
		Path:       path,
		Kind:       changeKind(pre.exists, post.exists),
		Origin:     origin,
		BeforeHash: pre.hash,
		AfterHash:  post.hash,
		BeforeSize: pre.size,
		AfterSize:  post.size,
		EventID:    req.ToolCallID,
	}
	now := time.Now().UTC()
	ev := journalEvent{
		Type:    eventTypeChange,
		TS:      now,
		EventID: req.ToolCallID,
		RunID:   req.RunID,
		Tool:    req.ToolName,
		Change:  ptrFileChange(change),
	}
	if err := j.store.append(req.ConversationID, ev); err != nil {
		slog.Warn("journal: append change failed", "conversation", req.ConversationID, "err", err)
		return
	}
	rs.changes = append(rs.changes, change)
	pt.lastAfterHash = post.hash
	pt.lastTouchTS = now
}

func ptrFileChange(c domain.FileChange) *domainFileChange {
	fc := fileChangeFromDomain(c)
	return &fc
}

// WrapMutation implements application.ChangeJournal.
func (j *Journal) WrapMutation(ctx context.Context, req application.MutationRequest, exec func() error) error {
	switch req.Class {
	case domain.MutationDeclared:
		return j.wrapDeclared(req, exec)
	case domain.MutationOpaque:
		return j.wrapOpaque(req, exec)
	case domain.MutationUnobserved:
		j.wrapUnobserved(req)
		return exec()
	default:
		return exec()
	}
}

func (j *Journal) wrapDeclared(req application.MutationRequest, exec func() error) error {
	rs := j.loadReplay(req.ConversationID)
	paths := make([]string, 0, len(req.Paths))
	pre := make(map[string]fileSnap, len(req.Paths))
	for _, path := range req.Paths {
		if path == "" {
			continue
		}
		if _, dup := pre[path]; !dup {
			paths = append(paths, path)
		}
		pre[path] = j.snapPath(path)
	}
	for _, path := range paths {
		j.prepareBaseline(req, &rs, path, pre[path])
	}
	err := exec()
	for _, path := range paths {
		before := pre[path]
		after := j.snapPath(path)
		if noOpChange(before, after) {
			continue
		}
		j.appendChange(req, &rs, path, before, after)
	}
	return err
}

func (j *Journal) wrapOpaque(req application.MutationRequest, exec func() error) error {
	root := req.WorkspaceRoot
	if req.Cwd != "" {
		root = req.Cwd
	}
	if root == "" {
		return exec()
	}
	rs := j.loadReplay(req.ConversationID)
	before, err := snapshotDir(root)
	if err != nil {
		slog.Warn("journal: pre-listing failed", "root", root, "err", err)
		return exec()
	}
	// Pre-hash every listed file. snapPath consults the (size, mtime) hash
	// cache, so files untouched since the previous snapshot are not re-read
	// (spec §23 Level 1/2).
	preSnaps := make(map[string]fileSnap, len(before))
	for rel := range before {
		abs := absPath(root, rel)
		preSnaps[rel] = j.snapPath(abs)
	}
	for rel, snap := range preSnaps {
		j.prepareBaseline(req, &rs, absPath(root, rel), snap)
	}
	execErr := exec()
	after, err := snapshotDir(root)
	if err != nil {
		slog.Warn("journal: post-listing failed", "root", root, "err", err)
		return execErr
	}
	added, modified, deleted := diffListing(before, after)
	for _, rel := range added {
		abs := absPath(root, rel)
		post := j.snapPath(abs)
		if pt := rs.paths[abs]; pt == nil || !pt.touched {
			if post.exists && post.hash != "" {
				j.storeBaseline(req.ConversationID, abs, post.hash)
			}
		}
		j.appendChange(req, &rs, abs, fileSnap{}, post)
	}
	for _, rel := range modified {
		abs := absPath(root, rel)
		pre := preSnaps[rel]
		post := j.snapPath(abs)
		if noOpChange(pre, post) {
			continue // metadata-only change: content identical
		}
		j.appendChange(req, &rs, abs, pre, post)
	}
	for _, rel := range deleted {
		abs := absPath(root, rel)
		pre := preSnaps[rel]
		j.appendChange(req, &rs, abs, pre, fileSnap{})
	}
	return execErr
}

func (j *Journal) wrapUnobserved(req application.MutationRequest) {
	ev := journalEvent{
		Type:    eventTypeUnobserved,
		TS:      time.Now().UTC(),
		EventID: req.ToolCallID,
		RunID:   req.RunID,
		Tool:    req.ToolName,
	}
	if err := j.store.append(req.ConversationID, ev); err != nil {
		slog.Warn("journal: append unobserved failed", "conversation", req.ConversationID, "err", err)
	}
}

// SessionState implements application.ChangeJournal.
func (j *Journal) SessionState(ctx context.Context, conversationID, workspaceRoot string) (*application.WorkspaceState, error) {
	events, err := j.store.readAll(conversationID)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return &application.WorkspaceState{
			ConversationID: conversationID,
			WorkspaceRoot:  workspaceRoot,
			Diffs:          map[string]string{},
		}, nil
	}
	rs := replayEvents(events)
	diffs := make(map[string]string)

	dir, err := sidecarPath(j.dataDir, conversationID)
	var blobs *blobStore
	if err == nil {
		blobs = newBlobStore(dir)
	}

	seen := make(map[string]struct{})
	for path, pt := range rs.paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		diff, ok := renderDiff(blobs, workspaceRoot, path, pt)
		if ok {
			diffs[path] = diff
		}
	}

	return &application.WorkspaceState{
		ConversationID: conversationID,
		WorkspaceRoot:  workspaceRoot,
		Changes:        rs.changes,
		Diffs:          diffs,
	}, nil
}

// renderDiff renders the unified diff between the recorded baseline and the
// current file content. Diff headers use the path relative to the workspace
// root so the agent sees "a/sub/dir/file.go", not a bare basename.
func renderDiff(blobs *blobStore, workspaceRoot, path string, pt *pathTrack) (string, bool) {
	var beforeBytes []byte
	if blobs != nil && pt != nil && pt.baselineHash != "" && blobs.exists(pt.baselineHash) {
		data, err := blobs.get(pt.baselineHash)
		if err != nil {
			return "", false
		}
		beforeBytes = data
	}
	var afterBytes []byte
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		data, err := readFileLimited(path, maxDiffBytes)
		if err != nil {
			afterBytes = nil
		} else {
			afterBytes = data
		}
	}
	if !isTextDiffEligible(beforeBytes, maxDiffBytes) || !isTextDiffEligible(afterBytes, maxDiffBytes) {
		return "", false
	}
	beforeLines := splitLines(string(beforeBytes))
	afterLines := splitLines(string(afterBytes))
	ud := difflib.UnifiedDiff{
		A:        beforeLines,
		B:        afterLines,
		FromFile: "a/" + displayPath(workspaceRoot, path),
		ToFile:   "b/" + displayPath(workspaceRoot, path),
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(ud)
	if err != nil {
		return "", false
	}
	return text, true
}

// displayPath returns the path relative to the workspace root when possible,
// falling back to the absolute path for files outside the workspace.
func displayPath(workspaceRoot, path string) string {
	if workspaceRoot != "" {
		if rel, err := filepath.Rel(workspaceRoot, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

func splitLines(s string) []string {
	if s == "" {
		return []string{}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

// Archive compresses the live journal into the gzip archive as a new
// multi-member append. Called at turn end so the JSONL stays bounded.
func (j *Journal) Archive(conversationID string) error {
	return j.store.archive(conversationID)
}

// Remove deletes the conversation's entire journal sidecar (events and
// blobs). Called when the conversation is deleted.
func (j *Journal) Remove(conversationID string) error {
	return j.store.remove(conversationID)
}
