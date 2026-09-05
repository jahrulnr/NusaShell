package application

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeInstructionFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func instructionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeInstructionFile(t, filepath.Join(dir, "AGENTS.md"), "# root\n")
	writeInstructionFile(t, filepath.Join(dir, "application", "AGENTS.md"), "# app\n")
	writeInstructionFile(t, filepath.Join(dir, "frontend", "AGENTS.md"), "# ui\n")
	writeInstructionFile(t, filepath.Join(dir, "node_modules", "pkg", "AGENTS.md"), "# noisy npm\n")
	writeInstructionFile(t, filepath.Join(dir, "vendor", "lib", "AGENTS.md"), "# noisy vendor\n")
	writeInstructionFile(t, filepath.Join(dir, ".experimental", "fork", "AGENTS.md"), "# noisy experimental\n")
	writeInstructionFile(t, filepath.Join(dir, ".gitignore"), "node_modules/\nvendor/\n.experimental/\n")
	return dir
}

func TestListInstructionFilesGitHonorsGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := instructionFixture(t)
	run := exec.Command("git", "-C", dir, "init", "--quiet")
	gitNull := "/dev/null"
	if runtime.GOOS == "windows" {
		gitNull = "NUL"
	}
	run.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+gitNull)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	got := listInstructionFiles(dir)
	want := []string{"AGENTS.md", "application/AGENTS.md", "frontend/AGENTS.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instruction files = %v, want %v", got, want)
	}
}

func TestListInstructionFilesWalkSkipsNoisyDirsWithoutGit(t *testing.T) {
	dir := instructionFixture(t)
	got := listInstructionFiles(dir)
	for _, p := range got {
		if strings.Contains(p, "node_modules") || strings.Contains(p, "vendor") || strings.Contains(p, ".experimental") {
			t.Fatalf("walk fallback leaked ignored path %q in %v", p, got)
		}
	}
	joined := strings.Join(got, ",")
	for _, want := range []string{"AGENTS.md", "application/AGENTS.md", "frontend/AGENTS.md"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("walk fallback missing %s in %v", want, got)
		}
	}
}

func TestListInstructionFilesEmptyWorkspace(t *testing.T) {
	if got := listInstructionFiles(""); got != nil {
		t.Fatalf("empty workspace = %v, want nil", got)
	}
	if got := listInstructionFiles(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("missing workspace = %v, want nil", got)
	}
}
