//go:build repomap

package main

import (
	"context"

	"fmt"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	toolsymbolmap "github.com/jahrulnr/nusashell/infrastructure/service/tool/symbolmap"
)

// With the repomap tag the wire registers the symbol_map record tool.
func TestWireSymbolMapRegisters(t *testing.T) {
	tools := map[string]dservice.Tool{}
	if err := wireSymbolMap(tools, "", t.TempDir(), repoworkspace.NewLocal(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	tool, ok := tools[toolsymbolmap.ToolName]
	if !ok {
		t.Fatal("symbol_map tool not registered under repomap tag")
	}
	if !tool.Schema().CallableBySystem() || !tool.Schema().CallableByAgent() {
		t.Fatal("symbol_map must serve system injection and agent on-demand calls")
	}
}

// End-to-end through the environment bundle: the hydration pass emits a
// "being built" record, the background index commits, and later passes
// serve the same map byte-identically — the property conversation dedup
// keys on.
func TestWireSymbolMapRecordFlow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := map[string]dservice.Tool{}
	if err := wireSymbolMap(tools, "", t.TempDir(), repoworkspace.NewLocal(), dir); err != nil {
		t.Fatal(err)
	}
	bundle := appjob.EnvironmentBundle{Tools: tools}
	ctx := dservice.WithWorkspaceDir(context.Background(), dir)

	records, err := bundle.ToolContexts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := onlySymbolMap(t, records)
	if !strings.Contains(first.Output, "is being built") {
		t.Fatalf("first pass should announce the build, got:\n%s", first.Output)
	}
	var prev string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		records, err := bundle.ToolContexts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := onlySymbolMap(t, records).Output
		if strings.Contains(out, "is being built") {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if prev == "" {
			prev = out
			if !strings.HasPrefix(out, "symbol map for "+dir+" — declarations only") {
				t.Fatalf("map header missing, got:\n%.200s", out)
			}
			continue
		}
		if out != prev {
			t.Fatal("steady workspace must yield byte-identical records for dedup")
		}
		return
	}
	t.Fatal("map never materialized")
}

// Acceptance: three bound roots, one full map. The whole record stays
// inside symbols.budget_tokens, extras cost one line each, the rendered
// map spans several top directories, steady passes are byte-identical,
// and a file change moves the record exactly once — never back through
// "is being built".
func TestWireSymbolMapMultiRootAcceptance(t *testing.T) {
	// Main root: enough files across enough dirs that a 500-token
	// budget must select, not fit everything.
	main := t.TempDir()
	for _, d := range []string{"aaa", "mmm", "zzz"} {
		for i := 0; i < 15; i++ {
			body := fmt.Sprintf("package %s\n\nfunc %sFn%02d() {}\n", d, d, i)
			write(t, filepath.Join(main, d), fmt.Sprintf("f%02d.go", i), body)
		}
	}
	write(t, main, "go.mod", "module fixture\n")
	extra1, extra2 := t.TempDir(), t.TempDir()
	write(t, extra1, "x.go", "package x\n\nfunc X() {}\n")
	write(t, extra2, "y.go", "package y\n\nfunc Y() {}\n")

	configPath := filepath.Join(t.TempDir(), "config.json")
	write(t, filepath.Dir(configPath), filepath.Base(configPath), `{"symbols":{"budget_tokens":500}}`)

	tools := map[string]dservice.Tool{}
	if err := wireSymbolMap(tools, configPath, t.TempDir(), repoworkspace.NewLocal(), main); err != nil {
		t.Fatal(err)
	}
	bundle := appjob.EnvironmentBundle{Tools: tools}
	ctx := dservice.WithWorkspaceDir(context.Background(), main)
	ctx = dservice.WithExtraFolders(ctx, []string{extra1, extra2})

	// Turn 1 may be the first-build line; wait for the real map.
	var rec string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out := onlySymbolMap(t, mustContexts(t, bundle, ctx)).Output
		if !strings.Contains(out, "is being built") {
			rec = out
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if rec == "" {
		t.Fatal("map never materialized")
	}

	// Budget: whole record ≤ 500 tokens + header/footer slack.
	if len(rec) > 500*4+300 {
		t.Fatalf("record over budget: %d bytes", len(rec))
	}
	// Exactly one full map; each extra is a single "+" line.
	if n := strings.Count(rec, "symbol map for "); n != 1 {
		t.Fatalf("want one full map, got %d:\n%s", n, rec)
	}
	for _, e := range []string{extra1, extra2} {
		if !strings.Contains(rec, "\n+ "+e+" — map on demand") {
			t.Fatalf("extra folder line missing for %s:\n%s", e, rec)
		}
	}
	// Breadth: symbols from all three top dirs survive selection.
	for _, d := range []string{"aaa/", "mmm/", "zzz/"} {
		if !strings.Contains(rec, d) {
			t.Fatalf("map lost top dir %s:\n%.400s", d, rec)
		}
	}

	// Five steady passes: byte-identical.
	for i := 0; i < 5; i++ {
		out := onlySymbolMap(t, mustContexts(t, bundle, ctx)).Output
		if out != rec {
			t.Fatalf("pass %d changed the record without a file change", i)
		}
	}

	// One file change: the record must hold the stale bytes, then move
	// exactly once — no flip back to "is being built".
	write(t, filepath.Join(main, "aaa"), "new.go", "package aaa\n\nfunc NewlyAdded() {}\n")
	out := onlySymbolMap(t, mustContexts(t, bundle, ctx)).Output
	if out != rec {
		t.Fatal("record churned before the rebuild landed")
	}
	var next string
	deadline = time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out = onlySymbolMap(t, mustContexts(t, bundle, ctx)).Output
		if strings.Contains(out, "is being built") {
			t.Fatal("stale map must serve during rebuild, not 'being built'")
		}
		if out != rec {
			next = out
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if next == "" {
		t.Fatal("rebuild never landed")
	}
	if !strings.Contains(next, "NewlyAdded") {
		t.Fatalf("rebuilt map lacks the new symbol:\n%.300s", next)
	}
	for i := 0; i < 3; i++ {
		if out := onlySymbolMap(t, mustContexts(t, bundle, ctx)).Output; out != next {
			t.Fatal("record must stabilize after the single transition")
		}
	}
}

// Real-tree evidence: SYMBOLMAP_E2E_ROOTS="ws:extra1:extra2" drives the
// production wire — gotreesitter, live config, disk cache — through three
// hydration passes and reports record bytes, build latency, and churn.
// Skipped without the env var.
func TestWireSymbolMapRealRoots(t *testing.T) {
	spec := os.Getenv("SYMBOLMAP_E2E_ROOTS")
	if spec == "" {
		t.Skip("set SYMBOLMAP_E2E_ROOTS=ws:extra1[:extra2…]")
	}
	roots := strings.Split(spec, ":")
	tools := map[string]dservice.Tool{}
	configPath := filepath.Join(t.TempDir(), "config.json")
	write(t, filepath.Dir(configPath), filepath.Base(configPath), `{"symbols":{"budget_tokens":3000}}`)
	storage := t.TempDir()
	if err := wireSymbolMap(tools, configPath, storage, repoworkspace.NewLocal(), roots[0]); err != nil {
		t.Fatal(err)
	}
	bundle := appjob.EnvironmentBundle{Tools: tools}
	ctx := dservice.WithWorkspaceDir(context.Background(), roots[0])
	ctx = dservice.WithExtraFolders(ctx, roots[1:])

	start := time.Now()
	var prev string
	changes, steady := 0, 0
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		out := onlySymbolMap(t, mustContexts(t, bundle, ctx)).Output
		if strings.Contains(out, "is being built") {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		switch {
		case prev == "":
			prev = out
			t.Logf("first map after %s", time.Since(start).Round(time.Millisecond))
		case out != prev:
			changes++
			steady = 0
			prev = out
		default:
			steady++
			if steady >= 3 {
				goto done
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
done:
	t.Logf("record bytes=%d (~%d tokens), transitions=%d", len(prev), len(prev)/4, changes)
	t.Logf("record excerpt:\n%s", prev)
	if prev == "" {
		t.Fatal("map never materialized")
	}
	if changes != 0 {
		t.Fatalf("steady tree churned: %d transitions", changes)
	}

	// On-demand: an agent-shaped call for a bound extra waits for and
	// returns that folder's map; a repeat serves the cached bytes. An
	// unbound path is refused. The hydration line then carries the
	// indexed counts — the "map on demand" promise is backed.
	if len(roots) > 1 {
		tool := tools[toolsymbolmap.ToolName]
		call, _ := entity.NewToolCall("call_ondemand", toolsymbolmap.ToolName,
			`{"path":"`+roots[1]+`"}`, enum.TooltypeOrigin)
		start = time.Now()
		res, err := tool.Execute(ctx, call, nil)
		if err != nil {
			t.Fatal(err)
		}
		waited := time.Since(start).Round(time.Millisecond)
		if strings.Contains(res.Text, "is being built") || !strings.Contains(res.Text, "symbol map for "+roots[1]) {
			t.Fatalf("on-demand call must return the extra's map (%s):\n%.300s", waited, res.Text)
		}
		start = time.Now()
		res2, _ := tool.Execute(ctx, call, nil)
		cached := time.Since(start).Round(time.Millisecond)
		if res2.Text != res.Text {
			t.Fatal("second on-demand call should serve identical cached bytes")
		}
		t.Logf("on-demand %s: first %s (bytes=%d), cached %s", roots[1], waited, len(res.Text), cached)

		bad, _ := entity.NewToolCall("call_bad", toolsymbolmap.ToolName, `{"path":"/etc"}`, enum.TooltypeOrigin)
		res3, _ := tool.Execute(ctx, bad, nil)
		if !strings.Contains(res3.Text, "not a folder bound") {
			t.Fatalf("unbound path must be refused, got:\n%.200s", res3.Text)
		}
		out := onlySymbolMap(t, mustContexts(t, bundle, ctx)).Output
		if !strings.Contains(out, "+ "+roots[1]+" — ") || strings.Contains(out, "+ "+roots[1]+" — map on demand") {
			t.Fatalf("indexed extra should now show counts, got:\n%.200s", out[strings.Index(out, "+ "):])
		}
	}
}

func mustContexts(t *testing.T, b appjob.EnvironmentBundle, ctx context.Context) []appjob.ToolContext {
	t.Helper()
	recs, err := b.ToolContexts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func onlySymbolMap(t *testing.T, records []appjob.ToolContext) appjob.ToolContext {
	t.Helper()
	for _, r := range records {
		if r.Name == toolsymbolmap.ToolName {
			return r
		}
	}
	t.Fatal("no symbol_map record produced")
	return appjob.ToolContext{}
}
