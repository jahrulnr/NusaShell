package reposkill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSweepTempsRemovesOrphanTempsOnly(t *testing.T) {
	root := t.TempDir()
	skill := filepath.Join(root, "demo")
	archive := filepath.Join(root, ".archive", "old")
	if err := os.MkdirAll(skill, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archive, 0755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(skill, "SKILL.md")
	orphan := filepath.Join(skill, ".skill-orphan")
	archived := filepath.Join(archive, ".skill-keep")
	for _, path := range []string{keep, orphan, archived} {
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SweepTemps(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan temp survived")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archived); err != nil {
		t.Fatal("archive temp was swept")
	}
}
