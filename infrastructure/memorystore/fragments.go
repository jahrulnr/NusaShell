package memorystore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
	clock "nusashell/pkg/time"
)

// FragmentsDir is the subdirectory under the data dir that holds one
// markdown file per memory fragment.
const FragmentsDir = "memory/fragments"

// fragmentFrontmatter is the YAML metadata block at the top of each
// fragment file. It is kept small and non-verbose: only the fields the
// search filter and UI need.
type fragmentFrontmatter struct {
	ID        string   `yaml:"id"`
	Category  string   `yaml:"category"`
	Project   string   `yaml:"project,omitempty"`
	Task      string   `yaml:"task,omitempty"`
	Tags      []string `yaml:"tags,omitempty"`
	Source    string   `yaml:"source,omitempty"`
	CreatedAt string   `yaml:"created_at"`
	UpdatedAt string   `yaml:"updated_at"`
}

// Fragments is the FragmentStore adapter backed by one markdown file
// per entry under <dataDir>/memory/fragments/<id>.md. Each file has a
// YAML frontmatter block delimited by "---" lines, followed by the
// markdown body. The store loads all fragments into memory on first
// access and re-reads files on demand when a Get is requested.
type Fragments struct {
	mu     sync.RWMutex
	dir    string
	cache  map[string]*domain.MemoryFragment
	loaded bool
}

// NewFragments opens (or auto-creates) the fragments directory and
// loads all fragment files into memory.
func NewFragments(dataDir string) (*Fragments, error) {
	dir := filepath.Join(dataDir, FragmentsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f := &Fragments{dir: dir, cache: make(map[string]*domain.MemoryFragment)}
	if err := f.loadAll(); err != nil {
		return nil, err
	}
	return f, nil
}

// loadAll scans the fragments directory and parses every .md file.
func (f *Fragments) loadAll() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return err
	}
	f.cache = make(map[string]*domain.MemoryFragment)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		frag, err := f.readFile(filepath.Join(f.dir, e.Name()))
		if err != nil {
			continue // skip corrupt files rather than failing the whole store
		}
		f.cache[frag.ID] = frag
	}
	f.loaded = true
	return nil
}

// readFile parses one fragment file into a MemoryFragment.
func (f *Fragments) readFile(path string) (*domain.MemoryFragment, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseFragment(string(raw))
}

// parseFragment splits a fragment file into frontmatter + body and
// decodes the YAML metadata.
func parseFragment(raw string) (*domain.MemoryFragment, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "---") {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}
	rest := strings.TrimPrefix(raw, "---\n")
	if strings.HasPrefix(rest, "---") {
		// empty frontmatter
		body := strings.TrimSpace(strings.TrimPrefix(rest, "---"))
		return &domain.MemoryFragment{Content: body}, nil
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, fmt.Errorf("unterminated YAML frontmatter")
	}
	fmText := rest[:end]
	body := strings.TrimSpace(rest[end+4:])
	var fm fragmentFrontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	created, _ := time.Parse(time.RFC3339, fm.CreatedAt)
	updated, _ := time.Parse(time.RFC3339, fm.UpdatedAt)
	if fm.Category == "" {
		fm.Category = domain.FragmentCategoryGeneral
	}
	return &domain.MemoryFragment{
		ID:        fm.ID,
		Category:  fm.Category,
		Project:   fm.Project,
		Task:      fm.Task,
		Tags:      fm.Tags,
		Source:    fm.Source,
		Content:   body,
		CreatedAt: created,
		UpdatedAt: updated,
	}, nil
}

// serializeFragment renders a fragment back to its file format.
func serializeFragment(f *domain.MemoryFragment) string {
	fm := fragmentFrontmatter{
		ID:        f.ID,
		Category:  f.Category,
		Project:   f.Project,
		Task:      f.Task,
		Tags:      f.Tags,
		Source:    f.Source,
		CreatedAt: clock.NewTime(f.CreatedAt).Format(time.RFC3339),
		UpdatedAt: clock.NewTime(f.UpdatedAt).Format(time.RFC3339),
	}
	fmBytes, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n")
	b.WriteString(strings.TrimSpace(f.Content))
	b.WriteString("\n")
	return b.String()
}

// List returns fragments matching the metadata filter (ignores the
// Query field — use Search for content ranking). Returns all fragments
// when the filter is empty.
func (f *Fragments) List(filter domain.FragmentSearchFilter) []*domain.MemoryFragment {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*domain.MemoryFragment, 0, len(f.cache))
	for _, frag := range f.cache {
		if matchesFilter(frag, filter) {
			out = append(out, cloneFragment(frag))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out
}

// Get returns a single fragment by id, or nil if missing.
func (f *Fragments) Get(id string) *domain.MemoryFragment {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if frag, ok := f.cache[id]; ok {
		return cloneFragment(frag)
	}
	return nil
}

// Save writes a fragment to disk and updates the in-memory cache. New
// fragments get a generated ID and timestamps; existing fragments keep
// their ID and CreatedAt but get a fresh UpdatedAt. Content is normalized so
// exact duplicate checks are stable across line endings and trailing spaces.
func (f *Fragments) Save(frag *domain.MemoryFragment) error {
	if frag == nil {
		return fmt.Errorf("fragment is nil")
	}
	frag.Content = domain.NormalizeMemoryContent(frag.Content)
	if frag.Content == "" {
		return fmt.Errorf("fragment content is required")
	}
	if frag.Category == "" {
		frag.Category = domain.FragmentCategoryGeneral
	}
	now := clock.NewTime().Time()
	if frag.ID == "" {
		frag.ID = domain.NewULID(domain.IDPrefixFrag)
		frag.CreatedAt = now
	}
	if frag.CreatedAt.IsZero() {
		frag.CreatedAt = now
	}
	frag.UpdatedAt = now
	path := filepath.Join(f.dir, frag.ID+".md")
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.WriteFile(path, []byte(serializeFragment(frag)), 0o644); err != nil {
		return err
	}
	f.cache[frag.ID] = cloneFragment(frag)
	return nil
}

// SaveIfAbsent saves a fragment only when no existing fragment has the same
// normalized content. It is idempotent for exact duplicates and safe for
// concurrent callers sharing this store instance. The returned existing
// fragment is a clone and can be returned to the caller without exposing the
// store's cache.
func (f *Fragments) SaveIfAbsent(frag *domain.MemoryFragment) (existing *domain.MemoryFragment, saved bool, err error) {
	if frag == nil {
		return nil, false, fmt.Errorf("fragment is nil")
	}
	frag.Content = domain.NormalizeMemoryContent(frag.Content)
	if frag.Content == "" {
		return nil, false, fmt.Errorf("fragment content is required")
	}
	if frag.Category == "" {
		frag.Category = domain.FragmentCategoryGeneral
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, candidate := range f.cache {
		if domain.NormalizeMemoryContent(candidate.Content) == frag.Content {
			return cloneFragment(candidate), false, nil
		}
	}
	now := clock.NewTime().Time()
	if frag.ID == "" {
		frag.ID = domain.NewULID(domain.IDPrefixFrag)
		frag.CreatedAt = now
	}
	if frag.CreatedAt.IsZero() {
		frag.CreatedAt = now
	}
	frag.UpdatedAt = now
	path := filepath.Join(f.dir, frag.ID+".md")
	if err := os.WriteFile(path, []byte(serializeFragment(frag)), 0o644); err != nil {
		return nil, false, err
	}
	f.cache[frag.ID] = cloneFragment(frag)
	return nil, true, nil
}

// Delete removes a fragment file and drops it from the cache.
func (f *Fragments) Delete(id string) error {
	if id == "" {
		return fmt.Errorf("fragment id is required")
	}
	path := filepath.Join(f.dir, id+".md")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("fragment %s not found", id)
		}
		return err
	}
	f.mu.Lock()
	delete(f.cache, id)
	f.mu.Unlock()
	return nil
}

// Search runs a BM25 content query over all fragments, filtered by
// metadata. When the query is empty, results are returned in
// UpdatedAt-descending order with Score 0 (same as List).
func (f *Fragments) Search(filter domain.FragmentSearchFilter) []domain.FragmentSearchHit {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	filter.Limit = 0 // List applies its own limit; we cap below
	candidates := f.List(filter)
	if filter.Query == "" || len(candidates) == 0 {
		if len(candidates) > limit {
			candidates = candidates[:limit]
		}
		hits := make([]domain.FragmentSearchHit, len(candidates))
		for i, c := range candidates {
			hits[i] = domain.FragmentSearchHit{Fragment: c}
		}
		return hits
	}
	docs := make([]jsonstore.BM25Doc, len(candidates))
	for i, c := range candidates {
		docs[i] = jsonstore.BM25Doc{
			ID:   c.ID,
			Text: c.Content + " " + strings.Join(c.Tags, " ") + " " + c.Project + " " + c.Task,
		}
	}
	bm25 := jsonstore.NewBM25(docs)
	results := bm25.Search(filter.Query, limit)
	hits := make([]domain.FragmentSearchHit, 0, len(results))
	for _, r := range results {
		frag := f.Get(r.ID)
		if frag == nil {
			continue
		}
		hits = append(hits, domain.FragmentSearchHit{Fragment: frag, Score: r.Score})
	}
	return hits
}

// matchesFilter returns true if a fragment satisfies all non-zero
// fields of the filter.
func matchesFilter(frag *domain.MemoryFragment, filter domain.FragmentSearchFilter) bool {
	if filter.Category != "" && frag.Category != filter.Category {
		return false
	}
	if filter.Project != "" && frag.Project != filter.Project {
		return false
	}
	if filter.Task != "" && frag.Task != filter.Task {
		return false
	}
	if len(filter.Tags) > 0 {
		have := make(map[string]bool, len(frag.Tags))
		for _, t := range frag.Tags {
			have[t] = true
		}
		for _, want := range filter.Tags {
			if !have[want] {
				return false
			}
		}
	}
	return true
}

// cloneFragment returns a deep copy so callers cannot mutate the cache.
func cloneFragment(f *domain.MemoryFragment) *domain.MemoryFragment {
	if f == nil {
		return nil
	}
	out := *f
	if f.Tags != nil {
		out.Tags = append([]string(nil), f.Tags...)
	}
	return &out
}
