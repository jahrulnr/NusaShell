package sqlitestore

import (
	"os"
	"path/filepath"
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
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.db mode = %o, want 0600", info.Mode().Perm())
	}
}
