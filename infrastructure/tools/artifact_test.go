package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nusashell/application"
	"nusashell/domain"
)

// stubArtifactStore is an in-memory CanvasArtifactStore for tests.
type stubArtifactStore struct {
	store map[string][]*domain.CanvasArtifact
}

func newStubArtifactStore() *stubArtifactStore {
	return &stubArtifactStore{store: make(map[string][]*domain.CanvasArtifact)}
}

func (s *stubArtifactStore) List(convID string) []*domain.CanvasArtifact {
	out := make([]*domain.CanvasArtifact, len(s.store[convID]))
	copy(out, s.store[convID])
	return out
}
func (s *stubArtifactStore) Get(convID, id string) *domain.CanvasArtifact {
	for _, a := range s.store[convID] {
		if a.ID == id {
			cp := *a
			return &cp
		}
	}
	return nil
}
func (s *stubArtifactStore) Save(convID string, a *domain.CanvasArtifact) error {
	for i, existing := range s.store[convID] {
		if existing.ID == a.ID {
			s.store[convID][i] = a
			return nil
		}
	}
	s.store[convID] = append(s.store[convID], a)
	return nil
}
func (s *stubArtifactStore) Delete(convID, id string) error {
	arts := s.store[convID]
	for i, existing := range arts {
		if existing.ID == id {
			s.store[convID] = append(arts[:i], arts[i+1:]...)
			return nil
		}
	}
	return nil
}

func artifactToolbox(t *testing.T) *Toolbox {
	t.Helper()
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.Artifacts = newStubArtifactStore()
	return tb
}

func artifactCtx(convID string) context.Context {
	return application.WithConversationID(context.Background(), convID)
}

func TestArtifactCreateReturnsArtifact(t *testing.T) {
	tb := artifactToolbox(t)
	ctx := artifactCtx("conv1")
	out, err := tb.Execute(ctx, "artifact_create", mustJSON(t, map[string]any{
		"html":  "<h1>Hello</h1>",
		"css":   "h1{color:red}",
		"js":    "console.log('hi')",
		"title": "Test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Artifact struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			HTML  string `json:"html"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Artifact.ID == "" {
		t.Fatal("artifact id is empty")
	}
	if parsed.Artifact.Title != "Test" {
		t.Errorf("title = %q, want %q", parsed.Artifact.Title, "Test")
	}
	if parsed.Artifact.HTML != "<h1>Hello</h1>" {
		t.Errorf("html mismatch")
	}
}

func TestArtifactCreateRequiresConversationContext(t *testing.T) {
	tb := artifactToolbox(t)
	_, err := tb.Execute(context.Background(), "artifact_create", mustJSON(t, map[string]any{"html": "<p/>"}))
	if err == nil {
		t.Fatal("expected error for missing conversation context")
	}
}

func TestArtifactCreateRejectsEmptyContent(t *testing.T) {
	tb := artifactToolbox(t)
	ctx := artifactCtx("conv1")
	_, err := tb.Execute(ctx, "artifact_create", mustJSON(t, map[string]any{}))
	if err == nil {
		t.Fatal("expected error for empty artifact")
	}
}

func TestArtifactUpdatePartialReplace(t *testing.T) {
	tb := artifactToolbox(t)
	ctx := artifactCtx("conv1")
	out, err := tb.Execute(ctx, "artifact_create", mustJSON(t, map[string]any{
		"html":  "<p>original</p>",
		"title": "Original",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Artifact struct {
			ID string `json:"id"`
		} `json:"artifact"`
	}
	json.Unmarshal([]byte(out), &created)
	id := created.Artifact.ID

	// Update only js — html and title should be preserved.
	out2, err := tb.Execute(ctx, "artifact_update", mustJSON(t, map[string]any{
		"id": id,
		"js": "console.log('patched')",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var updated struct {
		Artifact struct {
			HTML  string `json:"html"`
			JS    string `json:"js"`
			Title string `json:"title"`
		} `json:"artifact"`
	}
	json.Unmarshal([]byte(out2), &updated)
	if updated.Artifact.HTML != "<p>original</p>" {
		t.Errorf("html should be preserved, got %q", updated.Artifact.HTML)
	}
	if updated.Artifact.JS != "console.log('patched')" {
		t.Errorf("js should be patched, got %q", updated.Artifact.JS)
	}
	if updated.Artifact.Title != "Original" {
		t.Errorf("title should be preserved, got %q", updated.Artifact.Title)
	}
}

func TestArtifactUpdateNotFound(t *testing.T) {
	tb := artifactToolbox(t)
	ctx := artifactCtx("conv1")
	_, err := tb.Execute(ctx, "artifact_update", mustJSON(t, map[string]any{
		"id":   "art_nonexistent",
		"html": "<p/>",
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent artifact")
	}
}

func TestArtifactListReturnsCreatedArtifacts(t *testing.T) {
	tb := artifactToolbox(t)
	ctx := artifactCtx("conv1")
	tb.Execute(ctx, "artifact_create", mustJSON(t, map[string]any{"html": "<p>1</p>", "title": "First"}))
	tb.Execute(ctx, "artifact_create", mustJSON(t, map[string]any{"html": "<p>2</p>", "title": "Second"}))
	out, err := tb.Execute(ctx, "artifact_list", mustJSON(t, map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Count     int `json:"count"`
		Artifacts []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"artifacts"`
	}
	json.Unmarshal([]byte(out), &parsed)
	if parsed.Count != 2 {
		t.Errorf("count = %d, want 2", parsed.Count)
	}
}

func TestArtifactDeleteRemovesArtifact(t *testing.T) {
	tb := artifactToolbox(t)
	ctx := artifactCtx("conv1")
	out, _ := tb.Execute(ctx, "artifact_create", mustJSON(t, map[string]any{"html": "<p/>"}))
	var created struct {
		Artifact struct {
			ID string `json:"id"`
		} `json:"artifact"`
	}
	json.Unmarshal([]byte(out), &created)
	id := created.Artifact.ID

	_, err := tb.Execute(ctx, "artifact_delete", mustJSON(t, map[string]any{"id": id}))
	if err != nil {
		t.Fatal(err)
	}
	out2, _ := tb.Execute(ctx, "artifact_list", mustJSON(t, map[string]any{}))
	var parsed struct {
		Count int `json:"count"`
	}
	json.Unmarshal([]byte(out2), &parsed)
	if parsed.Count != 0 {
		t.Errorf("count after delete = %d, want 0", parsed.Count)
	}
}

func TestArtifactCreateRejectsOversized(t *testing.T) {
	tb := artifactToolbox(t)
	ctx := artifactCtx("conv1")
	big := strings.Repeat("x", artifactMaxBytes+1)
	_, err := tb.Execute(ctx, "artifact_create", mustJSON(t, map[string]any{"html": big}))
	if err == nil {
		t.Fatal("expected error for oversized artifact")
	}
}

func TestArtifactToolsAdvertisedWhenStoreConfigured(t *testing.T) {
	tb := artifactToolbox(t)
	tools := tb.ListTools()
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, expected := range []string{"artifact_create", "artifact_update", "artifact_read", "artifact_list", "artifact_delete"} {
		if !names[expected] {
			t.Errorf("artifact tool %q not advertised when Artifacts store is configured", expected)
		}
	}
}

func TestArtifactToolsNotAdvertisedWhenStoreNil(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	tools := tb.ListTools()
	for _, tool := range tools {
		if strings.HasPrefix(tool.Name, "artifact_") {
			t.Errorf("artifact tool %q should not be advertised when Artifacts store is nil", tool.Name)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
