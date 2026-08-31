package application

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/application/service/generatedmedia"
	"nusashell/domain"
)

const png1x1B64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

func png1x1(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(png1x1B64)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type memAttachmentStore struct {
	files map[string][]byte
	root  string
}

func (m *memAttachmentStore) Save(conversationID string, att domain.Attachment) (string, error) {
	if att.DataURL == "" {
		return "", nil
	}
	data, err := decodeAttachmentDataURL(att.DataURL)
	if err != nil {
		return "", err
	}
	return m.WriteBytes(conversationID, att.Name, data)
}

func (m *memAttachmentStore) WriteBytes(conversationID, name string, data []byte) (string, error) {
	if m.files == nil {
		m.files = map[string][]byte{}
	}
	path := filepath.Join(m.root, conversationID, name)
	m.files[path] = append([]byte(nil), data...)
	return path, nil
}

func (m *memAttachmentStore) ReadFile(absPath string) ([]byte, error) {
	data, ok := m.files[absPath]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

type scriptedImageGen struct {
	got    ImageGenRequest
	result *ImageGenResult
	err    error
	hits   int
}

func (s *scriptedImageGen) Generate(_ context.Context, req ImageGenRequest) (*ImageGenResult, error) {
	s.hits++
	s.got = req
	return s.result, s.err
}

func imageGenApp(t *testing.T, gen ImageGenerator, conv *domain.Conversation) *App {
	t.Helper()
	if conv == nil {
		conv = &domain.Conversation{ID: "c1"}
	}
	if conv.ID == "" {
		conv.ID = "c1"
	}
	return &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{conv.ID: conv}},
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"img": {ID: "img", Kind: domain.ProviderChat, Enabled: true, BaseURL: "https://api.openai.com/v1"},
		}},
		Credentials: &memCreds{m: map[string]string{"img": "sk-test"}},
		Attachments: &memAttachmentStore{root: t.TempDir()},
		ImageGeneratorFactory: func(p *domain.Provider, apiKey string) (ImageGenerator, error) {
			return gen, nil
		},
		Logs:         &fakeLogStore{},
		Bus:          NewBus(),
		retrySleeper: func(context.Context, time.Duration) error { return nil },
		imageGenSem:  make(chan struct{}, maxConcurrentImageGens),
	}
}

func TestParseGenerateImageArgs(t *testing.T) {
	got, err := parseGenerateImageArgs(`{"prompt":"a lighthouse","n":9,"size":"1024x1024"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.N != 4 {
		t.Fatalf("n clamp = %d, want 4", got.N)
	}
	if got.Size != "1024x1024" {
		t.Fatalf("size = %q", got.Size)
	}

	if _, err := parseGenerateImageArgs(`{}`); err == nil {
		t.Fatal("expected prompt required")
	}
	if _, err := parseGenerateImageArgs(`{"prompt":"x","size":"512x512"}`); err == nil {
		t.Fatal("expected invalid size")
	}
	if _, err := parseGenerateImageArgs(`{"prompt":"x","referenced_image_paths":["rel.png"]}`); err == nil {
		t.Fatal("expected absolute path error")
	}
	if _, err := parseGenerateImageArgs(`{"prompt":"x","referenced_image_paths":["/a","/b","/c","/d","/e","/f"]}`); err == nil {
		t.Fatal("expected max paths error")
	}
}

func TestExecuteGenerateImageRequiresProvider(t *testing.T) {
	app := imageGenApp(t, &scriptedImageGen{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	output, atts, err := app.executeGenerateImage(run, domain.ToolCall{
		ID: "tc_abc", Name: "generate_image", Args: `{"prompt":"a boat"}`,
	}, domain.Settings{})
	if err == nil {
		t.Fatal("expected unconfigured error")
	}
	if !strings.Contains(output, "Settings → Image generation") {
		t.Fatalf("output = %q", output)
	}
	if len(atts) != 0 {
		t.Fatalf("atts = %d", len(atts))
	}
}

func TestExecuteGenerateImageSavesWithoutPersistingDataURL(t *testing.T) {
	png := png1x1(t)
	gen := &scriptedImageGen{result: &ImageGenResult{
		Images:      []GeneratedImage{{Bytes: png, MediaType: "image/png"}},
		Provider:    "openai",
		Model:       "gpt-image-1",
		UsageTokens: 4175,
		CostUSD:     0.04,
	}}
	app := imageGenApp(t, gen, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	output, atts, err := app.executeGenerateImage(run, domain.ToolCall{
		ID: "tc_abc123", Name: "generate_image", Args: `{"prompt":"a red boat","size":"1024x1024"}`,
	}, domain.Settings{ImageProviderID: "img", ImageModelID: "gpt-image-1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gen.got.Prompt != "a red boat" || gen.got.N != 1 {
		t.Fatalf("request = %+v", gen.got)
	}
	if gen.got.TurnID != "tc_abc123" {
		t.Fatalf("TurnID = %q, want tool-call id", gen.got.TurnID)
	}
	if len(atts) != 1 {
		t.Fatalf("atts = %d", len(atts))
	}
	if atts[0].DataURL != "" {
		t.Fatal("DataURL must be stripped after save")
	}
	if !strings.HasSuffix(atts[0].FilePath, "gen-tc_abc123.png") {
		t.Fatalf("file_path = %q", atts[0].FilePath)
	}
	if atts[0].MediaType != "image/png" {
		t.Fatalf("media = %s", atts[0].MediaType)
	}
	// The "already displayed, do not re-render" policy lives in the
	// generate_media tool description (always visible to the model); the
	// result body only carries the unique edit hint.
	if !strings.Contains(output, "referenced_image_paths") {
		t.Fatalf("missing edit hint: %s", output)
	}
	if strings.Contains(output, "already displayed") {
		t.Fatalf("redundant UI hint must not be in the result body: %s", output)
	}
	if !strings.Contains(output, "status: completed") || !strings.Contains(output, "provider: openai") {
		t.Fatalf("yaml meta: %s", output)
	}
	if !strings.Contains(output, "usage_tokens: 4175") || !strings.Contains(output, "cost_usd: 0.04") {
		t.Fatalf("usage meta: %s", output)
	}
	store := app.Attachments.(*memAttachmentStore)
	if _, ok := store.files[atts[0].FilePath]; !ok {
		t.Fatalf("file not saved: %q", atts[0].FilePath)
	}
}

func TestExecuteGenerateImageLoadsReferencedPaths(t *testing.T) {
	png := png1x1(t)
	dir := t.TempDir()
	refPath := filepath.Join(dir, "prior.png")
	conv := &domain.Conversation{ID: "c1", Messages: []domain.Message{{
		ID:   "a1",
		Role: domain.RoleAssistant,
		ToolCalls: []domain.ToolCall{{
			ID: "old", Name: "generate_image",
			OutputAttachments: []domain.Attachment{{
				Type: "image", Name: "prior.png", MediaType: "image/png", FilePath: refPath,
			}},
		}},
	}}}
	gen := &scriptedImageGen{result: &ImageGenResult{
		Images:   []GeneratedImage{{Bytes: png, MediaType: "image/png"}},
		Provider: "openrouter",
		Model:    "openai/gpt-image-2",
	}}
	app := imageGenApp(t, gen, conv)
	// References are read straight from disk now — write a real file.
	saved := filepath.Join(dir, "prior.png")
	if err := os.WriteFile(saved, png, 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	args, _ := jsonGenerateArgs(saved)
	_, _, err := app.executeGenerateImage(run, domain.ToolCall{
		ID: "tc_edit", Name: "generate_image", Args: args,
	}, domain.Settings{ImageProviderID: "img", ImageModelID: "openai/gpt-image-2"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(gen.got.References) != 1 {
		t.Fatalf("references = %d", len(gen.got.References))
	}
	if string(gen.got.References[0].Data) != string(png) {
		t.Fatal("reference bytes mismatch")
	}
}

func TestExecuteGenerateImageRejectsI2IOnT2IOnlyModel(t *testing.T) {
	png := png1x1(t)
	dir := t.TempDir()
	refPath := filepath.Join(dir, "prior.png")
	if err := os.WriteFile(refPath, png, 0o644); err != nil {
		t.Fatal(err)
	}
	conv := &domain.Conversation{ID: "c1"}
	gen := &scriptedImageGen{result: &ImageGenResult{
		Images:   []GeneratedImage{{Bytes: png, MediaType: "image/png"}},
		Provider: "openrouter",
		Model:    "t2i-only",
	}}
	app := imageGenApp(t, gen, conv)
	// Override provider with a t2i-only model (Vision=false, Kind=image)
	app.Providers = &fakeProviderStore{items: map[string]*domain.Provider{
		"img": {ID: "img", Kind: domain.ProviderChat, Enabled: true, BaseURL: "https://api.openai.com/v1",
			Models: []domain.Model{{ID: "t2i-only", Kind: domain.ModelKindImage, Vision: false}}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	args, _ := jsonGenerateArgs(refPath)
	msg, _, err := app.executeGenerateImage(run, domain.ToolCall{
		ID: "tc_edit", Name: "generate_image", Args: args,
	}, domain.Settings{ImageProviderID: "img", ImageModelID: "t2i-only"})
	if err == nil {
		t.Fatal("expected error for i2i on t2i-only model")
	}
	if !strings.Contains(msg, "does not support image-to-image") {
		t.Errorf("error message = %q, want mention of image-to-image", msg)
	}
	if gen.hits > 0 {
		t.Fatal("generator should not have been called")
	}
}

func TestExecuteGenerateImageAllowsI2IOnVisionModel(t *testing.T) {
	png := png1x1(t)
	dir := t.TempDir()
	refPath := filepath.Join(dir, "prior.png")
	if err := os.WriteFile(refPath, png, 0o644); err != nil {
		t.Fatal(err)
	}
	conv := &domain.Conversation{ID: "c1"}
	gen := &scriptedImageGen{result: &ImageGenResult{
		Images:   []GeneratedImage{{Bytes: png, MediaType: "image/png"}},
		Provider: "openrouter",
		Model:    "i2i-capable",
	}}
	app := imageGenApp(t, gen, conv)
	app.Providers = &fakeProviderStore{items: map[string]*domain.Provider{
		"img": {ID: "img", Kind: domain.ProviderChat, Enabled: true, BaseURL: "https://api.openai.com/v1",
			Models: []domain.Model{{ID: "i2i-capable", Kind: domain.ModelKindImage, Vision: true}}},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	args, _ := jsonGenerateArgs(refPath)
	_, _, err := app.executeGenerateImage(run, domain.ToolCall{
		ID: "tc_edit", Name: "generate_image", Args: args,
	}, domain.Settings{ImageProviderID: "img", ImageModelID: "i2i-capable"})
	if err != nil {
		t.Fatalf("expected success for i2i on vision model: %v", err)
	}
	if len(gen.got.References) != 1 {
		t.Fatalf("references = %d, want 1", len(gen.got.References))
	}
}

func jsonGenerateArgs(refPath string) (string, error) {
	b, err := jsonMarshalPrompt(refPath)
	return string(b), err
}

func jsonMarshalPrompt(refPath string) ([]byte, error) {
	return fmt.Appendf(nil, `{"prompt":"make it dusk","referenced_image_paths":[%q]}`, refPath), nil
}

func TestGenerateImageWithRetryRetries503(t *testing.T) {
	png := png1x1(t)
	flaky := &flakyImageGen{png: png}
	app := imageGenApp(t, flaky, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	_, atts, err := app.executeGenerateImage(run, domain.ToolCall{
		ID: "tc1", Name: "generate_image", Args: `{"prompt":"x"}`,
	}, domain.Settings{ImageProviderID: "img", ImageModelID: "gpt-image-1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if flaky.hits != 2 {
		t.Fatalf("hits = %d, want 2", flaky.hits)
	}
	if len(atts) != 1 {
		t.Fatalf("atts = %d", len(atts))
	}
}

type flakyImageGen struct {
	hits int
	png  []byte
}

func (f *flakyImageGen) Generate(_ context.Context, _ ImageGenRequest) (*ImageGenResult, error) {
	f.hits++
	if f.hits == 1 {
		return nil, &UpstreamError{Kind: KindHTTPStatus, StatusCode: 503, Temporary: true, Err: fmt.Errorf("overloaded")}
	}
	return &ImageGenResult{
		Images:   []GeneratedImage{{Bytes: f.png, MediaType: "image/png"}},
		Provider: "openai",
		Model:    "gpt-image-1",
	}, nil
}

func TestChatMessagesForProviderHydratesFilePath(t *testing.T) {
	png := png1x1(t)
	store := &memAttachmentStore{root: t.TempDir()}
	path, err := store.WriteBytes("c1", "gen-tc.png", png)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Attachments: store}
	conv := &domain.Conversation{Messages: []domain.Message{
		{ID: "a1", Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{
			ID: "tc", Name: "generate_image", Output: "saved",
			OutputAttachments: []domain.Attachment{{
				Type: "image", Name: "gen-tc.png", MediaType: "image/png", FilePath: path,
			}},
		}}},
	}}
	msgs := app.chatMessagesForProvider(conv, "", ModelCapabilities{Vision: true})
	var tool *ChatMessage
	for i := range msgs {
		if msgs[i].Role == "tool" {
			tool = &msgs[i]
			break
		}
	}
	if tool == nil || tool.ToolResult == nil || len(tool.ToolResult.Attachments) != 1 {
		t.Fatalf("tool message = %+v", msgs)
	}
	att := tool.ToolResult.Attachments[0]
	if att.DataURL == "" || !strings.HasPrefix(att.DataURL, "data:image/png;base64,") {
		t.Fatalf("hydrated DataURL = %q", att.DataURL)
	}
}

func TestPersistGeneratedImagesNamesIndexedWhenNGreaterThanOne(t *testing.T) {
	png := png1x1(t)
	app := &App{Attachments: &memAttachmentStore{root: t.TempDir()}}
	atts, paths, err := app.persistGeneratedImages("c1", "tc-n", &ImageGenResult{
		Images: []GeneratedImage{
			{Bytes: png, MediaType: "image/png"},
			{Bytes: png, MediaType: "image/png"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 2 || len(paths) != 2 {
		t.Fatalf("n = %d paths = %d", len(atts), len(paths))
	}
	if !strings.HasSuffix(atts[0].Name, "gen-tc-n-1.png") || !strings.HasSuffix(atts[1].Name, "gen-tc-n-2.png") {
		t.Fatalf("names = %q %q", atts[0].Name, atts[1].Name)
	}
}

func TestPersistGeneratedImagesRejectsOversize(t *testing.T) {
	app := &App{Attachments: &memAttachmentStore{root: t.TempDir()}}
	huge := make([]byte, generatedmedia.MaxGeneratedImageBytes+1)
	copy(huge, png1x1(t))
	_, _, err := app.persistGeneratedImages("c1", "tc", &ImageGenResult{
		Images: []GeneratedImage{{Bytes: huge, MediaType: "image/png"}},
	})
	if err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("err = %v", err)
	}
}

func TestFormatImageGenFailureRateLimit(t *testing.T) {
	err := &UpstreamError{StatusCode: 429, RetryAfter: 2 * time.Minute, Err: fmt.Errorf("429")}
	msg := formatImageGenFailure(err)
	if !strings.Contains(msg, "rate-limited") || !strings.Contains(msg, "2m0s") {
		t.Fatalf("msg = %q", msg)
	}
}
