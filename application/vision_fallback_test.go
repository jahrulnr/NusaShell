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
	calls         int
}

func (f *fakeVisionAdapter) Name() string { return "fake-vision" }

func (f *fakeVisionAdapter) Chat(_ context.Context, _ *core.Request) (*core.Response, error) {
	f.calls++
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

func visionFallbackTestApp(adapter *fakeVisionAdapter, factoryCalls *int) *App {
	return &App{
		Logs: &fakeLogStore{},
		Bus:  NewBus(),
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"vision-prov": {ID: "vision-prov", Enabled: true, Kind: domain.ProviderChat},
		}},
		Credentials: &fakeVisionCredStore{creds: map[string]string{"vision-prov": "key"}},
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (core.Provider, error) {
			if factoryCalls != nil {
				*factoryCalls++
			}
			return adapter, nil
		},
	}
}

// TestDescribeImagesWithFallbackSkipsExistingDescription is the retry bug:
// a preserved image plus an existing vision:<name> text must not call the
// fallback model again or append a duplicate description.
func TestDescribeImagesWithFallbackSkipsExistingDescription(t *testing.T) {
	adapter := &fakeVisionAdapter{description: "should not run"}
	var factoryCalls int
	app := visionFallbackTestApp(adapter, &factoryCalls)
	settings := domain.Settings{VisionProviderID: "vision-prov", VisionModelID: "gpt-4o"}
	atts := []domain.Attachment{
		{Type: "image", Name: "image.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
		{Type: "text", Name: "vision:image.png", MediaType: "text/plain", Content: "[Image description for image.png]\nexisting"},
	}
	out := app.describeImagesWithFallback(context.Background(), settings, atts)
	if len(out) != 2 {
		t.Fatalf("expected unchanged attachments, got %d", len(out))
	}
	if factoryCalls != 0 {
		t.Fatalf("fallback factory called %d times, want 0", factoryCalls)
	}
	if adapter.calls != 0 {
		t.Fatalf("vision Chat called %d times, want 0", adapter.calls)
	}
	nDesc := 0
	for _, a := range out {
		if a.Name == "vision:image.png" {
			nDesc++
		}
	}
	if nDesc != 1 {
		t.Fatalf("description attachments = %d, want 1", nDesc)
	}
}

func TestDescribeImagesWithFallbackDescribesOnlyNewImages(t *testing.T) {
	adapter := &fakeVisionAdapter{description: "a dog on a sofa"}
	var factoryCalls int
	app := visionFallbackTestApp(adapter, &factoryCalls)
	settings := domain.Settings{VisionProviderID: "vision-prov", VisionModelID: "gpt-4o"}
	atts := []domain.Attachment{
		{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
		{Type: "text", Name: "vision:cat.png", MediaType: "text/plain", Content: "[Image description for cat.png]\nalready described"},
		{Type: "image", Name: "dog.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
	}
	out := app.describeImagesWithFallback(context.Background(), settings, atts)
	if adapter.calls != 1 {
		t.Fatalf("vision Chat called %d times, want 1 (only the undescribed image)", adapter.calls)
	}
	nCat, nDog := 0, 0
	for _, a := range out {
		switch a.Name {
		case "vision:cat.png":
			nCat++
		case "vision:dog.png":
			nDog++
			if !strings.Contains(a.Content, "dog on a sofa") {
				t.Errorf("new description should contain fallback output, got: %q", a.Content)
			}
		}
	}
	if nCat != 1 || nDog != 1 {
		t.Fatalf("vision:cat.png=%d vision:dog.png=%d, want 1 and 1 (got %d attachments)", nCat, nDog, len(out))
	}
}

func TestEnrichWithVisionDescriptionsSkipsWhenAlreadyDescribed(t *testing.T) {
	adapter := &fakeVisionAdapter{description: "should not run"}
	var factoryCalls int
	app := visionFallbackTestApp(adapter, &factoryCalls)
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{
				ID:   "u1",
				Role: domain.RoleUser,
				Attachments: []domain.Attachment{
					{Type: "image", Name: "image.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
					{Type: "text", Name: "vision:image.png", MediaType: "text/plain", Content: "[Image description for image.png]\nexisting"},
				},
			},
			{ID: "a1", Role: domain.RoleAssistant, Status: domain.StatusDone},
		},
	}
	app.Conversations = &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	settings := domain.Settings{VisionProviderID: "vision-prov", VisionModelID: "gpt-4o"}
	out := app.enrichWithVisionDescriptions(context.Background(), conv, "a1", settings)
	if factoryCalls != 0 || adapter.calls != 0 {
		t.Fatalf("retry enrich called factory=%d chat=%d, want 0/0", factoryCalls, adapter.calls)
	}
	nDesc := 0
	for _, a := range out.Messages[0].Attachments {
		if a.Name == "vision:image.png" {
			nDesc++
		}
	}
	if nDesc != 1 {
		t.Fatalf("description attachments = %d, want 1", nDesc)
	}
}
