package nusatemp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolateTemp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// os.TempDir uses TMPDIR on Unix and TMP/TEMP on Windows. Set all
	// supported names so the tests exercise the same isolated root everywhere.
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, root)
	}
	return root
}

func TestPathIsPlatformTempNusashell(t *testing.T) {
	root := isolateTemp(t)
	got := Path()
	want := filepath.Join(os.TempDir(), DirName)
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, root) {
		t.Fatalf("Path() = %q, want under %q", got, root)
	}
	if filepath.Base(got) != DirName {
		t.Fatalf("base = %q, want %q", filepath.Base(got), DirName)
	}
}

func TestDirCreates0700(t *testing.T) {
	isolateTemp(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != Path() {
		t.Fatalf("Dir() = %q, want Path() %q", dir, Path())
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
	// Windows does not expose the requested Unix permission bits through
	// FileMode.Perm; a normal writable directory reports 0777 there. The
	// platform-specific ACL semantics are outside this package's contract.
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Fatalf("Dir() mode = %o, want 0700", st.Mode().Perm())
	}
}

func TestMkdirTempLandsUnderDir(t *testing.T) {
	isolateTemp(t)
	tmp, err := MkdirTemp("tts-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	if filepath.Dir(tmp) != Path() {
		t.Fatalf("MkdirTemp dir = %q, want %q", filepath.Dir(tmp), Path())
	}
	if !strings.Contains(filepath.Base(tmp), "tts-") {
		t.Fatalf("unexpected name: %s", tmp)
	}
}
