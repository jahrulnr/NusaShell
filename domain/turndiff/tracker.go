package turndiff

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type trackedContent struct {
	content  string
	revision uint64
}

type trackedPath struct {
	environmentID string
	path          string
}

type diffCacheKey struct {
	leftEnv, leftPath   string
	hasLeftRev          bool
	leftRev             uint64
	rightEnv, rightPath string
	hasRightRev         bool
	rightRev            uint64
}

// Tracker records the net text diff for the current turn from committed
// file_* mutations, without rereading the workspace. Ported from Codex
// TurnDiffTracker.
type Tracker struct {
	valid          bool
	displayRoots   map[string]string
	baselineByPath map[trackedPath]trackedContent
	currentByPath  map[trackedPath]trackedContent
	originByPath   map[trackedPath]trackedPath
	nextRevision   uint64
	renderedDiffs  map[diffCacheKey]*string
	unifiedDiff    *string

	renderedDiffCount int
}

// Option configures a Tracker.
type Option func(*Tracker)

// WithDisplayRoot sets the single-environment (id "") display root used to
// render git paths as workspace-relative.
func WithDisplayRoot(root string) Option {
	return func(t *Tracker) {
		t.displayRoots[""] = root
	}
}

// WithEnvironmentDisplayRoots maps environment ids to display roots.
func WithEnvironmentDisplayRoots(roots map[string]string) Option {
	return func(t *Tracker) {
		for id, root := range roots {
			t.displayRoots[id] = root
		}
	}
}

// New returns a valid empty turn-diff tracker.
func New(opts ...Option) *Tracker {
	t := &Tracker{
		valid:          true,
		displayRoots:   map[string]string{},
		baselineByPath: map[trackedPath]trackedContent{},
		currentByPath:  map[trackedPath]trackedContent{},
		originByPath:   map[trackedPath]trackedPath{},
		renderedDiffs:  map[diffCacheKey]*string{},
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// TrackDelta applies a committed file delta in the default environment.
func (t *Tracker) TrackDelta(delta Delta) {
	t.TrackDeltaIn("", delta)
}

// TrackDeltaIn applies a committed file delta in environmentID.
func (t *Tracker) TrackDeltaIn(environmentID string, delta Delta) {
	if t == nil || !t.valid {
		return
	}
	if !delta.Exact {
		t.Invalidate()
		return
	}
	for i := range delta.Changes {
		t.applyChange(environmentID, delta.Changes[i])
	}
	t.refreshUnifiedDiff()
}

// Invalidate drops the turn diff (inexact or unreadable mutation).
func (t *Tracker) Invalidate() {
	if t == nil {
		return
	}
	t.valid = false
	t.renderedDiffs = map[diffCacheKey]*string{}
	t.unifiedDiff = nil
}

// UnifiedDiff returns the aggregated git unified diff, or false when none.
func (t *Tracker) UnifiedDiff() (string, bool) {
	if t == nil || t.unifiedDiff == nil {
		return "", false
	}
	return *t.unifiedDiff, true
}

// HasUnifiedDiff reports whether a non-empty aggregated diff is cached.
func (t *Tracker) HasUnifiedDiff() bool {
	return t != nil && t.unifiedDiff != nil
}

func (t *Tracker) refreshUnifiedDiff() {
	renamePairs := t.renamePairs()
	pairedDest := map[trackedPath]struct{}{}
	for _, dest := range renamePairs {
		pairedDest[dest] = struct{}{}
	}
	handled := map[trackedPath]struct{}{}
	paths := make([]trackedPath, 0, len(t.baselineByPath)+len(t.currentByPath))
	for p := range t.baselineByPath {
		paths = append(paths, p)
	}
	for p := range t.currentByPath {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		return t.displayPath(paths[i]) < t.displayPath(paths[j])
	})
	paths = dedupTracked(paths)

	previous := t.renderedDiffs
	t.renderedDiffs = map[diffCacheKey]*string{}
	var aggregated strings.Builder
	for _, path := range paths {
		if _, ok := handled[path]; ok {
			continue
		}
		handled[path] = struct{}{}
		if _, dest := pairedDest[path]; dest {
			continue
		}
		leftPath, rightPath := path, path
		if dest, ok := renamePairs[path]; ok {
			handled[dest] = struct{}{}
			rightPath = dest
		}
		leftContent, hasLeft := t.baselineByPath[leftPath]
		rightContent, hasRight := t.currentByPath[rightPath]
		key := diffCacheKey{
			leftEnv: leftPath.environmentID, leftPath: leftPath.path,
			hasLeftRev: hasLeft, leftRev: leftContent.revision,
			rightEnv: rightPath.environmentID, rightPath: rightPath.path,
			hasRightRev: hasRight, rightRev: rightContent.revision,
		}
		rendered, ok := previous[key]
		if !ok {
			var leftStr, rightStr *string
			if hasLeft {
				s := leftContent.content
				leftStr = &s
			}
			if hasRight {
				s := rightContent.content
				rightStr = &s
			}
			rendered = t.renderDiff(leftPath, leftStr, rightPath, rightStr)
		}
		if rendered != nil {
			aggregated.WriteString(*rendered)
			if !strings.HasSuffix(*rendered, "\n") {
				aggregated.WriteByte('\n')
			}
		}
		t.renderedDiffs[key] = rendered
	}
	if aggregated.Len() == 0 {
		t.unifiedDiff = nil
		return
	}
	s := aggregated.String()
	t.unifiedDiff = &s
}

func dedupTracked(paths []trackedPath) []trackedPath {
	if len(paths) == 0 {
		return paths
	}
	out := paths[:0]
	var prev trackedPath
	first := true
	for _, p := range paths {
		if first || p != prev {
			out = append(out, p)
			prev = p
			first = false
		}
	}
	return out
}

func (t *Tracker) applyChange(environmentID string, change FileChange) {
	source := trackedPath{environmentID: environmentID, path: change.Path}
	switch change.Kind {
	case ChangeAdd:
		t.applyAdd(source, change.Content, change.OverwrittenContent)
	case ChangeDelete:
		t.applyDelete(source, change.Content)
	case ChangeUpdate:
		var dest *trackedPath
		if change.MovePath != nil {
			p := trackedPath{environmentID: environmentID, path: *change.MovePath}
			dest = &p
		}
		t.applyUpdate(source, dest, change.OldContent, change.OverwrittenMoveContent, change.NewContent)
	}
}

func (t *Tracker) applyAdd(path trackedPath, content string, overwritten *string) {
	delete(t.originByPath, path)
	_, inCurrent := t.currentByPath[path]
	_, inBaseline := t.baselineByPath[path]
	if !inCurrent && !inBaseline && overwritten != nil {
		t.baselineByPath[path] = t.trackedContent(*overwritten)
	}
	t.currentByPath[path] = t.trackedContent(content)
}

func (t *Tracker) applyDelete(path trackedPath, content string) {
	_, hadCurrent := t.currentByPath[path]
	delete(t.currentByPath, path)
	if !hadCurrent {
		if _, inBaseline := t.baselineByPath[path]; !inBaseline {
			t.baselineByPath[path] = t.trackedContent(content)
		}
	}
	delete(t.originByPath, path)
}

func (t *Tracker) applyUpdate(source trackedPath, movePath *trackedPath, oldContent string, overwrittenMove *string, newContent string) {
	_, inCurrent := t.currentByPath[source]
	_, inBaseline := t.baselineByPath[source]
	if !inCurrent && !inBaseline {
		t.baselineByPath[source] = t.trackedContent(oldContent)
	}
	if movePath == nil {
		t.currentByPath[source] = t.trackedContent(newContent)
		return
	}
	dest := *movePath
	_, destCurrent := t.currentByPath[dest]
	_, destBaseline := t.baselineByPath[dest]
	if !destCurrent && !destBaseline && overwrittenMove != nil {
		t.baselineByPath[dest] = t.trackedContent(*overwrittenMove)
	}
	origin, ok := t.originByPath[source]
	delete(t.originByPath, source)
	if !ok {
		origin = source
	}
	delete(t.currentByPath, source)
	t.currentByPath[dest] = t.trackedContent(newContent)
	delete(t.originByPath, dest)
	if dest != origin {
		t.originByPath[dest] = origin
	}
}

func (t *Tracker) trackedContent(content string) trackedContent {
	rev := t.nextRevision
	t.nextRevision++
	return trackedContent{content: content, revision: rev}
}

func (t *Tracker) renamePairs() map[trackedPath]trackedPath {
	out := map[trackedPath]trackedPath{}
	for dest, origin := range t.originByPath {
		if dest == origin {
			continue
		}
		if _, ok := t.currentByPath[origin]; ok {
			continue
		}
		if _, ok := t.currentByPath[dest]; !ok {
			continue
		}
		if _, ok := t.baselineByPath[origin]; !ok {
			continue
		}
		if _, ok := t.baselineByPath[dest]; ok {
			continue
		}
		out[origin] = dest
	}
	return out
}

func (t *Tracker) renderDiff(leftPath trackedPath, leftContent *string, rightPath trackedPath, rightContent *string) *string {
	if ptrStrEqual(leftContent, rightContent) {
		return nil
	}
	t.renderedDiffCount++

	leftDisplay := strings.ReplaceAll(t.displayPath(leftPath), "\\", "/")
	rightDisplay := strings.ReplaceAll(t.displayPath(rightPath), "\\", "/")
	leftOID := ZeroOID
	if leftContent != nil {
		leftOID = gitBlobOID([]byte(*leftContent))
	}
	rightOID := ZeroOID
	if rightContent != nil {
		rightOID = gitBlobOID([]byte(*rightContent))
	}

	var diff strings.Builder
	fmt.Fprintf(&diff, "diff --git a/%s b/%s\n", leftDisplay, rightDisplay)
	switch {
	case leftContent == nil && rightContent != nil:
		fmt.Fprintf(&diff, "new file mode %s\n", RegularFileMode)
	case leftContent != nil && rightContent == nil:
		fmt.Fprintf(&diff, "deleted file mode %s\n", RegularFileMode)
	case leftContent == nil && rightContent == nil:
		return nil
	}
	fmt.Fprintf(&diff, "index %s..%s\n", leftOID, rightOID)

	oldHeader := DevNull
	if leftContent != nil {
		oldHeader = "a/" + leftDisplay
	}
	newHeader := DevNull
	if rightContent != nil {
		newHeader = "b/" + rightDisplay
	}

	oldBody, newBody := "", ""
	if leftContent != nil {
		oldBody = *leftContent
	}
	if rightContent != nil {
		newBody = *rightContent
	}
	body := unifiedBody(oldBody, newBody)
	fmt.Fprintf(&diff, "--- %s\n+++ %s\n", oldHeader, newHeader)
	diff.WriteString(body)
	s := diff.String()
	return &s
}

func ptrStrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (t *Tracker) displayPath(path trackedPath) string {
	display := path.path
	if root := t.displayRoots[path.environmentID]; root != "" {
		if rel, err := filepath.Rel(root, path.path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			display = rel
		}
	}
	if len(t.displayRoots) > 1 && path.environmentID != "" {
		return path.environmentID + "/" + display
	}
	return display
}

func (t *Tracker) renderedDiffsComputed() int {
	return t.renderedDiffCount
}
