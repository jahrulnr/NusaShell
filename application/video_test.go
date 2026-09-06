package application

import (
	"context"
	"encoding/json"
	"nusashell/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- from video_read_video_test.go ---

func TestExecuteReadVideoNative(t *testing.T) {
	dir := t.TempDir()
	videoPath := writeTestFile(t, dir, "clip.mp4")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(videoPath, "what happens in the video?"),
	}

	output, atts, err := app.executeReadVideo(run, toolCall, ModelCapabilities{Video: true}, domain.Settings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(output, "Video loaded") {
		t.Errorf("output should mention video loaded, got: %q", output)
	}
	// Question echo removed from native video output (model already knows).
	if strings.Contains(output, "what happens") {
		t.Errorf("video output should NOT echo the question, got: %q", output)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment (video), got %d", len(atts))
	}
	if atts[0].Type != "video" || atts[0].Name != "clip.mp4" {
		t.Errorf("attachment should be the video, got %q %q", atts[0].Type, atts[0].Name)
	}
	if atts[0].MediaType != "video/mp4" || atts[0].DataURL == "" {
		t.Errorf("attachment should be inline mp4 video, got %q url=%v", atts[0].MediaType, atts[0].DataURL != "")
	}
}

func TestExecuteReadVideoNonVideoNoFallback(t *testing.T) {
	dir := t.TempDir()
	videoPath := writeTestFile(t, dir, "clip.mp4")
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(videoPath, ""),
	}

	output, atts, err := app.executeReadVideo(run, toolCall, ModelCapabilities{}, domain.Settings{})
	if err != nil {
		t.Fatalf("expected graceful error message, not Go error: %v", err)
	}
	if !strings.Contains(output, "does not support video input") {
		t.Errorf("output should explain no video support, got: %q", output)
	}
	if len(atts) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(atts))
	}
}

func TestExecuteReadVideoVideoNotFound(t *testing.T) {
	dir := t.TempDir()
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: filePathArgs(testAbsPath(dir, "nonexistent.mp4"), ""),
	}

	output, _, err := app.executeReadVideo(run, toolCall, ModelCapabilities{Video: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for nonexistent video")
	}
	if !strings.Contains(output, "not found") {
		t.Errorf("output should mention not found, got: %q", output)
	}
}

func TestExecuteReadVideoMissingArgs(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{}`,
	}

	output, _, err := app.executeReadVideo(run, toolCall, ModelCapabilities{Video: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for missing file_path")
	}
	if !strings.Contains(output, "file_path is required") {
		t.Errorf("output should mention missing arg, got: %q", output)
	}
}

func TestExecuteReadVideoRejectsRelativePath(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	toolCall := domain.ToolCall{
		ID:   "tc1",
		Name: "read_media",
		Args: `{"file_path":"clip.mp4"}`,
	}

	output, _, err := app.executeReadVideo(run, toolCall, ModelCapabilities{Video: true}, domain.Settings{})
	if err == nil {
		t.Error("expected error for relative path")
	}
	if !strings.Contains(output, "absolute") {
		t.Errorf("output should mention absolute path required, got: %q", output)
	}
}

func TestDescribeVideosWithFallbackSkipsExistingDescription(t *testing.T) {
	adapter := &fakeVisionAdapter{description: "should not run"}
	var factoryCalls int
	app := visionFallbackTestApp(adapter, &factoryCalls)
	settings := domain.Settings{VideoProviderID: "vision-prov", VideoModelID: "gpt-4o"}
	atts := []domain.Attachment{
		{Type: "video", Name: "clip.mp4", MediaType: "video/mp4", DataURL: "data:video/mp4;base64,AAA="},
		{Type: "text", Name: "video:clip.mp4", MediaType: "text/plain", Content: "[Video description for clip.mp4]\nexisting"},
	}
	out := app.describeVideosWithFallback(context.Background(), settings, atts)
	if len(out) != 2 {
		t.Fatalf("expected unchanged attachments, got %d", len(out))
	}
	if factoryCalls != 0 || adapter.calls != 0 {
		t.Fatalf("video fallback called factory=%d chat=%d, want 0/0", factoryCalls, adapter.calls)
	}
}

// --- from video_generate_test.go ---

type scriptedVideoGen struct {
	got    VideoGenRequest
	result *VideoGenResult
	err    error
	hits   int
}

func (s *scriptedVideoGen) Generate(_ context.Context, req VideoGenRequest) (*VideoGenResult, error) {
	s.hits++
	s.got = req
	return s.result, s.err
}

func videoGenApp(t *testing.T, gen VideoGenerator) *App {
	t.Helper()
	return &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {ID: "c1"}}},
		Providers: &fakeProviderStore{items: map[string]*domain.Provider{
			"vid": {ID: "vid", Kind: domain.ProviderChat, Enabled: true, BaseURL: "https://openrouter.ai/api/v1"},
		}},
		Credentials: &memCreds{m: map[string]string{"vid": "sk-test"}},
		Attachments: &memAttachmentStore{root: t.TempDir()},
		VideoGeneratorFactory: func(p *domain.Provider, apiKey string) (VideoGenerator, error) {
			return gen, nil
		},
		Logs:         &fakeLogStore{},
		Bus:          NewBus(),
		retrySleeper: func(context.Context, time.Duration) error { return nil },
	}
}

// minimalMP4 returns bytes with a valid MP4 ftyp box header so
// saveGeneratedMedia's magic-byte validation passes.
func minimalMP4() []byte {
	return []byte{0x00, 0x00, 0x00, 0x14, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0x00, 0x00, 0x02, 0x00, 'i', 's', 'o', 'm'}
}

func TestExecuteGenerateVideoT2V(t *testing.T) {
	gen := &scriptedVideoGen{result: &VideoGenResult{
		Video: minimalMP4(), MediaType: "video/mp4", Ext: "mp4",
		Provider: "openrouter-videos", Model: "x-ai/grok-imagine-video",
	}}
	app := videoGenApp(t, gen)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	args, _ := json.Marshal(map[string]any{"prompt": "a sunset", "duration_seconds": 4, "resolution": "720p"})
	_, atts, err := app.executeGenerateVideo(run, domain.ToolCall{
		ID: "tc1", Name: "generate_video", Args: string(args),
	}, domain.Settings{VideoGenProviderID: "vid", VideoGenModelID: "x-ai/grok-imagine-video"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("atts = %d, want 1", len(atts))
	}
	if len(gen.got.References) != 0 {
		t.Fatalf("references = %d, want 0 for t2v", len(gen.got.References))
	}
}

func TestExecuteGenerateVideoI2V(t *testing.T) {
	png := png1x1(t)
	dir := t.TempDir()
	refPath := filepath.Join(dir, "source.png")
	if err := os.WriteFile(refPath, png, 0o644); err != nil {
		t.Fatal(err)
	}
	gen := &scriptedVideoGen{result: &VideoGenResult{
		Video: minimalMP4(), MediaType: "video/mp4", Ext: "mp4",
		Provider: "openrouter-videos", Model: "google/veo-3.1",
	}}
	app := videoGenApp(t, gen)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	args, _ := json.Marshal(map[string]any{
		"prompt":                 "camera pans right",
		"duration_seconds":       4,
		"resolution":             "720p",
		"referenced_image_paths": []string{refPath},
	})
	msg, atts, err := app.executeGenerateVideo(run, domain.ToolCall{
		ID: "tc1", Name: "generate_video", Args: string(args),
	}, domain.Settings{VideoGenProviderID: "vid", VideoGenModelID: "google/veo-3.1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("atts = %d, want 1", len(atts))
	}
	if len(gen.got.References) != 1 {
		t.Fatalf("references = %d, want 1", len(gen.got.References))
	}
	if string(gen.got.References[0].Data) != string(png) {
		t.Fatal("reference bytes mismatch")
	}
	if !strings.Contains(msg, "1 reference") {
		t.Errorf("message should mention reference count, got: %s", msg)
	}
}

func TestExecuteGenerateVideoRejectsRelativeRefPath(t *testing.T) {
	gen := &scriptedVideoGen{result: &VideoGenResult{Video: []byte("x"), MediaType: "video/mp4"}}
	app := videoGenApp(t, gen)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: ctx, Cancel: cancel}
	args, _ := json.Marshal(map[string]any{
		"prompt":                 "test",
		"referenced_image_paths": []string{"relative/path.png"},
	})
	_, _, err := app.executeGenerateVideo(run, domain.ToolCall{
		ID: "tc1", Name: "generate_video", Args: string(args),
	}, domain.Settings{VideoGenProviderID: "vid", VideoGenModelID: "x"})
	if err == nil {
		t.Fatal("expected error for relative path")
	}
	if gen.hits > 0 {
		t.Fatal("generator should not have been called")
	}
}
