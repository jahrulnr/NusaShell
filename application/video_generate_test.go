package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

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
