package conversation

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

	got := ListInstructionFiles(dir)
	want := []string{"AGENTS.md", "application/AGENTS.md", "frontend/AGENTS.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instruction files = %v, want %v", got, want)
	}
}

// TestListInstructionFilesGitSymlinkedWorkspace reproduces the CI failure on
// macOS/Windows: git --show-toplevel returns the real path while the
// workspace is addressed through a symlink (/var → /private/var on macOS,
// short names on Windows), which made the git path report an empty catalog
// and skip the walk fallback. The listing must be identical either way.
func TestListInstructionFilesGitSymlinkedWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if runtime.GOOS == "windows" {
		// Windows runners only get usable directory symlinks with
		// Developer Mode; this test reproduces the macOS /var →
		// /private/var realpath mismatch, which Windows does not exhibit
		// (short-name casing is covered by wsRelativeTo + EvalSymlinks).
		t.Skip("directory symlinks are not representative on Windows runners")
	}
	base := t.TempDir()
	fixture := filepath.Join(base, "real")
	writeInstructionFile(t, filepath.Join(fixture, "AGENTS.md"), "# root\n")
	writeInstructionFile(t, filepath.Join(fixture, "application", "AGENTS.md"), "# app\n")
	writeInstructionFile(t, filepath.Join(fixture, "frontend", "AGENTS.md"), "# ui\n")
	writeInstructionFile(t, filepath.Join(fixture, "node_modules", "pkg", "AGENTS.md"), "# noisy\n")
	writeInstructionFile(t, filepath.Join(fixture, ".gitignore"), "node_modules/\nvendor/\n.experimental/\n")
	run := exec.Command("git", "-C", fixture, "init", "--quiet")
	gitNull := "/dev/null"
	if runtime.GOOS == "windows" {
		gitNull = "NUL"
	}
	run.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+gitNull)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(fixture, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	got := ListInstructionFiles(link)
	want := []string{"AGENTS.md", "application/AGENTS.md", "frontend/AGENTS.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("instruction files via symlinked workspace = %v, want %v", got, want)
	}
}

func TestListInstructionFilesWalkSkipsNoisyDirsWithoutGit(t *testing.T) {
	dir := instructionFixture(t)
	got := ListInstructionFiles(dir)
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
	if got := ListInstructionFiles(""); got != nil {
		t.Fatalf("empty workspace = %v, want nil", got)
	}
	if got := ListInstructionFiles(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("missing workspace = %v, want nil", got)
	}
}
