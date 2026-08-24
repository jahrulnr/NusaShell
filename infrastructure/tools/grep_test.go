package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	// Count JSON content lines (each match is one JSON object with "content")
	if !contains(out, "matches: 2") {
		t.Errorf("max_results=2 should report 2 matches, got: %s", out)
	}
	lines := strings.Split(out, "\n")
	contentLines := 0
	for _, l := range lines {
		if contains(l, `"content"`) {
			contentLines++
		}
	}
	if contentLines > 2 {
		t.Errorf("max_results=2 should cap at 2 content lines, got %d: %s", contentLines, out)
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
