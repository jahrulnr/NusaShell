// Package jsonstore — embedding cache.
//
// The embedding cache stores computed embedding vectors keyed by
// (model_id, sha256(normalized_text)). This avoids re-embedding the same
// content on every search call — a problem that scales linearly with the
// number of memory/skill entries.
//
// Storage: learning_embeddings.jsonl — one entry per line, append-only.
// The full cache is loaded into memory at startup. Cache hits return
// instantly with zero API calls; cache misses embed, store, and return.
//
// The cache auto-invalidates when the embedding model changes: the model_id
// is part of the key, so a model swap produces cache misses by construction.
package jsonstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// EmbeddingCacheEntry is one cached embedding vector.
type EmbeddingCacheEntry struct {
	ModelID string    `json:"model_id"`
	Hash    string    `json:"hash"` // sha256 of normalized text
	Vector  []float32 `json:"vector"`
}

// EmbeddingCache is a content-addressed cache for embedding vectors.
// It persists to learning_embeddings.jsonl and keeps an in-memory map
// for O(1) lookups.
type EmbeddingCache struct {
	mu      sync.RWMutex
	muFile  sync.Mutex
	path    string
	entries map[string]*EmbeddingCacheEntry // key = modelID + ":" + hash
	file    *os.File
}

// cacheKey returns the composite key for the in-memory map.
func cacheKey(modelID, hash string) string {
	return modelID + ":" + hash
}

// NewEmbeddingCache opens or creates the cache file at dataDir.
func NewEmbeddingCache(dataDir string) (*EmbeddingCache, error) {
	path := filepath.Join(dataDir, "learning_embeddings.jsonl")
	c := &EmbeddingCache{
		path:    path,
		entries: make(map[string]*EmbeddingCacheEntry),
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	// Open file for append.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	c.file = f
	return c, nil
}

// load reads the entire cache file into memory.
func (c *EmbeddingCache) load() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry EmbeddingCacheEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip malformed lines
		}
		c.entries[cacheKey(entry.ModelID, entry.Hash)] = &entry
	}
	return nil
}

// Get returns the cached vector for (modelID, text), or nil if not cached.
func (c *EmbeddingCache) Get(modelID, text string) ([]float32, bool) {
	h := hashText(text)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.entries[cacheKey(modelID, h)]; ok {
		return e.Vector, true
	}
	return nil, false
}

// Put stores a vector for (modelID, text) and appends to the JSONL file.
func (c *EmbeddingCache) Put(modelID, text string, vector []float32) error {
	h := hashText(text)
	entry := &EmbeddingCacheEntry{
		ModelID: modelID,
		Hash:    h,
		Vector:  vector,
	}
	c.mu.Lock()
	c.entries[cacheKey(modelID, h)] = entry
	c.mu.Unlock()

	// Append to file (under file mutex to avoid interleaving).
	c.muFile.Lock()
	defer c.muFile.Unlock()
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = c.file.Write(b)
	return err
}

// GetBatch returns vectors for texts that are cached, and a list of
// indices that need embedding (cache misses). The returned vectors slice
// has the same length as texts; misses are nil.
func (c *EmbeddingCache) GetBatch(modelID string, texts []string) (vectors [][]float32, misses []int) {
	vectors = make([][]float32, len(texts))
	for i, text := range texts {
		if v, ok := c.Get(modelID, text); ok {
			vectors[i] = v
		} else {
			misses = append(misses, i)
		}
	}
	return vectors, misses
}

// PutBatch stores multiple vectors at once. texts and vectors must be the
// same length. Only non-nil vectors are stored.
func (c *EmbeddingCache) PutBatch(modelID string, texts []string, vectors [][]float32) error {
	for i, text := range texts {
		if i >= len(vectors) || vectors[i] == nil {
			continue
		}
		if err := c.Put(modelID, text, vectors[i]); err != nil {
			return err
		}
	}
	return nil
}

// Stats returns cache hit/miss counts and total entries.
func (c *EmbeddingCache) Stats() (entries int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// InvalidateModel removes all entries for a specific model. Called when
// the embedding model changes.
func (c *EmbeddingCache) InvalidateModel(modelID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for k := range c.entries {
		if strings.HasPrefix(k, modelID+":") {
			delete(c.entries, k)
			removed++
		}
	}
	return removed
}

// Close closes the underlying file handle.
func (c *EmbeddingCache) Close() error {
	c.muFile.Lock()
	defer c.muFile.Unlock()
	if c.file != nil {
		return c.file.Close()
	}
	return nil
}

// hashText returns the SHA-256 hex digest of the normalized text.
// Normalization: trim + collapse whitespace + lowercase. This ensures
// that "Hello  World" and "hello world" share the same cache entry.
func hashText(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Join(strings.Fields(normalized), " ")
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}
