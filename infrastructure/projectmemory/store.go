// Package projectmemory implements the skill-compatible project-memory
// store: {base}/{key}/{kind}.md with anchored BEGIN_ENTRY/END_ENTRY blocks.
package projectmemory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// Store persists project memory on disk. Base is resolved on every call
// from dataDir + an optional Settings override so a Settings change takes
// effect without restart.
type Store struct {
	dataDir  string
	override func() string
	now      func() time.Time
	mu       sync.Mutex
}

// New constructs a store. override may be nil (empty = default base).
func New(dataDir string, override func() string) *Store {
	if override == nil {
		override = func() string { return "" }
	}
	return &Store{dataDir: dataDir, override: override, now: func() time.Time { return clock.NewTime().Time() }}
}

func (s *Store) base() string {
	return domain.ResolveProjectMemoryBase(s.dataDir, s.override())
}

func (s *Store) dir(workspace string) string {
	return filepath.Join(s.base(), domain.ProjectMemoryKey(workspace))
}

func (s *Store) loadBlobs(workspace string, includeArchive bool) ([]domain.ProjectMemoryFileBlob, error) {
	dir := s.dir(workspace)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var blobs []domain.ProjectMemoryFileBlob
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		kind := strings.TrimSuffix(e.Name(), ".md")
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		blobs = append(blobs, domain.ProjectMemoryFileBlob{
			Rel: e.Name(), Kind: kind, Raw: string(raw),
		})
	}
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Rel < blobs[j].Rel })
	if !includeArchive {
		return blobs, nil
	}
	archDir := filepath.Join(dir, "archive")
	archEnts, err := os.ReadDir(archDir)
	if err != nil {
		if os.IsNotExist(err) {
			return blobs, nil
		}
		return nil, err
	}
	var arch []domain.ProjectMemoryFileBlob
	for _, e := range archEnts {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		kind := strings.TrimSuffix(e.Name(), ".md")
		raw, err := os.ReadFile(filepath.Join(archDir, e.Name()))
		if err != nil {
			return nil, err
		}
		arch = append(arch, domain.ProjectMemoryFileBlob{
			Rel: filepath.ToSlash(filepath.Join("archive", e.Name())), Kind: kind, Raw: string(raw), Archived: true,
		})
	}
	sort.Slice(arch, func(i, j int) bool { return arch[i].Rel < arch[j].Rel })
	return append(blobs, arch...), nil
}

func blobsToEntries(blobs []domain.ProjectMemoryFileBlob) []domain.ProjectMemoryEntry {
	var out []domain.ProjectMemoryEntry
	for _, b := range blobs {
		out = append(out, domain.ParseProjectMemoryEntries(b.Raw, b.Rel, b.Kind, b.Archived)...)
	}
	return out
}

func (s *Store) Query(workspace string, q domain.ProjectMemoryQuery) ([]domain.ProjectMemoryHit, error) {
	blobs, err := s.loadBlobs(workspace, q.Archive)
	if err != nil {
		return nil, err
	}
	return domain.MatchProjectMemoryQuery(blobsToEntries(blobs), q), nil
}

func (s *Store) List(workspace string) ([]string, error) {
	dir := s.dir(workspace)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, kind := range domain.ProjectKindReadOrder {
		name := kind + ".md"
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			out = append(out, name)
			seen[name] = true
		}
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var extra []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || seen[e.Name()] {
			continue
		}
		extra = append(extra, e.Name())
	}
	sort.Strings(extra)
	out = append(out, extra...)
	arch, err := os.ReadDir(filepath.Join(dir, "archive"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var archNames []string
	for _, e := range arch {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		archNames = append(archNames, filepath.ToSlash(filepath.Join("archive", e.Name())))
	}
	sort.Strings(archNames)
	return append(out, archNames...), nil
}

func (s *Store) Read(workspace, kind, id string) (string, error) {
	kind = domain.NormalizeProjectKindFile(kind)
	id = strings.TrimSpace(id)
	if kind == "" && id == "" {
		return "", fmt.Errorf("kind or id is required")
	}
	blobs, err := s.loadBlobs(workspace, true)
	if err != nil {
		return "", err
	}
	if id != "" {
		for _, e := range blobsToEntries(blobs) {
			if e.ID != id {
				continue
			}
			if kind != "" && domain.NormalizeProjectKindFile(e.FileKind) != kind {
				continue
			}
			return e.Body, nil
		}
		return "", fmt.Errorf("project memory entry %q not found", id)
	}
	for _, b := range blobs {
		if !b.Archived && b.Kind == kind {
			return b.Raw, nil
		}
	}
	return "", fmt.Errorf("project memory file %s.md not found", kind)
}

func (s *Store) Admit(workspace, kind, id, content string) (domain.ProjectMemoryAdmitResult, error) {
	kind = domain.NormalizeProjectKindFile(kind)
	if kind == "" {
		return domain.ProjectMemoryAdmitResult{}, fmt.Errorf("kind is required")
	}
	if msg := domain.RejectProjectUserKind(kind); msg != "" {
		return domain.ProjectMemoryAdmitResult{}, fmt.Errorf("%s", msg)
	}
	if !domain.IsCanonicalProjectKind(kind) {
		return domain.ProjectMemoryAdmitResult{}, fmt.Errorf("kind %q is not a writable project-memory kind", kind)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return domain.ProjectMemoryAdmitResult{}, fmt.Errorf("content is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		id = domain.ExtractProjectEntryID(content)
	}
	if id == "" {
		return domain.ProjectMemoryAdmitResult{}, fmt.Errorf("id is required (or include an ID: field in content)")
	}
	if extracted := domain.ExtractProjectEntryID(content); extracted != "" && extracted != id {
		return domain.ProjectMemoryAdmitResult{}, fmt.Errorf("id %q does not match content ID %q", id, extracted)
	}
	if !domain.ProjectKindIDValid(kind, id) {
		return domain.ProjectMemoryAdmitResult{}, fmt.Errorf("id %q does not match required prefix %s for kind %s", id, domain.ProjectKindIDPrefix(kind), kind)
	}
	body := domain.WrapProjectEntry(id, content)

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.dir(workspace)
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o755); err != nil {
		return domain.ProjectMemoryAdmitResult{}, err
	}
	path := filepath.Join(dir, kind+".md")
	prev, prevExists, err := readFileOptional(path)
	if err != nil {
		return domain.ProjectMemoryAdmitResult{}, err
	}
	next := upsertEntry(prev, id, body)
	if err := atomicWrite(path, []byte(next)); err != nil {
		return domain.ProjectMemoryAdmitResult{}, err
	}
	problems, err := s.lintLocked(workspace)
	if err != nil {
		return domain.ProjectMemoryAdmitResult{}, err
	}
	if len(problems) > 0 {
		if prevExists {
			_ = atomicWrite(path, []byte(prev))
		} else {
			_ = os.Remove(path)
		}
		return domain.ProjectMemoryAdmitResult{}, &domain.ProjectMemoryLintError{Problems: problems}
	}
	result := domain.ProjectMemoryAdmitResult{ID: id, Kind: kind}
	if kind == domain.ProjectKindDebug {
		note, err := s.trackPatternsLocked(dir, kind)
		if err != nil {
			return domain.ProjectMemoryAdmitResult{}, err
		}
		result.PatternNote = note
	}
	return result, nil
}

func (s *Store) Archive(workspace, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.dir(workspace)
	blobs, err := s.loadBlobs(workspace, false)
	if err != nil {
		return err
	}
	var found *domain.ProjectMemoryEntry
	for _, e := range blobsToEntries(blobs) {
		if e.ID == id {
			cp := e
			found = &cp
			break
		}
	}
	if found == nil {
		return fmt.Errorf("project memory entry %q not found", id)
	}
	kind := found.FileKind
	livePath := filepath.Join(dir, kind+".md")
	live, err := os.ReadFile(livePath)
	if err != nil {
		return err
	}
	nextLive, ok := removeEntry(string(live), id)
	if !ok {
		return fmt.Errorf("project memory entry %q not found in %s.md", id, kind)
	}
	retired := retireStatus(found.Body)
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o755); err != nil {
		return err
	}
	archPath := filepath.Join(dir, "archive", kind+".md")
	prevArch, _, err := readFileOptional(archPath)
	if err != nil {
		return err
	}
	nextArch := upsertEntry(prevArch, id, retired)
	if err := atomicWrite(livePath, []byte(nextLive)); err != nil {
		return err
	}
	if err := atomicWrite(archPath, []byte(nextArch)); err != nil {
		_ = atomicWrite(livePath, live)
		return err
	}
	return nil
}

func (s *Store) Lint(workspace string) ([]domain.ProjectMemoryLintProblem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lintLocked(workspace)
}

func (s *Store) lintLocked(workspace string) ([]domain.ProjectMemoryLintProblem, error) {
	blobs, err := s.loadBlobs(workspace, true)
	if err != nil {
		return nil, err
	}
	return domain.LintProjectMemory(blobs, domain.ProjectPatternThreshold), nil
}

func (s *Store) IndexExtract(workspace string) (domain.ProjectIndexExtract, bool, error) {
	indexPath := filepath.Join(s.dir(workspace), "index.md")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.ProjectIndexExtract{}, false, nil
		}
		return domain.ProjectIndexExtract{}, false, err
	}
	ents := domain.ParseProjectMemoryEntries(string(raw), "index.md", domain.ProjectKindIndex, false)
	var chosen *domain.ProjectMemoryEntry
	for i := range ents {
		if ents[i].ID == "IDX-project" {
			chosen = &ents[i]
			break
		}
	}
	if chosen == nil && len(ents) == 1 {
		chosen = &ents[0]
	}
	if chosen == nil {
		return domain.ProjectIndexExtract{}, false, nil
	}
	x := domain.CompactProjectIndex(*chosen)
	if x.Purpose == "" && x.Locks == "" && x.CurrentState == "" && x.Routes == "" {
		return x, false, nil
	}
	return x, true, nil
}

func (s *Store) trackPatternsLocked(dir, sourceKind string) (string, error) {
	threshold := domain.ProjectPatternThreshold
	type row struct{ key, id string }
	var rows []row
	seenID := map[string]bool{}
	for _, rel := range []string{
		filepath.Join("archive", sourceKind+".md"),
		sourceKind + ".md",
	} {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		archived := strings.Contains(rel, "archive")
		for _, e := range domain.ParseProjectMemoryEntries(string(raw), rel, sourceKind, archived) {
			key := domain.NormalizePatternKey(e.Fields["PATTERN_KEY"])
			if e.ID == "" || key == "" || seenID[e.ID] {
				continue
			}
			seenID[e.ID] = true
			rows = append(rows, row{key: key, id: e.ID})
		}
	}
	members := map[string][]string{}
	for _, r := range rows {
		members[r.key] = append(members[r.key], r.id)
	}
	var qualifying []string
	for key, ids := range members {
		if len(ids) >= threshold {
			qualifying = append(qualifying, key)
		}
	}
	if len(qualifying) == 0 {
		return "", nil
	}
	sort.Strings(qualifying)
	path := filepath.Join(dir, "patterns.md")
	prev, _, err := readFileOptional(path)
	if err != nil {
		return "", err
	}
	patterns := prev
	today := clock.NewTime(s.now()).Format("2006-01-02")
	changed := false
	var notes []string
	for _, key := range qualifying {
		ids := members[key]
		occ := len(ids)
		pid := domain.PatternEntryID(sourceKind, key)
		memberCSV := strings.Join(ids, ", ")
		prevCount, status, _, _, exists := readPatternMeta(patterns, pid)
		if exists && prevCount == occ {
			continue
		}
		if exists {
			patterns = updatePatternBlock(patterns, pid, key, sourceKind, occ, memberCSV, today)
		} else {
			block := patternBlock(pid, key, sourceKind, occ, memberCSV, today)
			if strings.TrimSpace(patterns) == "" {
				patterns = block
			} else {
				patterns = strings.TrimRight(patterns, "\n") + "\n\n" + block
			}
			status = "TRACKING"
		}
		changed = true
		if status == "SCRIPTED" {
			continue
		}
		if shouldSuggestPattern(prevCount, occ, threshold) {
			notes = append(notes, fmt.Sprintf("memory-pattern: '%s' (%s) has occurred %dx. Promote the stable procedure to playbook.md.", key, sourceKind, occ))
		}
	}
	if changed {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		if err := atomicWrite(path, []byte(ensureTrailingNewline(patterns))); err != nil {
			return "", err
		}
	}
	return strings.Join(notes, "\n"), nil
}

func shouldSuggestPattern(previous, occ, threshold int) bool {
	if previous < threshold && occ >= threshold {
		return true
	}
	next := threshold * 2
	for next <= occ {
		if previous < next {
			return true
		}
		next *= 2
	}
	return false
}

func patternBlock(id, key, kind string, occ int, members, today string) string {
	return strings.Join([]string{
		"### BEGIN_ENTRY: " + id + " ###",
		"ID: " + id,
		"KIND: PATTERN",
		"STATUS: TRACKING",
		"PATTERN_KEY: " + key,
		"SOURCE_KIND: " + kind,
		fmt.Sprintf("OCCURRENCES: %d", occ),
		"MEMBER_IDS: [" + members + "]",
		"FIRST_SEEN: " + today,
		"LAST_SEEN: " + today,
		"SUGGESTED_SCRIPT: none",
		"### END_ENTRY: " + id + " ###",
		"",
	}, "\n")
}

func readPatternMeta(raw, id string) (occ int, status, firstSeen, suggested string, exists bool) {
	status = "TRACKING"
	suggested = "none"
	in := false
	for _, line := range strings.Split(raw, "\n") {
		if line == "ID: "+id {
			in = true
			exists = true
			continue
		}
		if in && strings.HasPrefix(line, "### END_ENTRY: ") {
			break
		}
		if !in {
			continue
		}
		switch {
		case strings.HasPrefix(line, "OCCURRENCES: "):
			fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "OCCURRENCES: ")), "%d", &occ)
		case strings.HasPrefix(line, "STATUS: "):
			status = strings.TrimSpace(strings.TrimPrefix(line, "STATUS: "))
		case strings.HasPrefix(line, "FIRST_SEEN: "):
			firstSeen = strings.TrimSpace(strings.TrimPrefix(line, "FIRST_SEEN: "))
		case strings.HasPrefix(line, "SUGGESTED_SCRIPT: "):
			suggested = strings.TrimSpace(strings.TrimPrefix(line, "SUGGESTED_SCRIPT: "))
		}
	}
	return occ, status, firstSeen, suggested, exists
}

func updatePatternBlock(raw, id, key, kind string, occ int, members, today string) string {
	in := false
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if line == "ID: "+id {
			in = true
			continue
		}
		if in && strings.HasPrefix(line, "### END_ENTRY: ") {
			in = false
			continue
		}
		if !in {
			continue
		}
		switch {
		case strings.HasPrefix(line, "PATTERN_KEY: "):
			lines[i] = "PATTERN_KEY: " + key
		case strings.HasPrefix(line, "SOURCE_KIND: "):
			lines[i] = "SOURCE_KIND: " + kind
		case strings.HasPrefix(line, "OCCURRENCES: "):
			lines[i] = fmt.Sprintf("OCCURRENCES: %d", occ)
		case strings.HasPrefix(line, "MEMBER_IDS: "):
			lines[i] = "MEMBER_IDS: [" + members + "]"
		case strings.HasPrefix(line, "LAST_SEEN: "):
			lines[i] = "LAST_SEEN: " + today
		}
	}
	return strings.Join(lines, "\n")
}

func upsertEntry(raw, id, body string) string {
	body = ensureTrailingNewline(strings.TrimRight(body, "\n"))
	if next, ok := replaceEntry(raw, id, body); ok {
		return next
	}
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return body
	}
	return raw + "\n\n" + body
}

func replaceEntry(raw, id, body string) (string, bool) {
	begin := "### BEGIN_ENTRY: " + id + " ###"
	end := "### END_ENTRY: " + id + " ###"
	start := strings.Index(raw, begin)
	if start < 0 {
		return raw, false
	}
	relEnd := strings.Index(raw[start:], end)
	if relEnd < 0 {
		return raw, false
	}
	stop := start + relEnd + len(end)
	if stop < len(raw) && raw[stop] == '\n' {
		stop++
	}
	return raw[:start] + body + raw[stop:], true
}

func removeEntry(raw, id string) (string, bool) {
	next, ok := replaceEntry(raw, id, "")
	if !ok {
		return raw, false
	}
	next = strings.ReplaceAll(next, "\n\n\n", "\n\n")
	return strings.TrimLeft(next, "\n"), true
}

func retireStatus(body string) string {
	lines := strings.Split(body, "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "STATUS: ") {
			found = true
			st := strings.TrimSpace(strings.TrimPrefix(line, "STATUS: "))
			if st == "ACTIVE" || st == "TRACKING" || st == "" {
				lines[i] = "STATUS: RETIRED"
			}
		}
	}
	if found {
		return strings.Join(lines, "\n")
	}
	out := make([]string, 0, len(lines)+1)
	inserted := false
	for _, line := range lines {
		out = append(out, line)
		if !inserted && strings.HasPrefix(line, "KIND: ") {
			out = append(out, "STATUS: RETIRED")
			inserted = true
		}
	}
	if !inserted {
		out = make([]string, 0, len(lines)+1)
		for _, line := range lines {
			out = append(out, line)
			if !inserted && strings.HasPrefix(line, "ID: ") {
				out = append(out, "STATUS: RETIRED")
				inserted = true
			}
		}
	}
	return strings.Join(out, "\n")
}

func readFileOptional(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(b), true, nil
}

func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nusashell-pm-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
