package toolsymbolmap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol/regex"
)

// testTool wires the tool against a temp workspace and the zero-dep regex
// adapter — the manager's engine choice is not what the tool contract
// pins.
func testTool(t *testing.T, dir string) dservice.Tool {
	t.Helper()
	maps := symbol.NewManager(
		func(o symbol.Options) dservice.SymbolIndex { return regex.New(o.MaxFileBytes) },
		func() symbol.Options {
			return symbol.Options{BudgetTokens: 2000, Workers: 1, MaxFileBytes: symbol.MaxFileBytes}
		},
		nil,
	)
	tool, err := New(repoworkspace.NewLocal(), dir, maps)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// The schema is dual-use: the harness injects it as a record each pass
// and the agent may call it for a bound extra folder's map on demand.
func TestSchemaIsSystemRecord(t *testing.T) {
	tool := testTool(t, t.TempDir())
	s := tool.Schema()
	if s.Name() != "symbol_map" || !s.CallableBySystem() || !s.CallableByAgent() {
		t.Fatalf("symbol_map must serve system and agent, got %+v", s)
	}
	if s.Permission() != enum.ToolPermissionGeneral {
		t.Fatalf("want general permission, got %s", s.Permission())
	}
}

// A bare {"path": <extra>} call is the on-demand mode: it must wait for
// and return that folder's map, and a repeat call must serve the cached
// bytes without another build.
func TestExecuteOnDemandBoundFolder(t *testing.T) {
	ws, extra := t.TempDir(), t.TempDir()
	writeFile(t, ws, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, extra, "x.go", "package x\n\nfunc OnDemand() {}\n")
	tool := testTool(t, ws)
	ctx := dservice.WithWorkspaceDir(context.Background(), ws)
	ctx = dservice.WithExtraFolders(ctx, []string{extra})
	call, err := entity.NewToolCall("call_test", "symbol_map", `{"path":"`+extra+`"}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := tool.Execute(ctx, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "is being built") {
		t.Fatalf("on-demand must wait for the map, got:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "OnDemand") {
		t.Fatalf("on-demand map missing the extra folder's symbol:\n%s", res.Text)
	}
	t.Logf("first on-demand call: %s", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	res2, err := tool.Execute(ctx, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Text != res.Text || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("cached call should be identical and cheap (%s)", time.Since(start))
	}
}

// A path outside the conversation's bound folders is refused with a
// list of what's allowed — the tool never maps arbitrary roots.
func TestExecuteRejectsUnboundPath(t *testing.T) {
	ws, extra := t.TempDir(), t.TempDir()
	writeFile(t, ws, "main.go", "package main\n")
	writeFile(t, extra, "x.go", "package x\n")
	tool := testTool(t, ws)
	ctx := dservice.WithWorkspaceDir(context.Background(), ws)
	ctx = dservice.WithExtraFolders(ctx, []string{extra})
	call, err := entity.NewToolCall("call_test", "symbol_map", `{"path":"/etc"}`, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(ctx, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "not a folder bound") || !strings.Contains(res.Text, extra) {
		t.Fatalf("rejection must name the bound folders, got:\n%s", res.Text)
	}
}

// The hydration pass stays non-blocking: a system-shaped call on a cold
// workspace returns "being built" immediately rather than parking on
// the build like the on-demand path does.
func TestExecuteSystemCallNeverBlocks(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		writeFile(t, dir, fmt.Sprintf("f%02d.go", i), "package x\n")
	}
	tool := testTool(t, dir)
	ctx := dservice.WithWorkspaceDir(context.Background(), dir)
	call, err := entity.NewToolCall("call_test", "symbol_map", `{"path":"`+dir+`"}`, enum.TooltypeInjection)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := tool.Execute(ctx, call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("system call blocked %s on a cold build", time.Since(start))
	}
	if !strings.Contains(res.Text, "is being built") {
		t.Fatalf("cold system call should announce the build, got:\n%.200s", res.Text)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteYieldsMapAfterBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := testTool(t, dir)
	call, err := entity.NewToolCall("call_test", "symbol_map", "{}", enum.TooltypeInjection)
	if err != nil {
		t.Fatal(err)
	}
	// First render announces the build; the record then lands once the
	// background index commits.
	res, err := tool.Execute(context.Background(), call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "is being built") {
		t.Fatalf("first execute should announce the build, got:\n%s", res.Text)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, err = tool.Execute(context.Background(), call, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(res.Text, "symbol map for "+dir+" — declarations only") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(res.Text, "function    main :3") {
		t.Fatalf("map should list main(), got:\n%s", res.Text)
	}
}

// TestProbeLatency measures one Map call against a real directory —
// point SYMBOLMAP_PROBE_DIR at a big, marker-less root (a home dir) to
// verify the guard keeps a turn's cost trivial. Skipped unless the env
// var names a directory.
func TestProbeLatency(t *testing.T) {
	dir := os.Getenv("SYMBOLMAP_PROBE_DIR")
	if dir == "" {
		t.Skip("set SYMBOLMAP_PROBE_DIR to measure")
	}
	tool := testTool(t, dir)
	call, err := entity.NewToolCall("call_test", "symbol_map", "{}", enum.TooltypeInjection)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for i, elapsed := 0, time.Duration(0); i < 3; i++ {
		start := time.Now()
		res, err := tool.Execute(context.Background(), call, nil)
		if err != nil {
			t.Fatal(err)
		}
		elapsed = time.Since(start)
		text = res.Text
		t.Logf("pass %d: %s — %s", i+1, elapsed, firstLine(text))
	}
	_ = text
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
