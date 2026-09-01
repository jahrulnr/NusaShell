package journal

import (
	"os"
	"path/filepath"
	"testing"

	"nusashell/pkg/hash"
)

func TestBlobStore_roundtrip(t *testing.T) {
	dir := t.TempDir()
	bs := newBlobStore(dir)
	data := []byte("snapshot content")
	hash := hash.Content(data)
	if err := bs.put(hash, data); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(bs.path(hash))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("got %q want %q", got, data)
	}
}

func TestBlobStore_dedup(t *testing.T) {
	dir := t.TempDir()
	bs := newBlobStore(dir)
	data := []byte("same")
	hash := hash.Content(data)
	if err := bs.put(hash, data); err != nil {
		t.Fatal(err)
	}
	path := bs.path(hash)
	info1, _ := os.Stat(path)
	if err := bs.put(hash, data); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(path)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("second put should not rewrite existing blob")
	}
}

func TestBlobStore_atomicWrite(t *testing.T) {
	dir := t.TempDir()
	bs := newBlobStore(dir)
	hash := hash.Content([]byte("atomic"))
	if err := bs.put(hash, []byte("atomic")); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(dir, "blobs", hash[:2], hash+".tmp")
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("temp file should be removed after successful write")
	}
	if _, err := os.Stat(bs.path(hash)); err != nil {
		t.Fatal("final blob should exist")
	}
}
