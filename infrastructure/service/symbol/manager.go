// Package symbol's manager owns the symbol map's lifecycle behind the
// hydration record: it detects file-list changes per workspace root,
// runs index builds off the calling turn, and renders the bounded map.
package symbol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Options carries the live tuning the symbols config section controls.
// The manager reads it through the injected getter on every serve, so an
// operator's `config set` reaches the next build/render without restart.
type Options struct {
	BudgetTokens int
	Workers      int
	MaxFileBytes int64
}

// CacheStore persists one index per root key. The JSON store under
// <storage>/symbols implements it; tests may substitute a map.
type CacheStore interface {
	Load(rootKey string) (svc.CachedIndex, bool)
	Save(rootKey string, idx svc.CachedIndex) error
}

// Manager serves bounded symbol maps for any number of workspace roots.
// It is shared process-wide so a second conversation on the same root
// never pays for a build it can read. Safe for concurrent use.
type Manager struct {
	opts    func() Options
	factory func(Options) svc.SymbolIndex
	store   CacheStore

	// Bounds as injectable knobs — the constants in guard.go are the
	// shipped values; tests shrink the timeouts.
	probeTimeout time.Duration
	indexTimeout time.Duration
	skipTTL      time.Duration
	now          func() time.Time

	buildSem chan struct{} // global build semaphore — one index at a time

	mu     sync.Mutex
	idx    svc.SymbolIndex // built lazily; rebuilt when its opts change
	idxOpt Options
	roots  map[string]*rootState
}

type rootState struct {
	ready    bool   // a build (or disk load) produced symbols for fileHash
	fileHash string // hash of the file list the cached symbols describe
	text     string // last rendered record — may lag fileHash during a rebuild
	symbols  []svc.Symbol
	probe    probeResult // gate counts from the winning probe
	files    int         // files the last build actually indexed
	total    int         // files the last build should have indexed
	indexCap bool        // the build hit IndexTimeout — footer marks it
	diskRead bool        // store.Load attempted for this key

	// desired/desiredProbe name the newest file hash the builder owes;
	// building serializes the goroutine that pays it. partial tracks a
	// truncated result whose continuation should keep building.
	desired      string
	desiredProbe probeResult
	building     bool
	partial      bool
	waitCh       chan struct{} // closed when a build commits — MapSync parks on it

	skipUntil  time.Time // gate verdict TTL — zero means "go probe"
	skipReason string
}

// NewManager wires the lifecycle: factory builds the symbol index —
// swapping gotreesitter for another engine is this one argument — opts
// supplies live tuning, and store is the on-disk cache (nil keeps the
// cache in memory only).
func NewManager(factory func(Options) svc.SymbolIndex, opts func() Options, store CacheStore) *Manager {
	return &Manager{
		opts: opts, factory: factory, store: store, roots: map[string]*rootState{},
		probeTimeout: ProbeTimeout, indexTimeout: IndexTimeout, skipTTL: SkipCacheTTL,
		now: time.Now, buildSem: make(chan struct{}, 1),
	}
}

// index returns the shared adapter, rebuilding it when the tuning knobs
// it was constructed with no longer match the live options.
func (m *Manager) index(opts Options) svc.SymbolIndex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idx == nil || m.idxOpt != opts {
		m.idx = m.factory(opts)
		m.idxOpt = opts
	}
	return m.idx
}

// Map returns the record text for the root opened by open. Serving is
// stale-while-revalidate: once a map exists, a changed file list keeps
// returning it byte-identically while the background build catches up —
// the "being built" marker only ever stands in for a first build.
func (m *Manager) Map(ctx context.Context, key, label string, open func(context.Context) (svc.SymbolRoot, error)) string {
	opts := m.opts()

	m.mu.Lock()
	st := m.roots[key]
	if st == nil {
		st = &rootState{}
		m.roots[key] = st
	}
	if st.skipUntil.After(m.now()) {
		reason := st.skipReason
		m.mu.Unlock()
		return skipText(label, reason)
	}
	m.mu.Unlock()

	idx := m.index(opts)
	root, err := open(ctx)
	if err != nil {
		return skipText(label, "workspace unreadable: "+err.Error())
	}
	defer closeQuiet(root)
	scope, _ := idx.(svc.SymbolIndexScope)
	pr, err := probe(ctx, root, scope, opts.MaxFileBytes, m.probeTimeout)
	if err != nil {
		return skipText(label, "workspace walk failed: "+err.Error())
	}
	switch {
	case pr.truncated == "time cap":
		return m.setSkip(key, label, "probe exceeded the "+m.probeTimeout.String()+" walk limit")
	case !pr.marker && pr.indexable > GateFileLimit:
		return m.setSkip(key, label, fmt.Sprintf("is not a project root (no marker, >%d files)", GateFileLimit))
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case st.ready && st.fileHash == pr.hash && st.text != "":
		// The dominant path — nothing changed, return the same bytes.
		return st.text
	case st.fileHash == pr.hash && st.ready:
		// The build just committed; render once, then the case above
		// serves the identical string every pass after.
		st.text = mapText(label, st.symbols, st.probe, st.files, st.total, st.indexCap, opts.BudgetTokens)
		return st.text
	case st.text == "" && !st.diskRead:
		// First-ever serve for this root: try the disk cache before
		// paying for a build.
		st.diskRead = true
		if m.store != nil {
			if cached, ok := m.store.Load(key); ok && cached.FileHash == pr.hash {
				st.fileHash, st.symbols, st.probe = cached.FileHash, cached.Symbols, pr
				st.probe.indexable = cached.Files
				st.files, st.total, st.ready = cached.Files, cached.Total, true
				st.partial = cached.Partial
				st.text = mapText(label, st.symbols, st.probe, st.files, st.total, cached.Partial, opts.BudgetTokens)
				if !cached.Partial {
					return st.text
				}
				// A partial cache still serves — and earns a continuation.
			}
		}
		fallthrough
	default:
		// Dirty (or first) hash: queue the build the builder owes —
		// desired moves forward with every change so only the freshest
		// file list is paid for.
		st.desired, st.desiredProbe = pr.hash, pr
		if st.waitCh == nil {
			st.waitCh = make(chan struct{})
		}
		if !st.building {
			st.building = true
			m.spawnBuild(key, label, opts, idx, open)
		}
		if st.text != "" {
			// Stale-while-revalidate: the old map stays until its
			// replacement is complete.
			return st.text
		}
		if pr.skippedLang > 0 {
			return buildingText(label) + fmt.Sprintf("\n… %d files skipped: unsupported language", pr.skippedLang)
		}
		return buildingText(label)
	}
}

// MapLine is the compact extra-folder form: one line, zero probing.
// Numbers come from whatever this process already knows — a folder that
// was once a workspace shows its last coverage; a never-indexed folder
// just offers the map on demand.
func (m *Manager) MapLine(_, label string) string {
	m.mu.Lock()
	st := m.roots[label]
	m.mu.Unlock()
	if st == nil || !st.ready {
		return "+ " + label + " — map on demand"
	}
	return fmt.Sprintf("+ %s — %d files, %s symbols · map on demand", label, st.files, compact(len(st.symbols)))
}

// MapSync is Map for callers that want the map now — the agent's
// on-demand path. It parks on the pending build's commit channel and
// re-serves, so a cold folder answers with a real map inside this call
// instead of a "being built" note. The wait is bounded by ctx plus a
// hard ceiling; on expiry the caller gets the honest still-building
// line and can retry cheaply.
func (m *Manager) MapSync(ctx context.Context, key, label string, open func(context.Context) (svc.SymbolRoot, error)) string {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	for {
		out := m.Map(ctx, key, label, open)
		if !strings.Contains(out, "is being built") {
			return out
		}
		m.mu.Lock()
		var ch chan struct{}
		if st := m.roots[key]; st != nil {
			ch = st.waitCh
		}
		m.mu.Unlock()
		if ch == nil {
			return out
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return out + "\n… index still building — call again shortly"
		case <-deadline.C:
			return out + "\n… index still building — call again shortly"
		}
	}
}

// compact renders 2.8k-style counts.
func compact(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// setSkip caches the gate verdict for skipTTL and returns the one-line
// record — the tool still renders every pass, but a skipped root costs
// this one line and zero walks.
func (m *Manager) setSkip(key, label, reason string) string {
	m.mu.Lock()
	if st := m.roots[key]; st != nil {
		st.skipUntil, st.skipReason = m.now().Add(m.skipTTL), reason
	}
	m.mu.Unlock()
	return skipText(label, reason)
}

// spawnBuild runs the serialized builder loop for key. One goroutine at
// a time holds buildSem across all roots — parallel parses were what
// pushed concurrent builds into the time cap. The loop keeps working
// until the newest desired hash is fully indexed: a changed file list
// retargets it, and a time-capped result continues where the adapter's
// content cache left off, up to a stall guard.
func (m *Manager) spawnBuild(key, label string, opts Options, idx svc.SymbolIndex, open func(context.Context) (svc.SymbolRoot, error)) {
	go func() {
		m.buildSem <- struct{}{}
		defer func() { <-m.buildSem }()
		prevFiles := -1
		for {
			m.mu.Lock()
			st := m.roots[key]
			target, tpr := st.desired, st.desiredProbe
			m.mu.Unlock()

			var syms []svc.Symbol
			files, total, indexCap := 0, tpr.indexable, false
			bctx, cancel := context.WithTimeout(context.Background(), m.indexTimeout)
			root, err := open(bctx)
			if err == nil {
				scope, _ := idx.(svc.SymbolIndexScope)
				br := boundedRoot{SymbolRoot: root, scope: scope, maxBytes: opts.MaxFileBytes}
				var ierr error
				if detail, ok := idx.(svc.SymbolIndexDetail); ok {
					syms, files, total, ierr = detail.IndexDetail(bctx, br)
				} else {
					syms, ierr = idx.Index(bctx, br)
					files = total
				}
				indexCap = errors.Is(ierr, context.DeadlineExceeded)
				closeQuiet(root)
			}
			cancel()

			m.mu.Lock()
			if st = m.roots[key]; st != nil && st.desired != "" {
				st.fileHash, st.symbols, st.probe = target, syms, tpr
				st.files, st.total, st.indexCap = files, total, indexCap
				st.partial = indexCap || (total > 0 && files < total)
				st.ready = true
				// Render at commit: every landed build becomes a servable
				// baseline, so a churning tree can never pin the record
				// on "being built" while newer hashes keep retargeting.
				st.text = mapText(label, syms, tpr, files, total, indexCap, opts.BudgetTokens)
				if st.waitCh != nil {
					close(st.waitCh)
					st.waitCh = nil
				}
				if m.store != nil {
					m.store.Save(key, svc.CachedIndex{
						Root: label, FileHash: target, Symbols: syms,
						Files: files, Total: total, Partial: st.partial,
					})
				}
				switch {
				case st.desired != target:
					// A newer file list arrived mid-build — loop for it.
				case st.partial && files > prevFiles:
					// Truncated but progressing — continue the coverage.
					prevFiles = files
				default:
					st.building = false
					m.mu.Unlock()
					return
				}
				m.mu.Unlock()
				continue
			}
			m.mu.Unlock()
			return
		}
	}()
}

func skipText(label, reason string) string {
	return "symbol map skipped: " + label + " " + reason
}

func buildingText(label string) string {
	return "symbol map for " + label + " is being built — it will appear on a later turn."
}

// mapText renders the record: the fixed header first (so its shape
// survives truncation), then the bounded map and every applicable
// footer — budget omission inside RenderDetail, then the guard's own
// partial coverage, file cap, time cap, depth cap, and
// unsupported-language lines.
func mapText(label string, syms []svc.Symbol, pr probeResult, files, total int, indexCap bool, budget int) string {
	pol := DefaultPolicy
	pol.Budget = budget
	text, _ := RenderDetail(syms, pol)
	var b strings.Builder
	b.WriteString("symbol map for " + label + " — declarations only, public first; pick the file here, then read or search it.\n")
	b.WriteString(text)
	if files < total {
		fmt.Fprintf(&b, "… partial (%d of %d files)\n", files, total)
	}
	if pr.truncated == "file cap" {
		fmt.Fprintf(&b, "… truncated: file cap (%d limit)\n", MaxIndexedFiles)
	}
	if indexCap {
		fmt.Fprintf(&b, "… truncated: time cap (%s index)\n", IndexTimeout)
	}
	if pr.skippedDepth > 0 {
		fmt.Fprintf(&b, "… %d files skipped: depth cap\n", pr.skippedDepth)
	}
	if pr.skippedLang > 0 {
		fmt.Fprintf(&b, "… %d files skipped: unsupported language\n", pr.skippedLang)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func closeQuiet(root svc.SymbolRoot) {
	if c, ok := root.(io.Closer); ok {
		c.Close()
	}
}
