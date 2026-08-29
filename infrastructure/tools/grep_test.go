package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGrepContentMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package main\n\nfunc foo() {}\nfunc bar() {}\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package main\n\nfunc foo() {}\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":     "func foo",
		"path":        dir,
		"output_mode": "content",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "func foo() {}") {
		t.Errorf("output should contain matching line, got: %s", out)
	}
	if !contains(out, "a.go") {
		t.Errorf("output should contain filename a.go, got: %s", out)
	}
	if !contains(out, "b.go") {
		t.Errorf("output should contain filename b.go, got: %s", out)
	}
}

func TestGrepFilesWithMatchesMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "func foo() {}\n")
	writeFile(t, filepath.Join(dir, "b.go"), "func foo() {}\n")
	writeFile(t, filepath.Join(dir, "c.go"), "func bar() {}\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":     "func foo",
		"path":        dir,
		"output_mode": "files_with_matches",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "a.go") || !contains(out, "b.go") {
		t.Errorf("output should list a.go and b.go, got: %s", out)
	}
	if contains(out, "c.go") {
		t.Errorf("output should NOT list c.go (no match), got: %s", out)
	}
	if contains(out, "func foo") {
		t.Errorf("files_with_matches should not include line content, got: %s", out)
	}
}

func TestGrepCountMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "foo\nfoo\nbar\n")
	writeFile(t, filepath.Join(dir, "b.go"), "foo\nbar\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":     "foo",
		"path":        dir,
		"output_mode": "count",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "a.go") || !contains(out, "2") {
		t.Errorf("a.go should have count 2, got: %s", out)
	}
	if !contains(out, "b.go") || !contains(out, "1") {
		t.Errorf("b.go should have count 1, got: %s", out)
	}
}

func TestGrepGlobFilter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "target\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "target\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":      "target",
		"path":         dir,
		"glob_pattern": "*.go",
		"output_mode":  "files_with_matches",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "a.go") {
		t.Errorf("should match a.go, got: %s", out)
	}
	if contains(out, "b.txt") {
		t.Errorf("should NOT match b.txt (glob filter), got: %s", out)
	}
}

func TestGrepContextLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "line1\nline2\nMATCH\nline4\nline5\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":       "MATCH",
		"path":          dir,
		"context_lines": 1,
		"output_mode":   "content",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "line2") {
		t.Errorf("should include 1 line before match, got: %s", out)
	}
	if !contains(out, "line4") {
		t.Errorf("should include 1 line after match, got: %s", out)
	}
	if contains(out, "line1") {
		t.Errorf("should NOT include line1 (outside context), got: %s", out)
	}
}

func TestGrepCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "FuncFoo\nfuncfoo\nFUNCFOO\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":          "funcfoo",
		"path":             dir,
		"case_insensitive": true,
		"output_mode":      "count",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "3") {
		t.Errorf("case-insensitive should match all 3, got: %s", out)
	}
}

func TestGrepRegexPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "func handleAuth()\nfunc handleUser()\nfunc other()\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":     "func handle\\w+",
		"path":        dir,
		"output_mode": "content",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "handleAuth") || !contains(out, "handleUser") {
		t.Errorf("should match both handle* functions, got: %s", out)
	}
	if contains(out, "other") {
		t.Errorf("should NOT match other(), got: %s", out)
	}
}

func TestGrepSingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	writeFile(t, path, "foo\nbar\nfoo\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":     "foo",
		"path":        path,
		"output_mode": "content",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "foo") {
		t.Errorf("should match foo in single file, got: %s", out)
	}
}

func TestGrepMaxResults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "match\nmatch\nmatch\nmatch\nmatch\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":     "match",
		"path":        dir,
		"output_mode": "content",
		"max_results": 2,
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "line_matches: 2") {
		t.Errorf("max_results=2 should report 2 matches, got: %s", out)
	}
	if !contains(out, "capped: true") {
		t.Errorf("max_results=2 of 5 should flag capped, got: %s", out)
	}
	// rg-style content lines are file:line:text — exactly 2 of them.
	contentLines := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, ":") && strings.HasPrefix(l, filepath.Join(dir, "a.go")) {
			contentLines++
		}
	}
	if contentLines != 2 {
		t.Errorf("max_results=2 should emit exactly 2 match lines, got %d: %s", contentLines, out)
	}
}

func TestGrepClipsHugeLines(t *testing.T) {
	dir := t.TempDir()
	hugeLine := "match " + strings.Repeat("a", 300_000) + " match"
	writeFile(t, filepath.Join(dir, "big.txt"), hugeLine+"\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":     "match",
		"path":        dir,
		"output_mode": "content",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "line omitted") {
		t.Fatalf("expected per-line clip, got: %s", out[:min(len(out), 400)])
	}
	if strings.Contains(out, strings.Repeat("a", 1000)) {
		t.Fatal("minified line leaked into in-band grep output")
	}
	if !strings.Contains(out, "match aaaa") {
		t.Fatal("head of clipped line missing")
	}
}

func TestGrepSkipsVendorAndMinified(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src.js"), "onChange\n")
	writeFile(t, filepath.Join(dir, "vendor", "lib.js"), "onChange\n")
	writeFile(t, filepath.Join(dir, "chart.umd.min.js"), "onChange\n")

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":      "onChange",
		"path":         dir,
		"glob_pattern": "**/*.js",
		"output_mode":  "files_with_matches",
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "src.js") {
		t.Fatalf("expected first-party hit, got: %s", out)
	}
	if strings.Contains(out, "vendor") || strings.Contains(out, ".min.js") {
		t.Fatalf("vendor/minified should be skipped, got: %s", out)
	}
}

func TestGrepSpillWhenManyMatches(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 250; i++ {
		fmt.Fprintf(&b, "needle %s\n", strings.Repeat("z", 180))
	}
	writeFile(t, filepath.Join(dir, "many.txt"), b.String())

	ok, out, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern":     "needle",
		"path":        dir,
		"output_mode": "content",
		"max_results": 250,
	}))
	if !ok || err != nil {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "overflow_path:") {
		t.Fatalf("expected spill for bulky match set, got %d chars:\n%s", len(out), out[:min(len(out), 500)])
	}
	path := overflowPathFrom(t, out)
	t.Cleanup(func() { _ = os.Remove(path) })
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "needle") {
		t.Fatal("spill file missing matches")
	}
	if len(out) > toolInlineMaxBytes+2048 {
		t.Fatalf("in-band grep still too large: %d", len(out))
	}
}

func TestGrepEmptyPattern(t *testing.T) {
	_, _, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern": "",
		"path":    "/tmp",
	}))
	if err == nil {
		t.Error("empty pattern should error")
	}
}

func TestGrepNonexistentPath(t *testing.T) {
	_, _, err := executeFileTool("grep", mustJSONArgs(t, map[string]any{
		"pattern": "foo",
		"path":    "/nonexistent/path/xyz",
	}))
	if err == nil {
		t.Error("nonexistent path should error")
	}
}

// --- backend unit tests (rg JSON parser, Go fallback, shared formatter) ----

func TestParseRgJSONContextGrouping(t *testing.T) {
	// Two matches in one file with -C 1: context lines must attach to the
	// right match (before-context to the next match, after-context to the
	// previous one), and the cap must be reported.
	stream := `{"type":"begin","data":{"path":{"text":"a.go"}}}
{"type":"context","data":{"path":{"text":"a.go"},"lines":{"text":"ctx1\n"},"line_number":1}}
{"type":"match","data":{"path":{"text":"a.go"},"lines":{"text":"hit one\n"},"line_number":2}}
{"type":"context","data":{"path":{"text":"a.go"},"lines":{"text":"mid\n"},"line_number":3}}
{"type":"match","data":{"path":{"text":"a.go"},"lines":{"text":"hit two\n"},"line_number":4}}
{"type":"context","data":{"path":{"text":"a.go"},"lines":{"text":"ctx5\n"},"line_number":5}}
{"type":"end","data":{"path":{"text":"a.go"}}}`
	matches, capped := parseRgJSON(strings.NewReader(stream), 1, 10)
	if capped {
		t.Error("should not be capped with maxResults=10")
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 matches, got %d", len(matches))
	}
	m1, m2 := matches[0], matches[1]
	// "mid" (line 3) sits between the two matches; it must render exactly
	// once, attached as after-context of match1 (not duplicated as
	// before-context of match2).
	if len(m1.Context) != 2 || !m1.Context[0].Before || m1.Context[0].Content != "ctx1" || m1.Context[1].Before || m1.Context[1].Content != "mid" {
		t.Errorf("match1 context wrong: %+v", m1.Context)
	}
	if len(m2.Context) != 1 || m2.Context[0].Before || m2.Context[0].Content != "ctx5" {
		t.Errorf("match2 context wrong (mid must not duplicate): %+v", m2.Context)
	}
}

func TestParseRgJSONCap(t *testing.T) {
	stream := `{"type":"match","data":{"path":{"text":"a.go"},"lines":{"text":"hit\n"},"line_number":1}}
{"type":"match","data":{"path":{"text":"a.go"},"lines":{"text":"hit\n"},"line_number":2}}
{"type":"match","data":{"path":{"text":"a.go"},"lines":{"text":"hit\n"},"line_number":3}}`
	matches, capped := parseRgJSON(strings.NewReader(stream), 0, 2)
	if !capped {
		t.Error("3 matches with maxResults=2 must report capped")
	}
	if len(matches) != 2 {
		t.Errorf("want 2 kept matches, got %d", len(matches))
	}
}

func TestGrepGoFallbackWalker(t *testing.T) {
	// Exercise the pure-Go backend directly (rg may or may not be present
	// on the test host; this covers the portable path either way).
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sub", "a.go"), "alpha\nbeta\nalpha\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "alpha\n")
	writeFile(t, filepath.Join(dir, "node_modules", "skip.js"), "alpha\n")

	re := regexp.MustCompile("alpha")
	matches, err := grepDir(dir, "*.go", re, 0, 100)
	if err != nil {
		t.Fatalf("grepDir: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 matches (glob *.go, node_modules skipped), got %d: %+v", len(matches), matches)
	}
	for _, m := range matches {
		if !strings.HasSuffix(m.File, "a.go") {
			t.Errorf("unexpected file %s", m.File)
		}
	}
	if matches[0].Line != 1 || matches[1].Line != 3 {
		t.Errorf("want lines 1 and 3, got %d and %d", matches[0].Line, matches[1].Line)
	}
}

func TestGrepGoFallbackContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	writeFile(t, path, "l1\nl2\nHIT\nl4\nl5\n")
	re := regexp.MustCompile("HIT")
	matches, err := grepFile(path, re, 1)
	if err != nil {
		t.Fatalf("grepFile: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %d", len(matches))
	}
	m := matches[0]
	var before, after []grepContextLine
	for _, c := range m.Context {
		if c.Before {
			before = append(before, c)
		} else {
			after = append(after, c)
		}
	}
	if len(before) != 1 || before[0].Content != "l2" || before[0].Line != 2 {
		t.Errorf("before-context wrong: %+v", before)
	}
	if len(after) != 1 || after[0].Content != "l4" || after[0].Line != 4 {
		t.Errorf("after-context wrong: %+v", after)
	}
}

func TestFormatRgResultsShapes(t *testing.T) {
	matches := []grepMatch{
		{File: "a.go", Line: 2, Content: "hit one",
			Context: []grepContextLine{{Line: 1, Content: "ctx", Before: true}}},
		{File: "a.go", Line: 9, Content: "hit two"},
		{File: "b.go", Line: 1, Content: "hit three"},
	}

	out := formatRgResults(matches, "content", "go", false)
	for _, want := range []string{"a.go-1-ctx", "a.go:2:hit one", "a.go:9:hit two", "b.go:1:hit three", "line_matches: 3", "via: go"} {
		if !strings.Contains(out, want) {
			t.Errorf("content mode missing %q:\n%s", want, out)
		}
	}

	out = formatRgResults(matches, "files_with_matches", "rg", false)
	if !strings.Contains(out, "files: 2") || strings.Contains(out, "hit one") {
		t.Errorf("files_with_matches shape wrong:\n%s", out)
	}

	out = formatRgResults(matches, "count", "rg", true)
	for _, want := range []string{"a.go:2", "b.go:1", "total_line_matches: 3", "capped: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("count mode missing %q:\n%s", want, out)
		}
	}

	out = formatRgResults(nil, "content", "go", false)
	if !strings.Contains(out, "line_matches: 0") || !strings.Contains(out, "via: go") {
		t.Errorf("empty result shape wrong:\n%s", out)
	}
}

// helpers

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSONArgs(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func countOccurrences(s, sub string) int {
	count := 0
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			count++
		}
	}
	return count
}
