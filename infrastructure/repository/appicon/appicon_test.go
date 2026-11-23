package repoappicon

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteIsAValidPNGAndIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	path, err := Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, File) {
		t.Fatalf("path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != b.Dy() || b.Dx() < 128 {
		t.Fatalf("icon is %dx%d, want a square of at least 128", b.Dx(), b.Dy())
	}
	before, _ := os.Stat(path)
	if _, err := Write(dir); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("an up-to-date icon was rewritten")
	}
}

func TestWriteRepairsADamagedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, icon) {
		t.Fatal("the damaged icon was not replaced")
	}
	left, _ := filepath.Glob(filepath.Join(dir, File+".tmp-*"))
	if len(left) != 0 {
		t.Fatalf("staging files left behind: %v", left)
	}
}
