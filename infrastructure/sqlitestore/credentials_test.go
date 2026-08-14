package sqlitestore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCredentialsFileIsOwnerReadableOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.db")
	store, err := NewCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Set("p1", "secret"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get("p1")
	if err != nil || !ok || got != "secret" {
		t.Fatalf("Get() = %q ok=%v err=%v, want stored secret", got, ok, err)
	}
	if runtime.GOOS == "windows" {
		// NTFS ACLs do not expose Unix 0600; os.Chmod only toggles the
		// read-only attribute on Windows.
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.db mode = %o, want 0600", info.Mode().Perm())
	}
}
