package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// fakeVisionAdapter is a minimal core.Provider that returns a canned
// description for any Chat call. Used to test describeImagesWithFallback
// without a real provider.
type fakeVisionAdapter struct {
	description string
	// reasoningOnly simulates reasoning models (e.g. dots-3-note) that put
	// their output in Reasoning instead of Content.
	reasoningOnly bool
}

func (f *fakeVisionAdapter) Name() string { return "fake-vision" }

func (f *fakeVisionAdapter) Chat(_ context.Context, _ *core.Request) (*core.Response, error) {
	resp := &core.Response{FinishReason: core.FinishReasonStop}
	if f.reasoningOnly {
		resp.Blocks = append(resp.Blocks, core.ReasoningBlock{Text: f.description})
	} else {
		resp.Blocks = append(resp.Blocks, core.TextBlock{Text: f.description})
	}
	return resp, nil
}

func (f *fakeVisionAdapter) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	resp, err := f.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &stubStream{events: coreResponseEvents(resp)}, nil
}

// fakeVisionCredStore is a minimal CredentialStore for vision tests.
type fakeVisionCredStore struct {
	creds map[string]string
}

func (f *fakeVisionCredStore) Get(providerID string) (string, bool, error) {
	key, ok := f.creds[providerID]
	return key, ok, nil
}
func (f *fakeVisionCredStore) Set(providerID, key string) error { return nil }
func (f *fakeVisionCredStore) Delete(providerID string) error   { return nil }
func (f *fakeVisionCredStore) ListByPrefix(prefix string) ([]string, error) {
	var out []string
	for k := range f.creds {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}

func TestDescribeImagesWithFallbackNoImages(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"vision-prov": {ID: "vision-prov", Enabled: true, Kind: domain.ProviderChat},
		}},
		Credentials: &fakeVisionCredStore{creds: map[string]string{"vision-prov": "key"}},
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error) {
			return &fakeVisionAdapter{description: "a cat"}, nil
		},
	}
	settings := domain.Settings{VisionProviderID: "vision-prov", VisionModelID: "gpt-4o"}
	atts := []domain.Attachment{
		{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "hello"},
	}
	out := app.describeImagesWithFallback(context.Background(), settings, atts)
	if len(out) != 1 {
		t.Fatalf("expected 1 attachment (no images to describe), got %d", len(out))
	}
}

func TestDescribeImagesWithFallbackNoConfig(t *testing.T) {
	app := &App{}
	settings := domain.Settings{} // no vision fallback configured
	atts := []domain.Attachment{
		{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
	}
	out := app.describeImagesWithFallback(context.Background(), settings, atts)
	if len(out) != 1 {
		t.Fatalf("expected unchanged attachments when fallback not configured, got %d", len(out))
	}
}

func TestDescribeImagesWithFallbackAddsDescription(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"vision-prov": {ID: "vision-prov", Enabled: true, Kind: domain.ProviderChat},
		}},
		Credentials: &fakeVisionCredStore{creds: map[string]string{"vision-prov": "key"}},
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error) {
			return &fakeVisionAdapter{description: "A orange cat sitting on a windowsill"}, nil
		},
	}
	settings := domain.Settings{VisionProviderID: "vision-prov", VisionModelID: "gpt-4o"}
	atts := []domain.Attachment{
		{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
		{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "see image"},
	}
	out := app.describeImagesWithFallback(context.Background(), settings, atts)
	if len(out) != 3 {
		t.Fatalf("expected 3 attachments (2 original + 1 description), got %d", len(out))
	}
	// Original image preserved
	if out[0].Type != "image" {
		t.Errorf("original image should be preserved at index 0, got type %q", out[0].Type)
	}
	// Original text preserved
	if out[1].Type != "text" || out[1].Name != "note.txt" {
		t.Errorf("original text should be preserved at index 1, got %q %q", out[1].Type, out[1].Name)
	}
	// Description appended
	desc := out[2]
	if desc.Type != "text" {
		t.Errorf("description should be text type, got %q", desc.Type)
	}
	if !strings.Contains(desc.Name, "cat.png") {
		t.Errorf("description name should reference original image, got %q", desc.Name)
	}
	if !strings.Contains(desc.Content, "orange cat") {
		t.Errorf("description content should contain vision model output, got: %q", desc.Content)
	}
}

func TestDescribeImagesWithFallbackProviderNotFound(t *testing.T) {
	app := &App{
		Logs:      &fakeLogStore{},
		Bus:       NewBus(),
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{}},
	}
	settings := domain.Settings{VisionProviderID: "nonexistent", VisionModelID: "gpt-4o"}
	atts := []domain.Attachment{
		{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
	}
	out := app.describeImagesWithFallback(context.Background(), settings, atts)
	if len(out) != 1 {
		t.Fatalf("expected unchanged attachments when provider not found, got %d", len(out))
	}
}

// TestDescribeImagesWithFallbackReasoningOnlyModel tests reasoning models
// (e.g. dots-3-note-preview) that put their output in the reasoning field
// instead of content. The fallback must still produce a description.
func TestDescribeImagesWithFallbackReasoningOnlyModel(t *testing.T) {
	app := &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"vision-prov": {ID: "vision-prov", Enabled: true, Kind: domain.ProviderChat},
		}},
		Credentials: &fakeVisionCredStore{creds: map[string]string{"vision-prov": "key"}},
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error) {
			return &fakeVisionAdapter{
				description:   "A photo of a sunset over the ocean",
				reasoningOnly: true,
			}, nil
		},
	}
	settings := domain.Settings{VisionProviderID: "vision-prov", VisionModelID: "dots-3-note"}
	atts := []domain.Attachment{
		{Type: "image", Name: "sunset.jpg", MediaType: "image/jpeg", DataURL: "data:image/jpeg;base64,/9j/4AAQ="},
	}
	out := app.describeImagesWithFallback(context.Background(), settings, atts)
	if len(out) != 2 {
		t.Fatalf("expected 2 attachments (image + description), got %d", len(out))
	}
	desc := out[1]
	if desc.Type != "text" {
		t.Errorf("description should be text type, got %q", desc.Type)
	}
	if !strings.Contains(desc.Content, "sunset") {
		t.Errorf("description should contain reasoning output, got: %q", desc.Content)
	}
}
