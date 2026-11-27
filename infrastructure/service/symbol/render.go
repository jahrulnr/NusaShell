package symbol

import (
	"fmt"
	"sort"
	"strings"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// MapKindSet is the whitelist of symbol kinds the rendered map shows. Detail
// kinds (fields, constants, enum members, variables) are parsed but left out
// of the map — they dominate token cost without orienting a reader.
var MapKindSet = map[string]bool{
	"class": true, "interface": true, "struct": true, "type": true,
	"enum": true, "trait": true, "record": true, "module": true,
	"impl": true, "object": true, "function": true, "method": true,
	"constructor": true, "macro": true,
}

// MaxSymbolsPerFile caps lines a single file contributes, so one dense file
// cannot eat the whole budget.
const MaxSymbolsPerFile = 48

// CharsPerToken is the rough map estimate; the renderer budgets against it.
const CharsPerToken = 4

// Policy is the complete, deterministic selection+ordering rule for one
// rendered map. The zero value is pure lexical (policy A) — files in
// lexical order until the budget runs out. Every knob is resolved before
// rendering, so output for the same symbols+policy is byte-stable, which is
// what hydration dedup needs.
type Policy struct {
	// Budget in approximate tokens (chars/BudgetPerToken). 0 = unlimited.
	Budget int
	// KindSet whitelists rendered kinds; nil = MapKindSet.
	KindSet map[string]bool
	// DirQuota caps one top-level directory's share of Budget (0 = off).
	// Reserves are proportional to file count, capped at DirQuota; the
	// unspent remainder forms a pool overflow dirs draw on in lexical order.
	DirQuota float64
	// PublicFirst ranks exported symbols before private ones inside each
	// file — the outward API survives truncation. (Ranking files by public
	// count was measured and rejected: it front-loads large API files that
	// drain a directory's share and costs more hits than it buys.)
	PublicFirst bool
	// ShallowFirst orders files inside a directory by path depth before
	// lexical order — package roots before deep internals.
	ShallowFirst bool
	// MaxPerFile caps symbols per file; 0 = MaxSymbolsPerFile.
	MaxPerFile int
	// KindFirst ranks container/type kinds (class, interface, struct, enum,
	// trait, record, module, impl, object) before callable kinds (function,
	// method, constructor) inside each file — the file's shape before its
	// verbs.
	KindFirst bool
}

// kindTier groups kinds for KindFirst ordering; lower renders earlier.
func kindTier(k string) int {
	switch k {
	case "module", "class", "interface", "struct", "enum", "trait", "record", "impl", "object", "type":
		return 0
	case "function", "method", "constructor":
		return 1
	}
	return 2
}

// DefaultPolicy is the selection policy Render uses; the numbers come from
// the selection benchmark (cmd/repomap -bench), not taste: the 0.5 dir quota
// maximized hit-rate at the recommended budget on both benchmark repos, a
// 12-symbol file cap buys breadth (more files per token), and public-first /
// shallow-first were measured neutral — kept because they cost nothing and
// keep truncated files API-forward.
var DefaultPolicy = Policy{
	DirQuota:     0.5,
	PublicFirst:  true,
	ShallowFirst: true,
	MaxPerFile:   12,
}

type fileBlock struct {
	path   string
	topDir string
	depth  int
	syms   []svc.Symbol
}

// Render produces the bounded symbol map under DefaultPolicy.
func Render(syms []svc.Symbol, budgetTokens int) string {
	p := DefaultPolicy
	p.Budget = budgetTokens
	return RenderPolicy(syms, p)
}

// RenderPolicy renders syms under an explicit policy. Symbols are grouped by
// path and emitted in source order; `Owner.Name` is rendered for owned
// symbols. When the budget runs out, what remains is summarized in one
// truncation footer instead of being dropped silently.
func RenderPolicy(syms []svc.Symbol, pol Policy) string {
	text, _ := RenderDetail(syms, pol)
	return text
}

// RenderDetail is RenderPolicy plus the emitted symbols in render order —
// the benchmark needs the emission itself for hit-rate and rank metrics.
func RenderDetail(syms []svc.Symbol, pol Policy) (string, []svc.Symbol) {
	kindSet := pol.KindSet
	if kindSet == nil {
		kindSet = MapKindSet
	}
	maxPerFile := pol.MaxPerFile
	if maxPerFile <= 0 {
		maxPerFile = MaxSymbolsPerFile
	}
	budget := pol.Budget * CharsPerToken

	// group by file, preserving source order; public-first is a stable sort
	// inside the file so API declarations outrank helpers on a tight budget
	byFile := map[string][]svc.Symbol{}
	var order []string
	for _, s := range syms {
		if !kindSet[s.Kind] {
			continue
		}
		if _, ok := byFile[s.Path]; !ok {
			order = append(order, s.Path)
		}
		byFile[s.Path] = append(byFile[s.Path], s)
	}

	files := make([]fileBlock, 0, len(order))
	for _, p := range order {
		ss := append([]svc.Symbol(nil), byFile[p]...)
		if pol.PublicFirst || pol.KindFirst {
			sort.SliceStable(ss, func(a, b int) bool {
				if pol.PublicFirst && ss[a].Public != ss[b].Public {
					return ss[a].Public
				}
				if pol.KindFirst {
					if ta, tb := kindTier(ss[a].Kind), kindTier(ss[b].Kind); ta != tb {
						return ta < tb
					}
				}
				return false
			})
		}
		depth := strings.Count(p, "/")
		top := p
		if before, _, ok := strings.Cut(p, "/"); ok {
			top = before
		}
		files = append(files, fileBlock{path: p, topDir: top, depth: depth, syms: ss})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })

	// order blocks: pure lexical, or dir-major with the dir's own ordering
	var blocks []fileBlock
	if pol.DirQuota <= 0 && !pol.ShallowFirst {
		blocks = files
	} else {
		dirs := map[string][]fileBlock{}
		var dirNames []string
		for _, f := range files {
			if _, ok := dirs[f.topDir]; !ok {
				dirNames = append(dirNames, f.topDir)
			}
			dirs[f.topDir] = append(dirs[f.topDir], f)
		}
		sort.Strings(dirNames)
		for _, d := range dirNames {
			fs := dirs[d]
			if pol.ShallowFirst {
				sort.SliceStable(fs, func(i, j int) bool {
					if fs[i].depth != fs[j].depth {
						return fs[i].depth < fs[j].depth
					}
					return fs[i].path < fs[j].path
				})
			}
			blocks = append(blocks, fs...)
		}
	}

	// per-dir reserve: proportional to file count, capped at DirQuota share;
	// the pool holds every dir's unspent reserve plus unlimited-budget slack
	type dirCap struct {
		reserve int
		used    int
	}
	caps := map[string]*dirCap{}
	if pol.DirQuota > 0 && budget > 0 {
		total := len(files)
		capChars := int(pol.DirQuota * float64(budget))
		for _, f := range files {
			c := caps[f.topDir]
			if c == nil {
				c = &dirCap{}
				caps[f.topDir] = c
			}
		}
		for d, c := range caps {
			share := min(budget*len(filesInDir(d, files))/total, capChars)
			c.reserve = share
		}
	}

	var b strings.Builder
	used := 0
	rendered := map[string]bool{}
	var emitted []svc.Symbol
	var overflow []fileBlock
	emit := func(f fileBlock, capLeft int) (int, bool) {
		shown := f.syms
		hidden := 0
		if len(shown) > maxPerFile {
			hidden = len(shown) - maxPerFile
			shown = shown[:maxPerFile]
		}
		var fb strings.Builder
		fmt.Fprintf(&fb, "%s\n", f.path)
		for _, s := range shown {
			name := s.Name
			if s.Owner != "" {
				name = s.Owner + "." + name
			}
			fmt.Fprintf(&fb, "  %-11s %s :%d\n", s.Kind, name, s.Line)
		}
		if hidden > 0 {
			fmt.Fprintf(&fb, "  … +%d more\n", hidden)
		}
		if capLeft >= 0 && fb.Len() > capLeft {
			return 0, false
		}
		if budget > 0 && used+fb.Len() > budget {
			return 0, false
		}
		used += fb.Len()
		b.WriteString(fb.String())
		rendered[f.path] = true
		emitted = append(emitted, shown...)
		return fb.Len(), true
	}

	for _, f := range blocks {
		if budget > 0 && used >= budget {
			break
		}
		capLeft := -1
		if c := caps[f.topDir]; c != nil {
			capLeft = c.reserve - c.used
		}
		n, ok := emit(f, capLeft)
		if !ok {
			overflow = append(overflow, f)
			continue
		}
		if c := caps[f.topDir]; c != nil {
			c.used += n
		}
	}
	// overflow dirs draw on the global remainder in the same order
	for _, f := range overflow {
		if budget > 0 && used >= budget {
			break
		}
		if _, ok := emit(f, -1); !ok {
			break
		}
	}

	omittedFiles, omittedSyms := 0, 0
	for _, f := range files {
		if !rendered[f.path] {
			omittedFiles++
			omittedSyms += len(f.syms)
		}
	}
	if omittedFiles > 0 {
		fmt.Fprintf(&b, "… %d more files, %d symbols omitted (budget)\n", omittedFiles, omittedSyms)
	}
	return b.String(), emitted
}

func filesInDir(top string, files []fileBlock) []fileBlock {
	var out []fileBlock
	for _, f := range files {
		if f.topDir == top {
			out = append(out, f)
		}
	}
	return out
}
