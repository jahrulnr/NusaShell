package jsonstore

import (
	"path/filepath"
	"testing"

	"nusashell/domain"
)

func TestArtifactStoreSaveAndGet(t *testing.T) {
	s := NewArtifactStore(filepath.Join(t.TempDir(), "artifacts.json"))
	art := &domain.CanvasArtifact{
		ID:    "art_1",
		Title: "Test",
		HTML:  "<p>hello</p>",
	}
	if err := s.Save("conv1", art); err != nil {
		t.Fatal(err)
	}
	got := s.Get("conv1", "art_1")
	if got == nil {
		t.Fatal("artifact not found after save")
	}
	if got.Title != "Test" {
		t.Errorf("title = %q, want %q", got.Title, "Test")
	}
}

func TestArtifactStoreSaveUpdatesExisting(t *testing.T) {
	s := NewArtifactStore(filepath.Join(t.TempDir(), "artifacts.json"))
	art := &domain.CanvasArtifact{ID: "art_1", Title: "Original", HTML: "<p/>"}
	s.Save("conv1", art)
	updated := &domain.CanvasArtifact{ID: "art_1", Title: "Patched", HTML: "<p>new</p>"}
	if err := s.Save("conv1", updated); err != nil {
		t.Fatal(err)
	}
	got := s.Get("conv1", "art_1")
	if got.Title != "Patched" {
		t.Errorf("title = %q, want %q", got.Title, "Patched")
	}
	if got.HTML != "<p>new</p>" {
		t.Errorf("html not updated")
	}
	if len(s.List("conv1")) != 1 {
		t.Errorf("List should return 1 artifact, got %d", len(s.List("conv1")))
	}
}

func TestArtifactStoreDelete(t *testing.T) {
	s := NewArtifactStore(filepath.Join(t.TempDir(), "artifacts.json"))
	s.Save("conv1", &domain.CanvasArtifact{ID: "art_1", Title: "A"})
	s.Save("conv1", &domain.CanvasArtifact{ID: "art_2", Title: "B"})
	if err := s.Delete("conv1", "art_1"); err != nil {
		t.Fatal(err)
	}
	if s.Get("conv1", "art_1") != nil {
		t.Error("artifact still present after delete")
	}
	if len(s.List("conv1")) != 1 {
		t.Errorf("List should return 1 after delete, got %d", len(s.List("conv1")))
	}
}

func TestArtifactStoreListIsolatedPerConversation(t *testing.T) {
	s := NewArtifactStore(filepath.Join(t.TempDir(), "artifacts.json"))
	s.Save("conv1", &domain.CanvasArtifact{ID: "art_1", Title: "A"})
	s.Save("conv2", &domain.CanvasArtifact{ID: "art_2", Title: "B"})
	if len(s.List("conv1")) != 1 {
		t.Errorf("conv1 should have 1 artifact, got %d", len(s.List("conv1")))
	}
	if len(s.List("conv2")) != 1 {
		t.Errorf("conv2 should have 1 artifact, got %d", len(s.List("conv2")))
	}
}

func TestArtifactStorePersistsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts.json")
	s1 := NewArtifactStore(path)
	s1.Save("conv1", &domain.CanvasArtifact{ID: "art_1", Title: "Persisted"})
	s2 := NewArtifactStore(path)
	got := s2.Get("conv1", "art_1")
	if got == nil {
		t.Fatal("artifact not persisted across instances")
	}
	if got.Title != "Persisted" {
		t.Errorf("title = %q, want %q", got.Title, "Persisted")
	}
}
