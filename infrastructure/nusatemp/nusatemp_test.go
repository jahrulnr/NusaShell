package nusatemp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolateTemp(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
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
	if st.Mode().Perm() != 0o700 {
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

func TestCreateTempLandsUnderDir(t *testing.T) {
	isolateTemp(t)
	f, err := CreateTemp("stt-engine-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(path) })
	if filepath.Dir(path) != Path() {
		t.Fatalf("CreateTemp dir = %q, want %q", filepath.Dir(path), Path())
	}
}
