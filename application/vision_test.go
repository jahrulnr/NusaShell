package application

import (
	"context"
	"encoding/json"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- from vision_test.go ---

func containsImageOmissionNote(content string) bool {
	return domain.ContainsOmissionNote(content, "image")
}

func TestChatMessagesStripsImagesForNonVisionModel(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo=", FilePath: "/data/attachments/c1/cat.png"},
				{Type: "text", Name: "note.txt", MediaType: "text/plain", Content: "see image"},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "I see a cat", Status: domain.StatusDone},
	}}

	// Non-vision model: image stripped, text attachment kept, placeholder added
	got := chatMessages(c, "", ModelCapabilities{})
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	userMsg := got[0]
	if userMsg.Role != "user" {
		t.Fatalf("first message role = %q, want user", userMsg.Role)
	}
	hasImage := false
	for _, a := range userMsg.Attachments {
		if a.Type == "image" {
			hasImage = true
		}
	}
	if hasImage {
		t.Error("image attachment should be stripped for non-vision model")
	}
	hasText := false
	for _, a := range userMsg.Attachments {
		if a.Type == "text" {
			hasText = true
		}
	}
	if !hasText {
		t.Error("text attachment should be preserved")
	}
	if !containsImageOmissionNote(userMsg.Content) {
		t.Errorf("content should contain image omission placeholder, got: %q", userMsg.Content)
	}
	// The placeholder must include the absolute file path and tell the model
	// to call read_media with file_path.
	if !strings.Contains(userMsg.Content, "/data/attachments/c1/cat.png") {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "read_media") {
		t.Errorf("placeholder should mention read_media tool, got: %q", userMsg.Content)
	}
}

func TestChatMessagesPlaceholderIncludesFilePath(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "photo.jpg", MediaType: "image/jpeg", DataURL: "data:image/jpeg;base64,/9j/4AAQ=", FilePath: "/home/user/.config/nusashell/attachments/conv_1/photo.jpg"},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ok", Status: domain.StatusDone},
	}}

	got := chatMessages(c, "", ModelCapabilities{})
	userMsg := got[0]
	// The placeholder must include the absolute file path so file-based
	// tools can access the image directly.
	if !strings.Contains(userMsg.Content, "/home/user/.config/nusashell/attachments/conv_1/photo.jpg") {
		t.Errorf("placeholder should include absolute file path, got: %q", userMsg.Content)
	}
}

func TestChatMessagesKeepsImagesForVisionModel(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "I see a cat", Status: domain.StatusDone},
	}}

	// Vision model: image kept, no placeholder
	got := chatMessages(c, "", ModelCapabilities{Vision: true})
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	userMsg := got[0]
	hasImage := false
	for _, a := range userMsg.Attachments {
		if a.Type == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Error("image attachment should be kept for vision model")
	}
	if containsImageOmissionNote(userMsg.Content) {
		t.Error("content should NOT contain image omission placeholder for vision model")
	}
}

func TestChatMessagesStripsImagesFromHistoryWhenSwitchingModel(t *testing.T) {
	// Simulate: turn 1 with vision model (image in history),
	// turn 2 with non-vision model (image should be stripped from history)
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "I see a cat on a windowsill", Status: domain.StatusDone},
		{ID: "u2", Role: domain.RoleUser, Content: "What color is it?"},
	}}

	// Non-vision model: image from u1 stripped, but assistant response a1 preserved
	got := chatMessages(c, "", ModelCapabilities{})
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
	// u1 should have no image
	u1 := got[0]
	for _, a := range u1.Attachments {
		if a.Type == "image" {
			t.Error("image from u1 should be stripped for non-vision model")
		}
	}
	if !containsImageOmissionNote(u1.Content) {
		t.Errorf("u1 content should contain placeholder, got: %q", u1.Content)
	}
	// a1 should be intact
	if got[1].Content != "I see a cat on a windowsill" {
		t.Errorf("assistant response should be preserved, got: %q", got[1].Content)
	}
}

func TestModelSupportsVision(t *testing.T) {
	// Model with Vision=true
	p := &domain.Provider{Models: []domain.Model{
		{ID: "gpt-5.5", Vision: true},
		{ID: "gpt-4", Vision: false},
	}}
	if !domain.ModelCapabilitiesOf(p, "gpt-5.5").Vision {
		t.Error("gpt-5.5 should support vision")
	}
	if domain.ModelCapabilitiesOf(p, "gpt-4").Vision {
		t.Error("gpt-4 should NOT support vision")
	}
	// Unknown model: default true (backward compat)
	if !domain.ModelCapabilitiesOf(p, "unknown-model").Vision {
		t.Error("unknown model should default to vision=true")
	}
	// Nil provider: default true
	if !domain.ModelCapabilitiesOf(nil, "any").Vision {
		t.Error("nil provider should default to vision=true")
	}
}

func TestChatMessagesVisionModelGetsImagePathNote(t *testing.T) {
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "Edit this image",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo=", FilePath: "/data/attachments/c1/cat.png"},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ok", Status: domain.StatusDone},
	}}

	got := chatMessages(c, "", ModelCapabilities{Vision: true})
	userMsg := got[0]

	// The image pixels must be kept visible for a vision model.
	hasImage := false
	for _, a := range userMsg.Attachments {
		if a.Type == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Error("image attachment should be kept for vision model")
	}
	// The absolute file path is surfaced so the model can reference it for i2i.
	if !strings.Contains(userMsg.Content, "/data/attachments/c1/cat.png") {
		t.Errorf("vision content should include the image file path, got %q", userMsg.Content)
	}
	if !strings.Contains(userMsg.Content, "referenced_image_paths") {
		t.Errorf("vision content should mention referenced_image_paths, got %q", userMsg.Content)
	}
	// It must NOT be the non-vision omission placeholder.
	if containsImageOmissionNote(userMsg.Content) {
		t.Error("vision content must not contain the omission placeholder")
	}
}

func TestChatMessagesVisionModelNoNoteWithoutPath(t *testing.T) {
	// Legacy attachment with no FilePath: image kept, no note appended.
	c := &domain.Conversation{Messages: []domain.Message{
		{
			ID:      "u1",
			Role:    domain.RoleUser,
			Content: "What's in this image?",
			Attachments: []domain.Attachment{
				{Type: "image", Name: "cat.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
			},
		},
		{ID: "a1", Role: domain.RoleAssistant, Content: "a cat", Status: domain.StatusDone},
	}}

	got := chatMessages(c, "", ModelCapabilities{Vision: true})
	userMsg := got[0]
	if userMsg.Content != "What's in this image?" {
		t.Errorf("content should be unchanged when image has no file path, got %q", userMsg.Content)
	}
}

// --- from vision_fallback_test.go ---

// fakeVisionAdapter is a minimal core.Provider that returns a canned
// description for any Chat call. Used to test describeImagesWithFallback
// without a real provider.
type fakeVisionAdapter struct {
	description string
	// reasoningOnly simulates reasoning models (e.g. dots-3-note) that put
	// their output in Reasoning instead of Content.
	reasoningOnly bool
	calls         int
	lastReq       *core.Request
}

func (f *fakeVisionAdapter) Name() string { return "fake-vision" }

func (f *fakeVisionAdapter) Chat(_ context.Context, req *core.Request) (*core.Response, error) {
	f.calls++
	f.lastReq = req
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
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
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
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
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
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
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
		Factory: func(ctx context.Context, p *domain.Provider, apiKey string) (AIProvider, error) {
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

// TestDescribeImagesWithFallbackUsesResolvedMaxOutput is the 400-token
// truncation bug: describeOneImage hardcoded MaxTokens=400, which cut off
// UI descriptions (and reasoning-model thinking) even though the user
// prompt's "400 words" is only a soft hint. The fallback must use the
// same ResolveMaxOutput ceiling as a normal completion.
func TestDescribeImagesWithFallbackUsesResolvedMaxOutput(t *testing.T) {
	adapter := &fakeVisionAdapter{description: "a detailed UI screenshot"}
	app := visionFallbackTestApp(adapter, nil)
	settings := domain.Settings{
		VisionProviderID: "vision-prov",
		VisionModelID:    "gpt-4o",
		MaxOutputTokens:  8192,
	}
	atts := []domain.Attachment{
		{Type: "image", Name: "ui.png", MediaType: "image/png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
	}
	_ = app.describeImagesWithFallback(context.Background(), settings, atts)
	if adapter.lastReq == nil {
		t.Fatal("expected vision Chat to be called")
	}
	got := 0
	if adapter.lastReq.MaxTokens != nil {
		got = *adapter.lastReq.MaxTokens
	}
	want := domain.ResolveMaxOutput(&domain.Provider{ID: "vision-prov", Enabled: true, Kind: domain.ProviderChat}, "gpt-4o", settings)
	if got != want {
		t.Fatalf("MaxTokens=%d, want resolved max output %d (not a hardcoded 400)", got, want)
	}
}

// --- from vision_read_image_test.go ---

// testAbsPath returns an absolute path under dir (a t.TempDir). On Windows,
// root-anchored paths without a drive letter (\\data\...) are NOT absolute
// per filepath.IsAbs, so the dir parameter must be a platform-native
// absolute base (t.TempDir()).
func testAbsPath(dir string, parts ...string) string {
	return filepath.Join(append([]string{dir}, parts...)...)
}

// filePathArgs builds a read_media args JSON with the given file path.
// encoding/json escapes the path properly (backslashes on Windows).
func filePathArgs(path string, question string) string {
	m := map[string]string{"file_path": path}
	if question != "" {
		m["question"] = question
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// writeTestFile writes a small media payload with real binary magic
// numbers so loadMediaAttachment's magic-number validation accepts it.
// The file does not need to be a decodable media file — only the
// leading bytes must match the expected magic signature.
func writeTestFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	ext := strings.ToLower(filepath.Ext(name))
	var payload []byte
	switch ext {
	case ".png":
		// PNG signature + minimal IHDR chunk header
		payload = append(
			[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A},
			[]byte("\x00\x00\x00\x0DIHDR\x00\x00\x00\x01")...,
		)
	case ".jpg", ".jpeg":
		payload = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	case ".gif":
		payload = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00")
	case ".webp":
		payload = []byte("RIFF\x00\x00\x00\x00WEBP")
	case ".bmp":
		payload = []byte("BM\x00\x00\x00\x00")
	case ".mp3":
		payload = []byte("ID3\x03\x00\x00\x00\x00\x00\x00")
	case ".wav":
		payload = []byte("RIFF\x00\x00\x00\x00WAVE")
	case ".ogg":
		payload = []byte("OggS\x00\x00\x00\x00")
	case ".flac":
		payload = []byte("fLaC\x00\x00\x00\x22")
	case ".mp4", ".m4v":
		payload = []byte("\x00\x00\x00\x20ftypisom\x00\x00\x00\x00isomiso2avc1mp41")
	case ".webm":
		payload = []byte("\x1aE\xdf\xa3\x01\x00\x00\x00\x00\x00\x00\x1fB\x82\x88webm")
	case ".mov":
		payload = []byte("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00")
	case ".avi":
		payload = []byte("RIFF\x00\x00\x00\x00AVI ")
	default:
		t.Fatalf("writeTestFile: no magic payload for extension %q (file %s)", ext, name)
	}
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExecuteReadImageVisionModel(t *testing.T) {
	dir := t.TempDir()
	catPath := writeTestFile(t, dir, "cat.png")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(catPath, "what color is the cat?"),
	}

	output, atts, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Vision-model tool output is the file path only; the image is delivered
	// as an attachment and reinjected as a user message by chat-compat
	// providers (or carried inside the tool result by native providers).
	if !strings.Contains(output, catPath) {
		t.Errorf("output should be the file path, got: %q", output)
	}
	// Question echo was removed from vision-model output (the model already
	// knows its own question — echoing it wastes tokens).
	if strings.Contains(output, "what color") {
		t.Errorf("vision output should NOT echo the question, got: %q", output)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment (image), got %d", len(atts))
	}
	if atts[0].Type != "image" || atts[0].Name != "cat.png" {
		t.Errorf("attachment should be the image, got %q %q", atts[0].Type, atts[0].Name)
	}
	if atts[0].MediaType != "image/png" {
		t.Errorf("media type should be image/png, got %q", atts[0].MediaType)
	}
	if atts[0].DataURL == "" {
		t.Error("attachment should carry inline data URL")
	}
}

func TestExecuteReadImageOutsideWorkspace(t *testing.T) {
	// Absolute path outside any workspace/attachment root must load fine.
	dir := t.TempDir()
	sub := filepath.Join(dir, "elsewhere", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeTestFile(t, sub, "photo.jpg")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(path, ""),
	}

	output, atts, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error for path outside attachment roots: %v", err)
	}
	if !strings.Contains(output, path) {
		t.Errorf("output should be the file path, got: %q", output)
	}
	if len(atts) != 1 || atts[0].MediaType != "image/jpeg" {
		t.Fatalf("expected jpeg attachment from arbitrary path, got %+v", atts)
	}
}

func TestExecuteReadImageNonVisionNoFallback(t *testing.T) {
	dir := t.TempDir()
	catPath := writeTestFile(t, dir, "cat.png")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(catPath, ""),
	}

	output, atts, err := app.executeReadImage(run, toolCall, ModelCapabilities{}, domain.Settings{})
	// No fallback configured → returns error message, no attachments
	if err != nil {
		t.Fatalf("expected graceful error message, not Go error: %v", err)
	}
	if !strings.Contains(output, "does not support image input") {
		t.Errorf("output should explain no vision support, got: %q", output)
	}
	if len(atts) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(atts))
	}
}

func TestExecuteReadImageImageNotFound(t *testing.T) {
	dir := t.TempDir()
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(testAbsPath(dir, "nonexistent.png"), ""),
	}

	output, _, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for nonexistent image")
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("output should mention not found, got: %q", output)
	}
}

func TestExecuteReadImageMissingArgs(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{}`,
	}

	output, _, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for missing file_path")
	}
	if !strings.Contains(output, "file_path is required") {
		t.Errorf("output should mention missing arg, got: %q", output)
	}
}

func TestExecuteReadImageRejectsRelativePath(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{"file_path":"cat.png"}`,
	}

	output, _, err := app.executeReadImage(run, toolCall, ModelCapabilities{Vision: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for relative path")
	}
	if !strings.Contains(output, "absolute") {
		t.Errorf("output should mention absolute path required, got: %q", output)
	}
}
