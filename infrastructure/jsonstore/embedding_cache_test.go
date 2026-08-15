package jsonstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddingCacheGetPut(t *testing.T) {
	dir := t.TempDir()
	c, err := NewEmbeddingCache(dir)
	if err != nil {
		t.Fatalf("NewEmbeddingCache: %v", err)
	}
	defer c.Close()

	// Put a vector
	vec := []float32{0.1, 0.2, 0.3}
	if err := c.Put("model-a", "hello world", vec); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Get should return the cached vector
	got, ok := c.Get("model-a", "hello world")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 3 || got[0] != 0.1 {
		t.Errorf("got = %v, want %v", got, vec)
	}
}

func TestEmbeddingCacheMiss(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewEmbeddingCache(dir)
	defer c.Close()

	_, ok := c.Get("model-a", "not cached")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestEmbeddingCacheModelIsolation(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewEmbeddingCache(dir)
	defer c.Close()

	c.Put("model-a", "text", []float32{1.0})
	c.Put("model-b", "text", []float32{2.0})

	vA, _ := c.Get("model-a", "text")
	vB, _ := c.Get("model-b", "text")
	if vA[0] != 1.0 || vB[0] != 2.0 {
		t.Errorf("model isolation failed: a=%v b=%v", vA, vB)
	}
}

func TestEmbeddingCacheNormalization(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewEmbeddingCache(dir)
	defer c.Close()

	c.Put("model-a", "  Hello   World  ", []float32{1.0})

	// Different whitespace + case should hit the same cache entry
	got, ok := c.Get("model-a", "hello world")
	if !ok {
		t.Fatal("expected cache hit after normalization")
	}
	if got[0] != 1.0 {
		t.Errorf("got = %v", got)
	}
}

func TestEmbeddingCacheGetBatch(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewEmbeddingCache(dir)
	defer c.Close()

	c.Put("model-a", "cached", []float32{1.0})

	texts := []string{"cached", "not cached", "also not cached"}
	vectors, misses := c.GetBatch("model-a", texts)
	if vectors[0] == nil {
		t.Error("expected hit for 'cached'")
	}
	if vectors[1] != nil || vectors[2] != nil {
		t.Error("expected misses for uncached texts")
	}
	if len(misses) != 2 {
		t.Errorf("misses = %d, want 2", len(misses))
	}
	if misses[0] != 1 || misses[1] != 2 {
		t.Errorf("miss indices = %v, want [1 2]", misses)
	}
}

func TestEmbeddingCachePersistence(t *testing.T) {
	dir := t.TempDir()
	c1, _ := NewEmbeddingCache(dir)
	c1.Put("model-a", "persist me", []float32{0.5})
	c1.Close()

	// Reopen — should load from file
	c2, _ := NewEmbeddingCache(dir)
	defer c2.Close()
	got, ok := c2.Get("model-a", "persist me")
	if !ok {
		t.Fatal("expected cache hit after reopen")
	}
	if got[0] != 0.5 {
		t.Errorf("got = %v, want [0.5]", got)
	}
}

func TestEmbeddingCacheInvalidateModel(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewEmbeddingCache(dir)
	defer c.Close()

	c.Put("model-a", "text1", []float32{1.0})
	c.Put("model-a", "text2", []float32{2.0})
	c.Put("model-b", "text3", []float32{3.0})

	removed := c.InvalidateModel("model-a")
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	if _, ok := c.Get("model-a", "text1"); ok {
		t.Error("model-a entries should be invalidated")
	}
	if _, ok := c.Get("model-b", "text3"); !ok {
		t.Error("model-b entries should be untouched")
	}
}

func TestEmbeddingCacheFileCreated(t *testing.T) {
	dir := t.TempDir()
	c, _ := NewEmbeddingCache(dir)
	c.Put("model-a", "test", []float32{1.0})
	c.Close()

	if _, err := os.Stat(filepath.Join(dir, "learning_embeddings.jsonl")); err != nil {
		t.Errorf("cache file not created: %v", err)
	}
}
