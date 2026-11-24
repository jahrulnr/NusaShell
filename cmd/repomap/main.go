//go:build repomap

// repomap is a dev tool that prints the bounded repo/symbol map for a
// workspace. It is deliberately excluded from the default build:
//
//	go run -tags repomap ./cmd/repomap -root . -budget 3500
//
// For the lean grammar subset build (recommended), add the subset tags —
// see infrastructure/service/symbol/gotreesitter/index.go for the list.
//
// ADAPTER SELECTION LIVES HERE. Swapping engines is one line: replace
// gotreesitter.New(...) with another service.SymbolIndex implementation
// (regex.New(), a future ctags adapter). The renderer, skip rules, and
// bounding policy are engine-independent and shared.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol"
	symgts "github.com/jahrulnr/nusashell/infrastructure/service/symbol/gotreesitter"
	_ "github.com/jahrulnr/nusashell/infrastructure/service/symbol/regex" // fallback adapter, swap target
)

func main() {
	var (
		root   = flag.String("root", ".", "workspace root directory")
		budget = flag.Int("budget", 3000, "approximate token budget for the map (benchmark-chosen default)")
		asJSON = flag.Bool("json", false, "emit JSON instead of the text map")
		bench  = flag.Bool("bench", false, "run the selection-policy benchmark grid")
		tgts   = flag.String("targets", "", "JSON target set for -bench (overrides built-in)")
	)
	flag.Parse()

	ctx := context.Background()
	wsroot, err := symbol.OpenWorkspaceRoot(ctx, repoworkspace.NewLocal(), *root)
	if err != nil {
		fatal(err)
	}
	defer wsroot.Close()

	// ── the single selection point ──────────────────────────────────────
	var idx svc.SymbolIndex = symgts.New(symgts.Options{})
	// ────────────────────────────────────────────────────────────────────

	if p, ok := idx.(svc.SymbolIndexAvailability); ok {
		if err := p.Available(wsroot); err != nil {
			fatal(fmt.Errorf("symbol index unavailable: %w", err))
		}
	}

	if *bench {
		if err := runBench(ctx, idx, wsroot, *tgts); err != nil {
			fatal(err)
		}
		return
	}

	start := time.Now()
	syms, err := idx.Index(ctx, wsroot)
	if err != nil {
		fatal(err)
	}
	elapsed := time.Since(start)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"symbols": syms, "stats": stats(syms, elapsed)}); err != nil {
			fatal(err)
		}
		return
	}
	fmt.Print(symbol.Render(syms, *budget))
	printStats(syms, elapsed)
}

func stats(syms []svc.Symbol, elapsed time.Duration) map[string]any {
	byLang := map[string]int{}
	files := map[string]bool{}
	for _, s := range syms {
		byLang[s.Lang]++
		files[s.Path] = true
	}
	return map[string]any{
		"files": len(files), "symbols": len(syms),
		"elapsed_ms": elapsed.Milliseconds(), "peak_rss_mb": peakRSSMB(),
		"by_lang": byLang,
	}
}

func printStats(syms []svc.Symbol, elapsed time.Duration) {
	st := stats(syms, elapsed)
	byLang := st["by_lang"].(map[string]int)
	langs := make([]string, 0, len(byLang))
	for l := range byLang {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	fmt.Fprintf(os.Stderr, "\n── stats ──\nfiles=%d symbols=%d elapsed=%dms peak_rss=%dMB\n",
		st["files"], st["symbols"], st["elapsed_ms"], st["peak_rss_mb"])
	for _, l := range langs {
		fmt.Fprintf(os.Stderr, "  %-12s %d\n", l, byLang[l])
	}
}

func peakRSSMB() int64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	_ = m
	if b, err := os.ReadFile("/proc/self/status"); err == nil {
		var rss int64
		for _, line := range splitLines(string(b)) {
			if n, err := fmt.Sscanf(line, "VmHWM: %d kB", &rss); n == 1 && err == nil {
				return rss / 1024
			}
		}
	}
	return int64(m.Sys >> 20)
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "repomap:", err)
	os.Exit(1)
}
