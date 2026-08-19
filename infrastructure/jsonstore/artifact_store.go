package jsonstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"nusashell/domain"
)

// ArtifactStore is a durable, per-conversation artifact store backed by a
// single JSON file (artifacts.json). It stores interactive HTML/CSS/JS
// artifacts produced by the agent via artifact_create / artifact_update.
// It is safe for concurrent use.
type ArtifactStore struct {
	mu    sync.RWMutex
	path  string
	store map[string][]*domain.CanvasArtifact
}

// NewArtifactStore opens or creates the artifact store at path. A missing
// or corrupt file is treated as an empty store so the shell can still boot.
func NewArtifactStore(path string) *ArtifactStore {
	s := &ArtifactStore{
		path:  path,
		store: make(map[string][]*domain.CanvasArtifact),
	}
	s.load()
	return s
}

// List returns all artifacts for the given conversation, sorted by
// UpdatedAt descending (newest first). Returns nil when none exist.
func (s *ArtifactStore) List(conversationID string) []*domain.CanvasArtifact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	arts := s.store[conversationID]
	out := make([]*domain.CanvasArtifact, len(arts))
	copy(out, arts)
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// Get returns a single artifact by id, or nil when not found.
func (s *ArtifactStore) Get(conversationID, id string) *domain.CanvasArtifact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.store[conversationID] {
		if a.ID == id {
			cp := *a
			return &cp
		}
	}
	return nil
}

// Save inserts or updates an artifact (matched by ID) for the given
// conversation and persists to disk.
func (s *ArtifactStore) Save(conversationID string, a *domain.CanvasArtifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	arts := s.store[conversationID]
	for i, existing := range arts {
		if existing.ID == a.ID {
			cp := *a
			arts[i] = &cp
			return s.persistLocked()
		}
	}
	cp := *a
	s.store[conversationID] = append(arts, &cp)
	return s.persistLocked()
}

// Delete removes an artifact by id. No-op when not found.
func (s *ArtifactStore) Delete(conversationID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	arts := s.store[conversationID]
	for i, existing := range arts {
		if existing.ID == id {
			s.store[conversationID] = append(arts[:i], arts[i+1:]...)
			if len(s.store[conversationID]) == 0 {
				delete(s.store, conversationID)
			}
			return s.persistLocked()
		}
	}
	return nil
}

func (s *ArtifactStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return // missing file is fine
	}
	var current map[string][]*domain.CanvasArtifact
	if err := json.Unmarshal(b, &current); err != nil {
		return // corrupt file is fine — start empty
	}
	s.mu.Lock()
	s.store = current
	s.mu.Unlock()
}

func (s *ArtifactStore) persistLocked() error {
	b, err := json.MarshalIndent(s.store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
