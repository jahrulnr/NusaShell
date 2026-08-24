package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindFileByExtension(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "x")
	writeFile(t, filepath.Join(dir, "b.go"), "x")
	writeFile(t, filepath.Join(dir, "c.txt"), "x")

	ok, out, err := executeFileTool("find_file", mustJSONArgs(t, map[string]any{
		"pattern": "*.go",
		"path":    dir,
	}))
	if !ok || err != nil {
		t.Fatalf("find_file failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "a.go") || !contains(out, "b.go") {
		t.Errorf("should find a.go and b.go, got: %s", out)
	}
	if contains(out, "c.txt") {
		t.Errorf("should NOT find c.txt, got: %s", out)
	}
}

func TestFindFileRecursiveDoubleStar(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src", "deep", "nested")
	writeFile(t, filepath.Join(sub, "target.tsx"), "x")
	writeFile(t, filepath.Join(dir, "other.ts"), "x")

	ok, out, err := executeFileTool("find_file", mustJSONArgs(t, map[string]any{
		"pattern": "**/*.tsx",
		"path":    dir,
	}))
	if !ok || err != nil {
		t.Fatalf("find_file failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "target.tsx") {
		t.Errorf("should find nested target.tsx, got: %s", out)
	}
	if contains(out, "other.ts") {
		t.Errorf("should NOT find other.ts (wrong extension), got: %s", out)
	}
}

func TestFindFileByName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "x")
	writeFile(t, filepath.Join(dir, "main.go"), "x")
	sub := filepath.Join(dir, "docs")
	writeFile(t, filepath.Join(sub, "README.md"), "x")

	ok, out, err := executeFileTool("find_file", mustJSONArgs(t, map[string]any{
		"pattern": "README.md",
		"path":    dir,
	}))
	if !ok || err != nil {
		t.Fatalf("find_file failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "README.md") {
		t.Errorf("should find README.md, got: %s", out)
	}
	// Should find both README.md files (root + docs/)
	if countOccurrences(out, "README.md") < 2 {
		t.Errorf("should find README.md in multiple dirs, got: %s", out)
	}
}

func TestFindFileBraceExpansion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "x")
	writeFile(t, filepath.Join(dir, "b.ts"), "x")
	writeFile(t, filepath.Join(dir, "c.py"), "x")

	ok, out, err := executeFileTool("find_file", mustJSONArgs(t, map[string]any{
		"pattern": "*.{go,ts}",
		"path":    dir,
	}))
	if !ok || err != nil {
		t.Fatalf("find_file failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "a.go") || !contains(out, "b.ts") {
		t.Errorf("should find a.go and b.ts, got: %s", out)
	}
	if contains(out, "c.py") {
		t.Errorf("should NOT find c.py, got: %s", out)
	}
}

func TestFindFileEmptyPattern(t *testing.T) {
	_, _, err := executeFileTool("find_file", mustJSONArgs(t, map[string]any{
		"pattern": "",
		"path":    "/tmp",
	}))
	if err == nil {
		t.Error("empty pattern should error")
	}
}

func TestFindFileNonexistentPath(t *testing.T) {
	_, _, err := executeFileTool("find_file", mustJSONArgs(t, map[string]any{
		"pattern": "*.go",
		"path":    "/nonexistent/xyz",
	}))
	if err == nil {
		t.Error("nonexistent path should error")
	}
}

func TestFindFileSkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gitDir, "config.go"), "x")
	writeFile(t, filepath.Join(dir, "real.go"), "x")

	ok, out, err := executeFileTool("find_file", mustJSONArgs(t, map[string]any{
		"pattern": "*.go",
		"path":    dir,
	}))
	if !ok || err != nil {
		t.Fatalf("find_file failed: ok=%v err=%v", ok, err)
	}
	if !contains(out, "real.go") {
		t.Errorf("should find real.go, got: %s", out)
	}
	if contains(out, ".git") {
		t.Errorf("should NOT search inside .git/, got: %s", out)
	}
}
