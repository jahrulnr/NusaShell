package service

import (
	"context"
	"errors"
)

// Symbol is one language-neutral declaration extracted from a workspace
// source file: a class, function, method, type, or similar definition. The
// fields are the projection a repo/symbol map renders — Path is relative to
// the indexed root, Line is 1-based, and Owner is the enclosing or receiver
// type when the adapter can prove one (a Go method's receiver, a Rust impl's
// target type, a Java nested class's outer class); otherwise empty.
//
// Public marks the adapter's best-effort answer to "is this part of the
// outward API": Go capitalization, JS/TS `export`, Rust `pub`, C-family
// modifiers, Python's `_` convention. Adapters fill it per language; where
// a language has no usable signal they report false rather than guess.
//
// Symbol is deliberately free of engine types: an adapter emitting tree-
// sitter nodes, ctags lines, or regex hits must all normalize to this shape.
type Symbol struct {
	Path   string `json:"path"`
	Lang   string `json:"lang"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Owner  string `json:"owner,omitempty"`
	Line   int    `json:"line"`
	Public bool   `json:"public"`
}

// SymbolRoot abstracts one workspace tree for indexing: Walk yields each
// file path relative to the root with its byte size, ReadFile returns file
// contents by that same relative path. Implementations may be local or may
// proxy another machine (SSH/Docker drivers) — adapters must not assume a
// host filesystem, only this contract. Walk order should be deterministic;
// adapters re-sort regardless.
//
// A SymbolRoot does not have to pre-filter; adapters apply the shared skip
// rules (dependency folders, oversized files) themselves.
type SymbolRoot interface {
	Walk(ctx context.Context, visit func(rel string, size int64) error) error
	ReadFile(ctx context.Context, rel string) ([]byte, error)
}

// ErrSymbolUnsupported is the sentinel an adapter returns when it cannot
// index a given root at all — for example a ctags-class adapter that must
// spawn an external process inside the workspace environment cannot serve a
// root with no command execution. Consumers should fall back rather than
// treat it as an indexing failure.
var ErrSymbolUnsupported = errors.New("symbol index unsupported for this workspace root")

// SymbolIndex produces a workspace-level symbol map. One operation covers
// the whole root; per-file failures are skipped, never fatal. Implementations
// may keep their own cache and refresh policy — callers do not observe them;
// a second Index on the same root is the refresh path.
//
// Ordering: implementations return symbols grouped by Path (lexical) and
// source order so output is deterministic across runs.
type SymbolIndex interface {
	Index(ctx context.Context, root SymbolRoot) ([]Symbol, error)
}

// SymbolIndexAvailability is the optional capability a SymbolIndex
// implements when availability depends on the root. It exists for adapters
// that need more than bytes — a ctags adapter has to exec a binary inside
// the workspace environment and returns ErrSymbolUnsupported (or a wrapped
// diagnostic) when the root cannot provide that. In-process adapters may
// implement it trivially (nil) or not at all: absence means "always
// available". Consumers probe with a type assertion:
//
//	if p, ok := idx.(SymbolIndexAvailability); ok {
//		if err := p.Available(root); err != nil { /* fall back */ }
//	}
type SymbolIndexAvailability interface {
	Available(root SymbolRoot) error
}

// SymbolIndexScope is the optional contract an adapter implements when it
// can say up front which files it will index — the gotreesitter engine
// answers through its grammar registry's extension detection. Consumers
// use it to hash only files that can change the map (a README edit then
// never triggers a rebuild) and to report the skipped remainder.
type SymbolIndexScope interface {
	// IndexesFile reports whether rel (a path that already passed the
	// shared skip policy) is a source file this adapter can extract
	// symbols from.
	IndexesFile(rel string) bool
}

// SymbolIndexDetail is the optional contract an adapter implements when
// it can report build coverage — how many of the walk's files were
// actually processed before the result was cut (a context deadline, a
// fault). Consumers use it to render an honest "partial (N of M files)"
// footer and to decide whether a continuation build should run. Absence
// means the caller treats every result as complete.
type SymbolIndexDetail interface {
	IndexDetail(ctx context.Context, root SymbolRoot) (symbols []Symbol, indexedFiles, totalFiles int, err error)
}

// CachedIndex is the serialized form of one index result: the file-list
// hash it was built from plus the extracted symbols. Persistence adapters
// (a JSON store under the data dir, a future remote cache) save and load
// this shape; Root is informational only — the cache key is separate.
// Files/Total/Partial carry the coverage of a truncated build so a
// reloaded cache can still render an honest partial and resume it.
type CachedIndex struct {
	Root     string   `json:"root"`
	FileHash string   `json:"file_hash"`
	Symbols  []Symbol `json:"symbols"`
	Files    int      `json:"files,omitempty"`
	Total    int      `json:"total,omitempty"`
	Partial  bool     `json:"partial,omitempty"`
}
