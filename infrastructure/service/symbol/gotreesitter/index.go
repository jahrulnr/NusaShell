//go:build repomap

// Package gotreesitter implements service.SymbolIndex on the embedded
// pure-Go tree-sitter engine (github.com/odvcencio/gotreesitter). It is one
// of several adapters behind the port; consumers never see its types.
//
// Build tags: the package compiles only with `-tags repomap`. Grammar
// coverage follows the upstream grammar_subset tags — with none set, the
// full registry is embedded; the pilot set is:
//
//	go build -tags 'repomap grammar_subset grammar_subset_go
//	    grammar_subset_javascript grammar_subset_typescript grammar_subset_tsx
//	    grammar_subset_python grammar_subset_rust grammar_subset_java
//	    grammar_subset_kotlin grammar_subset_c_sharp grammar_subset_ruby
//	    grammar_subset_php grammar_subset_c grammar_subset_cpp
//	    grammar_subset_swift' ./cmd/repomap
//
// Outline queries live in queries/<lang>.scm: complete replacements for the
// stock tag queries where v0.58.0 leaves gaps (TS type aliases, C# records,
// Rust impl containers, Java records, JS arrow-function bindings). A
// language with no file keeps the stock resolved query.
package gotreesitter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol"
)

//go:embed queries/*.scm
var queryFS embed.FS

// DefaultParseBudget bounds a single file parse; oversized inputs are
// already excluded by symbol.MaxFileBytes, this guards the pathological
// expansion cases (deep nesting, generated tables).
const DefaultParseBudget = int64(64 << 20)

// DefaultParseTimeoutMicros caps one file's parse so a single adversarial
// source cannot stall a sweep.
const DefaultParseTimeoutMicros = uint64(5_000_000)

// DefaultWorkers bounds concurrent parses; each worker pins a parser and a
// tree, so the cap is also the in-flight memory bound.
const DefaultWorkers = 4

// DefaultMemLimitBytes and DefaultGCPercent bound the process heap the
// sweep may grow. They are applied in New and are process-wide — the same
// reason they belong on a dev-tool adapter rather than a shared library.
// GOMEMLIMIT counts heap only; VmHWM lands ~90-150 MB above it from
// non-heap reservations.
const DefaultMemLimitBytes = int64(220 << 20)
const DefaultGCPercent = 60

// Options tune an Index. Engines are configured per adapter; consumers only
// see service.SymbolIndex.
type Options struct {
	ParseBudget      int64
	ParseTimeoutUsec uint64
	Workers          int
	// MemLimitBytes is applied through debug.SetMemoryLimit — a
	// process-wide heap bound shared with the rest of the binary. Zero
	// selects DefaultMemLimitBytes; negative disables the bound.
	MemLimitBytes int64
	// GCPercent is applied through debug.SetGCPercent for the same reason.
	// Zero selects DefaultGCPercent; negative disables the override.
	GCPercent int
	// MaxFileBytes overrides the shared skip policy's size cap; zero
	// selects symbol.MaxFileBytes.
	MaxFileBytes int64
}

// Index is the gotreesitter-backed SymbolIndex. It caches parsed symbols by
// content hash per root so repeated Index calls only re-parse changed files.
type Index struct {
	opts    Options
	mu      sync.Mutex
	engines map[string]*langEngine
	cache   map[svc.SymbolRoot]*rootCache
}

type langEngine struct {
	entry    *grammars.LangEntry
	lang     *gotreesitter.Language
	outliner *gotreesitter.Outliner
	parsers  sync.Pool // *gotreesitter.Parser
	budget   int64
	timeout  uint64
}

type rootCache struct {
	hash map[string][32]byte
	syms map[string][]svc.Symbol
}

// New returns the adapter ready to index.
func New(opts Options) *Index {
	if opts.ParseBudget <= 0 {
		opts.ParseBudget = DefaultParseBudget
	}
	if opts.ParseTimeoutUsec <= 0 {
		opts.ParseTimeoutUsec = DefaultParseTimeoutMicros
	}
	if opts.Workers <= 0 {
		opts.Workers = DefaultWorkers
	}
	if opts.MemLimitBytes == 0 {
		opts.MemLimitBytes = DefaultMemLimitBytes
	}
	if opts.GCPercent == 0 {
		opts.GCPercent = DefaultGCPercent
	}
	if opts.MemLimitBytes > 0 {
		debug.SetMemoryLimit(opts.MemLimitBytes)
	}
	if opts.GCPercent > 0 {
		debug.SetGCPercent(opts.GCPercent)
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = symbol.MaxFileBytes
	}
	return &Index{
		opts:    opts,
		engines: map[string]*langEngine{},
		cache:   map[svc.SymbolRoot]*rootCache{},
	}
}

var _ svc.SymbolIndex = (*Index)(nil)
var _ svc.SymbolIndexAvailability = (*Index)(nil)
var _ svc.SymbolIndexScope = (*Index)(nil)
var _ svc.SymbolIndexDetail = (*Index)(nil)

// Available always succeeds — the engine is embedded in-process and needs
// nothing from the root beyond bytes.
func (i *Index) Available(svc.SymbolRoot) error { return nil }

// IndexesFile reports whether the grammar registry claims rel — the same
// detection the indexer applies per file, exposed so a consumer's change
// detector and skip report see the same boundary.
func (i *Index) IndexesFile(rel string) bool {
	return grammars.DetectLanguage(rel) != nil
}

// Index walks the root, parses every indexable source file, and returns the
// flattened symbols grouped by path in lexical order. Per-file failures
// (read error, parse blow-up) are skipped, never fatal.
func (i *Index) Index(ctx context.Context, root svc.SymbolRoot) ([]svc.Symbol, error) {
	syms, _, _, err := i.IndexDetail(ctx, root)
	return syms, err
}

// IndexDetail is Index plus coverage: how many of the walk's files were
// parsed before a deadline cut the batch. A canceled context still
// returns the completed files' symbols along with the error.
func (i *Index) IndexDetail(ctx context.Context, root svc.SymbolRoot) ([]svc.Symbol, int, int, error) {
	var files []string
	if err := root.Walk(ctx, func(rel string, size int64) error {
		if !symbol.ShouldIndexWithin(rel, size, i.opts.MaxFileBytes) {
			return nil
		}
		if grammars.DetectLanguage(rel) == nil {
			return nil
		}
		files = append(files, rel)
		return nil
	}); err != nil {
		return nil, 0, 0, err
	}
	symbol.OrderFiles(files)

	i.mu.Lock()
	prev := i.cache[root]
	next := &rootCache{hash: map[string][32]byte{}, syms: map[string][]svc.Symbol{}}
	i.mu.Unlock()

	// Files are processed by a worker pool — a parse is CPU-bound and
	// workspace reads can block on remote roots — but every result lands in
	// its sorted slot, so output stays deterministic.
	workers := min(i.opts.Workers, len(files))
	if workers < 1 {
		workers = 1
	}
	results := make([][]svc.Symbol, len(files))
	hashes := make([][32]byte, len(files))
	done := make(chan struct{})
	var doneCount atomic.Int64
	go func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(200 * time.Millisecond):
				if doneCount.Load() > 32 {
					debug.FreeOSMemory()
					doneCount.Store(0)
				}
			}
		}
	}()
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				rel := files[n]
				src, err := root.ReadFile(ctx, rel)
				if err != nil {
					continue
				}
				sum := sha256.Sum256(src)
				hashes[n] = sum
				if prev != nil && prev.hash[rel] == sum {
					results[n] = prev.syms[rel]
					continue
				}
				results[n] = i.parseFile(rel, src)
				doneCount.Add(1)
			}
		}()
	}
feed:
	for n := range files {
		select {
		case <-ctx.Done():
			break feed
		case jobs <- n:
		}
	}
	close(jobs)
	wg.Wait()
	close(done)
	debug.FreeOSMemory()

	var out []svc.Symbol
	indexed := 0
	for n, rel := range files {
		if results[n] == nil && hashes[n] == [32]byte{} {
			continue // unread or unprocessed
		}
		indexed++
		next.hash[rel] = hashes[n]
		next.syms[rel] = results[n]
		out = append(out, results[n]...)
	}

	i.mu.Lock()
	i.cache[root] = next
	i.mu.Unlock()
	// A canceled index still hands back what it processed — the caller
	// marks the map as capped rather than throwing the partial work away.
	return out, indexed, len(files), ctx.Err()
}

func (i *Index) engine(name string) *langEngine {
	i.mu.Lock()
	defer i.mu.Unlock()
	if e, ok := i.engines[name]; ok {
		return e
	}
	entry := grammars.DetectLanguageByName(name)
	if entry == nil {
		return nil
	}
	lang := entry.Language()
	query := grammars.ResolveTagsQuery(*entry)
	if patch, err := queryFS.ReadFile("queries/" + name + ".scm"); err == nil {
		query = string(patch)
	}
	outliner, err := gotreesitter.NewOutliner(lang, query,
		gotreesitter.WithOutlineOwnerRules(grammars.OutlineOwnerRules(*entry)))
	if err != nil {
		return nil
	}
	e := &langEngine{
		entry:    entry,
		lang:     lang,
		outliner: outliner,
		budget:   i.opts.ParseBudget,
		timeout:  i.opts.ParseTimeoutUsec,
	}
	e.parsers.New = func() any {
		p := gotreesitter.NewParser(lang)
		p.SetMemoryBudgetBytes(e.budget)
		p.SetTimeoutMicros(e.timeout)
		return p
	}
	i.engines[name] = e
	return e
}

// parseFile runs one source through its language engine and flattens the
// outline into domain symbols. It never reports an error — fail-soft is the
// contract — it returns what it could extract.
func (i *Index) parseFile(rel string, src []byte) []svc.Symbol {
	entry := grammars.DetectLanguage(rel)
	if entry == nil {
		return nil
	}
	e := i.engine(entry.Name)
	if e == nil {
		return nil
	}
	p := e.parsers.Get().(*gotreesitter.Parser)
	var tree *gotreesitter.Tree
	var err error
	if e.entry.TokenSourceFactory != nil {
		tree, err = p.ParseWithTokenSource(src, e.entry.TokenSourceFactory(src, e.lang))
	} else {
		tree, err = p.Parse(src)
	}
	// A parser's scratch arena grows to the largest file it has seen; a
	// parser that just chewed a big input is dropped rather than retained in
	// the pool, which is what bounds resident memory on mixed-size sweeps.
	if len(src) <= 64<<10 {
		e.parsers.Put(p)
	}
	if tree == nil {
		return nil
	}
	bt := gotreesitter.Bind(tree)
	defer bt.Release()
	_ = err // partial trees still carry symbols; strict parse errors stay soft
	syms, _ := e.outliner.OutlineBound(bt)
	var out []svc.Symbol
	flatten(syms, "", rel, entry.Name, &out)
	markPublic(out, entry.Name, src)
	return out
}

// markPublic applies the shared visibility heuristic to each symbol using
// its definition line, plus the grouped `export { … }` pass for JS-family
// languages where a name's export lives elsewhere in the file.
func markPublic(syms []svc.Symbol, lang string, src []byte) {
	var lines [][]byte
	var exported map[string]bool
	for k := range syms {
		s := &syms[k]
		if lines == nil {
			lines = bytes.Split(src, []byte{'\n'})
		}
		defLine := ""
		if s.Line-1 < len(lines) {
			defLine = string(lines[s.Line-1])
		}
		s.Public = symbol.Visibility(lang, defLine, s.Name)
		if !s.Public && (lang == "javascript" || lang == "typescript" || lang == "tsx") {
			if exported == nil {
				exported = symbol.ExportedNames(src)
			}
			if exported[s.Name] {
				s.Public = true
			}
		}
	}
}

func flatten(in []gotreesitter.OutlineSymbol, parent, rel, lang string, out *[]svc.Symbol) {
	for _, s := range in {
		owner := s.Owner
		if owner == "" {
			owner = parent
		}
		*out = append(*out, svc.Symbol{
			Path: rel, Lang: lang, Kind: s.Kind,
			Name: s.Name, Owner: owner,
			Line: int(s.Range.StartPoint.Row) + 1,
		})
		flatten(s.Children, s.Name, rel, lang, out)
	}
}
