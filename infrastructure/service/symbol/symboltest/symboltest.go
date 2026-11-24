// Package symboltest hosts the shared contract suite for
// service.SymbolIndex adapters plus the in-memory SymbolRoot the suite and
// adapter tests drive. Every adapter must pass the same checks: coverage of
// the agreed kind vocabulary, owner/line sanity, determinism, fail-soft on
// corrupt files, and the shared skip rules.
package symboltest

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol"
)

//go:embed testdata
var fixtureFS embed.FS

// MemRoot is a deterministic in-memory SymbolRoot.
type MemRoot struct {
	files map[string][]byte
}

// NewMemRoot builds a root from embedded fixtures plus any extra files.
// Extra paths let tests inject skip-rule and fail-soft cases.
func NewMemRoot(t *testing.T, extra map[string]string) *MemRoot {
	t.Helper()
	r := &MemRoot{files: map[string][]byte{}}
	err := fs.WalkDir(fixtureFS, "testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fixtureFS.ReadFile(p)
		if err != nil {
			return err
		}
		r.files[strings.TrimPrefix(p, "testdata/")] = b
		return nil
	})
	if err != nil {
		t.Fatalf("fixtures: %v", err)
	}
	for rel, body := range extra {
		r.files[rel] = []byte(body)
	}
	return r
}

func (r *MemRoot) Walk(_ context.Context, visit func(rel string, size int64) error) error {
	rels := make([]string, 0, len(r.files))
	for rel := range r.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if err := visit(rel, int64(len(r.files[rel]))); err != nil {
			return err
		}
	}
	return nil
}

func (r *MemRoot) ReadFile(_ context.Context, rel string) ([]byte, error) {
	b, ok := r.files[rel]
	if !ok {
		return nil, fmt.Errorf("memroot: no file %q", rel)
	}
	return b, nil
}

var _ svc.SymbolRoot = (*MemRoot)(nil)

// expect lists the (kind, name, public) triples every adapter must report
// for a fixture. Only the agreed-vocabulary subset is pinned; richer
// adapters may produce more. The public flag pins the shared visibility
// heuristic on exported and unexported cases alike.
var expect = map[string][][3]any{
	"fixture.go":    {{"struct", "Store", true}, {"interface", "Closer", true}, {"function", "OpenStore", true}, {"method", "Close", true}, {"struct", "cache", false}, {"function", "helper", false}},
	"fixture.js":    {{"class", "Queue", true}, {"function", "drain", true}, {"function", "internal", false}},
	"fixture.ts":    {{"interface", "Account", true}, {"class", "BaseRepo", true}, {"type", "ID", true}, {"module", "Auth", true}, {"function", "hashKey", false}, {"interface", "Local", false}},
	"fixture.tsx":   {{"interface", "CardProps", false}, {"function", "Card", true}, {"function", "Grid", true}},
	"fixture.py":    {{"class", "Engine", true}, {"function", "boot", true}, {"function", "_init_cache", false}},
	"fixture.rs":    {{"struct", "Config", true}, {"trait", "Store", true}, {"impl", "Config", false}, {"module", "inner", false}, {"macro", "make_config", false}, {"function", "load", true}},
	"Fixture.java":  {{"class", "Service", true}, {"interface", "Named", false}, {"record", "Point", true}, {"interface", "Tag", false}, {"method", "size", true}, {"class", "Helper", false}},
	"fixture.kt":    {{"class", "Session", true}, {"object", "Registry", true}, {"function", "start", true}, {"function", "cleanup", false}},
	"Fixture.cs":    {{"module", "Acme.Core", false}, {"class", "Worker", true}, {"record", "Job", true}, {"struct", "Point", true}, {"method", "Reset", false}},
	"fixture.rb":    {{"module", "Billing", true}, {"class", "Invoice", true}, {"method", "total", true}},
	"fixture.php":   {{"class", "Client", true}, {"trait", "HasLog", true}, {"function", "connect", true}, {"method", "secret", false}},
	"fixture.c":     {{"struct", "Buffer", true}, {"enum", "State", true}, {"function", "flush", true}, {"function", "helper", false}},
	"fixture.cc":    {{"module", "net", true}, {"class", "Socket", true}, {"struct", "Addr", true}, {"function", "bind", true}, {"function", "internal_fn", false}},
	"fixture.swift": {{"struct", "Frame", true}, {"class", "View", true}, {"interface", "Drawable", true}, {"function", "render", true}, {"function", "hidden", false}},
}

// Run exercises idx against the shared fixture corpus and the universal
// invariants. Adapters call it from their own tests:
//
//	symboltest.Run(t, func() svc.SymbolIndex { return regex.New(0) })
func Run(t *testing.T, factory func() svc.SymbolIndex) {
	t.Helper()
	ctx := context.Background()

	t.Run("Coverage", func(t *testing.T) {
		idx := factory()
		syms, err := idx.Index(ctx, NewMemRoot(t, nil))
		if err != nil {
			t.Fatalf("Index: %v", err)
		}
		got := map[[3]string]svc.Symbol{}
		for _, s := range syms {
			got[[3]string{s.Path, s.Kind, s.Name}] = s
			if s.Line <= 0 {
				t.Errorf("%s: %s %s has non-positive line %d", s.Path, s.Kind, s.Name, s.Line)
			}
		}
		for file, pairs := range expect {
			for _, p := range pairs {
				kind, name, pub := p[0].(string), p[1].(string), p[2].(bool)
				s, ok := got[[3]string{file, kind, name}]
				if !ok {
					t.Errorf("%s: missing %s %q", file, kind, name)
					continue
				}
				if s.Public != pub {
					t.Errorf("%s: %s %q public=%v want %v", file, kind, name, s.Public, pub)
				}
			}
		}
	})

	t.Run("Determinism", func(t *testing.T) {
		a, err := factory().Index(ctx, NewMemRoot(t, nil))
		if err != nil {
			t.Fatalf("first Index: %v", err)
		}
		b, err := factory().Index(ctx, NewMemRoot(t, nil))
		if err != nil {
			t.Fatalf("second Index: %v", err)
		}
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Fatal("two Index runs on identical roots differ")
		}
	})

	t.Run("FailSoft", func(t *testing.T) {
		idx := factory()
		bad := map[string]string{
			"broken.go":  "package x\nfunc broken( {\n\tif {\n",
			"broken.py":  "def x(:\nclass ((\n  @@@\n",
			"broken.ts":  "interface {\nconst = =>\nclass <<<\n",
			"notcode.go": "\x00\x01\x02\xff\xfe binary-ish",
		}
		syms, err := idx.Index(ctx, NewMemRoot(t, bad))
		if err != nil {
			t.Fatalf("Index with corrupt files: %v", err)
		}
		// the map must still carry symbols from healthy fixtures
		healthy := 0
		for _, s := range syms {
			if s.Path == "fixture.go" {
				healthy++
			}
		}
		if healthy < 3 {
			t.Fatalf("corrupt files poisoned the map: fixture.go produced %d symbols", healthy)
		}
	})

	t.Run("SkipRules", func(t *testing.T) {
		idx := factory()
		big := strings.Repeat("func f%d() {}\n", 40000)
		for i := 0; i < 40000; i++ {
			// cheap way to make distinct-but-valid-ish content
			_ = i
			break
		}
		skips := map[string]string{
			"vendor/dep/v.go":     "package dep\nfunc Vendored() {}\n",
			"node_modules/m/m.js": "function bundled() {}\n",
			".git/hooks/x.go":     "package hooks\nfunc GitHook() {}\n",
			"dist/bundle.ts":      "class Bundled {}\n",
			"target/gen.rs":       "fn generated() {}\n",
			"big/huge.go":         big,
			"empty/blank.go":      "",
		}
		syms, err := idx.Index(ctx, NewMemRoot(t, skips))
		if err != nil {
			t.Fatalf("Index: %v", err)
		}
		for _, s := range syms {
			for skip := range skips {
				if s.Path == skip {
					t.Errorf("skipped file %s produced symbol %s %s", skip, s.Kind, s.Name)
				}
			}
		}
	})

	t.Run("SkipPredicateSelfCheck", func(t *testing.T) {
		for _, tc := range []struct {
			rel  string
			size int64
			want bool
		}{
			{"a/b.go", 100, true},
			{"vendor/x/y.go", 100, false},
			{"node_modules/m.js", 100, false},
			{".git/HEAD", 100, false},
			{"src/.hidden/f.go", 100, false},
			{"dist/bundle.js", 100, false},
			{"big/f.go", symbol.MaxFileBytes + 1, false},
			{"ok/f.go", symbol.MaxFileBytes, true},
		} {
			if got := symbol.ShouldIndex(tc.rel, tc.size); got != tc.want {
				t.Errorf("ShouldIndex(%q,%d)=%v want %v", tc.rel, tc.size, got, tc.want)
			}
		}
	})
}

// RequireKindNames is a helper for adapter-specific tests that want the full
// (path,kind,name) set.
func RequireKindNames(t *testing.T, syms []svc.Symbol, file string, want [][2]string) {
	t.Helper()
	got := map[[2]string]bool{}
	for _, s := range syms {
		if s.Path == file || path.Base(s.Path) == file {
			got[[2]string{s.Kind, s.Name}] = true
		}
	}
	for _, p := range want {
		if !got[p] {
			t.Errorf("%s: missing %s %q", file, p[0], p[1])
		}
	}
}
