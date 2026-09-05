package dirbrowser

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestListDirsReturnsOnlySortedSubdirectories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"beta", "alpha", "gamma"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	listing, err := (OS{}).ListDirs(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if listing.Path != root {
		t.Fatalf("Path = %q, want %q", listing.Path, root)
	}
	if listing.Parent != filepath.Dir(root) {
		t.Fatalf("Parent = %q, want %q", listing.Parent, filepath.Dir(root))
	}
	if len(listing.Entries) != 3 {
		t.Fatalf("entries = %+v, want 3 directories", listing.Entries)
	}
	names := make([]string, 0, 3)
	for _, entry := range listing.Entries {
		names = append(names, entry.Name)
		if filepath.Base(entry.Path) != entry.Name {
			t.Fatalf("entry path %q must end with name %q", entry.Path, entry.Name)
		}
	}
	want := []string{"alpha", "beta", "gamma"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want sorted %v", names, want)
		}
	}
	if listing.Truncated {
		t.Fatal("small listing must not be truncated")
	}
}

func TestListDirsEmptyPathResolvesToHome(t *testing.T) {
	listing, err := (OS{}).ListDirs(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if listing.Path != home {
		t.Fatalf("Path = %q, want home %q", listing.Path, home)
	}
}

func TestListDirsRejectsRelativePath(t *testing.T) {
	_, err := (OS{}).ListDirs(context.Background(), "relative/dir")
	if err == nil {
		t.Fatal("relative path must be rejected")
	}
}

func TestListDirsMissingPathSurfacesNotExist(t *testing.T) {
	_, err := (OS{}).ListDirs(context.Background(), filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestListDirsTruncatesAtCap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxEntries+5; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("d%04d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	listing, err := (OS{}).ListDirs(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !listing.Truncated {
		t.Fatal("over-cap listing must be truncated")
	}
	if len(listing.Entries) != maxEntries {
		t.Fatalf("entries = %d, want cap %d", len(listing.Entries), maxEntries)
	}
}

func TestEnsureDirAcceptsDirectory(t *testing.T) {
	if err := (OS{}).EnsureDir(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDirRejectsFileAndMissingPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (OS{}).EnsureDir(context.Background(), file); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("file err = %v, want fs.ErrInvalid", err)
	}
	if err := (OS{}).EnsureDir(context.Background(), filepath.Join(t.TempDir(), "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing err = %v, want fs.ErrNotExist", err)
	}
}
