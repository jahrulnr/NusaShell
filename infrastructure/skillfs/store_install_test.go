package skillfs

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipArchive builds an in-memory zip with the entries in order.
func zipArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		body := "x"
		if strings.HasSuffix(name, "SKILL.md") {
			body = "---\nname: T\ndescription: d\n---\n# T\n"
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestStore_InstallExtractsUnderTopLevel(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data := zipArchive(t,
		"evil-skill/SKILL.md",
		"evil-skill/support/notes.txt",
	)
	id, err := s.Install(data)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if id != "evil-skill" {
		t.Fatalf("id = %q", id)
	}
	if _, err := os.Stat(filepath.Join(s.root, "evil-skill", "support", "notes.txt")); err != nil {
		t.Fatalf("support file missing: %v", err)
	}
}

func TestStore_InstallRejectsTraversal(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	data := zipArchive(t,
		"evil-skill/SKILL.md",
		"evil-skill/../escape.txt",
	)
	if _, err := s.Install(data); err == nil {
		t.Fatal("traversal entry must fail the install")
	}
	if _, err := os.Stat(filepath.Join(s.root, "escape.txt")); err == nil {
		t.Fatal("traversal entry escaped the skill directory")
	}
}
