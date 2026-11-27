package symbol

import (
	"context"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// memRoot is a SymbolRoot over a mutable file map — tests mutate files
// between calls to exercise the change detector. walks counts visited
// files so probes can be proven to stop early; walkDelay simulates a
// slow remote walk.
type memRoot struct {
	mu        sync.Mutex
	files     map[string]string
	walks     atomic.Int64
	walkDelay time.Duration
}

func (r *memRoot) Walk(ctx context.Context, visit func(rel string, size int64) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rels := make([]string, 0, len(r.files))
	for rel := range r.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.walkDelay > 0 {
			time.Sleep(r.walkDelay)
		}
		r.walks.Add(1)
		if err := visit(rel, int64(len(r.files[rel]))); err != nil {
			if err == fs.SkipAll {
				return nil
			}
			return err
		}
	}
	return nil
}

func (r *memRoot) ReadFile(_ context.Context, rel string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	body, ok := r.files[rel]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(body), nil
}

func (r *memRoot) write(rel, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[rel] = body
}

// countingIndex returns fixed symbols and records how many builds ran.
type countingIndex struct {
	calls atomic.Int64
	syms  []svc.Symbol
	done  chan struct{} // closed once the first Index returns
}

func (i *countingIndex) Index(context.Context, svc.SymbolRoot) ([]svc.Symbol, error) {
	i.calls.Add(1)
	select {
	case <-i.done:
	default:
		close(i.done)
	}
	return i.syms, nil
}

type mapStore struct {
	mu    sync.Mutex
	items map[string]svc.CachedIndex
}

func (s *mapStore) Load(key string) (svc.CachedIndex, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[key]
	return v, ok
}

func (s *mapStore) Save(key string, idx svc.CachedIndex) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]svc.CachedIndex{}
	}
	s.items[key] = idx
	return nil
}

func testManager(idx svc.SymbolIndex, store CacheStore) *Manager {
	return NewManager(
		func(Options) svc.SymbolIndex { return idx },
		func() Options { return Options{BudgetTokens: 2000, Workers: 1, MaxFileBytes: MaxFileBytes} },
		store,
	)
}

func opener(root svc.SymbolRoot) func(context.Context) (svc.SymbolRoot, error) {
	return func(context.Context) (svc.SymbolRoot, error) { return root, nil }
}

func waitBuild(t *testing.T, idx *countingIndex) {
	t.Helper()
	select {
	case <-idx.done:
	case <-time.After(5 * time.Second):
		t.Fatal("index build did not run")
	}
	// The done channel closes before state lands; give the goroutine a
	// moment to commit, then poll the observable state through Map.
	time.Sleep(20 * time.Millisecond)
}

func TestManagerServesBuildingThenMap(t *testing.T) {
	root := &memRoot{files: map[string]string{"main.go": "package main\n\nfunc main() {}\n"}}
	idx := &countingIndex{done: make(chan struct{}), syms: []svc.Symbol{{Path: "main.go", Lang: "go", Kind: "function", Name: "main", Line: 3, Public: false}}}
	m := testManager(idx, nil)

	first := m.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.Contains(first, "is being built") {
		t.Fatalf("first serve should announce a build, got:\n%s", first)
	}
	waitBuild(t, idx)
	second := m.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.HasPrefix(second, "symbol map for /ws — declarations only") {
		t.Fatalf("expected rendered map, got:\n%s", second)
	}
	if !strings.Contains(second, "func main :3") && !strings.Contains(second, "main") {
		t.Fatalf("map missing symbol:\n%s", second)
	}
	third := m.Map(context.Background(), "ws", "/ws", opener(root))
	if third != second {
		t.Fatal("steady-state serves must be byte-identical for dedup")
	}
	if idx.calls.Load() != 1 {
		t.Fatalf("unchanged files must not rebuild, got %d builds", idx.calls.Load())
	}
}

func TestManagerFileChangeTriggersRebuild(t *testing.T) {
	root := &memRoot{files: map[string]string{"a.go": "package a\n\ntype A struct{}\n"}}
	idx := &countingIndex{done: make(chan struct{}), syms: []svc.Symbol{{Path: "a.go", Lang: "go", Kind: "type", Name: "A", Line: 3}}}
	m := testManager(idx, nil)

	m.Map(context.Background(), "ws", "/ws", opener(root))
	waitBuild(t, idx)
	m.Map(context.Background(), "ws", "/ws", opener(root))

	root.write("b.go", "package b\n\nfunc B() {}\n")
	out := m.Map(context.Background(), "ws", "/ws", opener(root))
	if strings.Contains(out, "is being built") {
		t.Fatalf("changed files must serve the stale map, not churn to 'being built':\n%s", out)
	}
	if !strings.Contains(out, "A :3") {
		t.Fatalf("the stale map should still serve while rebuilding:\n%s", out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for idx.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if idx.calls.Load() != 2 {
		t.Fatalf("hash change must trigger exactly one rebuild, got %d", idx.calls.Load())
	}
}

func TestManagerSingleFlight(t *testing.T) {
	root := &memRoot{files: map[string]string{"a.go": "package a\n"}}
	idx := &countingIndex{done: make(chan struct{})}
	m := testManager(idx, nil)
	// Two serves during the same build window share one build.
	m.Map(context.Background(), "ws", "/ws", opener(root))
	out := m.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.Contains(out, "is being built") {
		t.Fatalf("in-flight build should keep the marker, got:\n%s", out)
	}
	time.Sleep(100 * time.Millisecond)
	if n := idx.calls.Load(); n != 1 {
		t.Fatalf("concurrent serves must single-flight, got %d builds", n)
	}
}

func TestManagerDiskCacheServesWithoutBuild(t *testing.T) {
	root := &memRoot{files: map[string]string{"a.go": "package a\n\nfunc A() {}\n"}}
	idx := &countingIndex{done: make(chan struct{}), syms: []svc.Symbol{{Path: "a.go", Lang: "go", Kind: "function", Name: "A", Line: 3, Public: true}}}
	store := &mapStore{}

	first := testManager(idx, store)
	first.Map(context.Background(), "ws", "/ws", opener(root))
	waitBuild(t, idx)
	first.Map(context.Background(), "ws", "/ws", opener(root))

	// A fresh manager (new process) serves the persisted map with no
	// rebuild at all.
	second := testManager(idx, store)
	out := second.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.HasPrefix(out, "symbol map for /ws — declarations only") {
		t.Fatalf("disk cache should serve the map immediately, got:\n%s", out)
	}
	if idx.calls.Load() != 1 {
		t.Fatalf("warm cache must not rebuild, got %d builds", idx.calls.Load())
	}
}

func TestManagerSkipsUnsupportedFiles(t *testing.T) {
	root := &memRoot{files: map[string]string{
		"a.go":        "package a\n\nfunc A() {}\n",
		"notes.weird": "not a source file\n",
	}}
	idx := &scopedIndex{done: make(chan struct{}), syms: []svc.Symbol{{Path: "a.go", Lang: "go", Kind: "function", Name: "A", Line: 3}}}
	m := testManager(idx, nil)
	m.Map(context.Background(), "ws", "/ws", opener(root))
	waitBuild(t, &idx.countingIndex)
	out := m.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.Contains(out, "1 files skipped: unsupported language") {
		t.Fatalf("expected skip footer, got:\n%s", out)
	}
}

// scopedIndex reports .go files only — notes.weird lands in the skip
// footer and outside the file hash.
type scopedIndex struct{ countingIndex }

func (i *scopedIndex) IndexesFile(rel string) bool { return strings.HasSuffix(rel, ".go") }

// IsBuilding is the provisional-render predicate the tool declares to the
// application: the first-build marker and the wait line that follows it
// are provisional, a real map and a skip verdict are final.
func TestIsBuilding(t *testing.T) {
	final := mapText("/ws", nil, probeResult{}, 0, 0, false, 0)
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"first build", buildingText("/ws"), true},
		{"wait line", buildingText("/ws") + "\n… index still building — call again shortly", true},
		{"final map", final, false},
		{"skip verdict", skipText("/home/user", "is not a project root (no marker, >2000 files)"), false},
	}
	for _, tc := range cases {
		if got := IsBuilding(tc.text); got != tc.want {
			t.Errorf("%s: IsBuilding = %v, want %v", tc.name, got, tc.want)
		}
	}
}
