package toolmedia

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

type models struct {
	image dservice.ImageRequest
	video dservice.VideoRequest
	audio dservice.TranscriptionRequest
}

func (m *models) GenerateImage(_ context.Context, req dservice.ImageRequest) ([]dservice.Media, error) {
	m.image = req
	return []dservice.Media{{MediaType: "image/png", Data: png}, {MediaType: "image/png", Data: png}}, nil
}

func (m *models) Speak(context.Context, dservice.SpeechRequest) (dservice.Media, error) {
	return dservice.Media{MediaType: "audio/mpeg", Data: []byte("ID3")}, nil
}

func (m *models) Transcribe(_ context.Context, req dservice.TranscriptionRequest) (string, error) {
	m.audio = req
	return "halo", nil
}

func (m *models) GenerateVideo(_ context.Context, req dservice.VideoRequest, progress dservice.VideoProgress) (dservice.Media, error) {
	m.video = req
	progress("in_progress")
	return dservice.Media{MediaType: "video/mp4", Data: []byte("mp4")}, nil
}

func setup(t *testing.T) (map[string]dservice.Tool, *models, string, context.Context) {
	t.Helper()
	store := t.TempDir()
	if err := os.Mkdir(filepath.Join(store, "c1"), 0o700); err != nil {
		t.Fatal(err)
	}
	outputs, err := repoconversation.NewToolOutputRepository(store)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	m := &models{}
	tools, err := Tools(Deps{Models: m, Outputs: outputs, Workspace: repoworkspace.NewLocal(), Root: ws})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]dservice.Tool{}
	for _, tool := range tools {
		if !tool.Schema().CallableByAgent() {
			t.Fatalf("%s is not offered to the model", tool.Schema().Name())
		}
		byName[tool.Schema().Name()] = tool
	}
	ctx := dservice.WithConversationID(dservice.WithWorkspaceDir(context.Background(), ws), "c1")
	return byName, m, ws, ctx
}

func call(t *testing.T, name, input string) entity.ToolCall {
	t.Helper()
	c, err := entity.NewToolCall("call_1", name, input, enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGenerateImageKeepsAndAttaches(t *testing.T) {
	tools, m, ws, ctx := setup(t)
	if err := os.WriteFile(filepath.Join(ws, "ref.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := tools["generate_image"].Execute(ctx, call(t, "generate_image", `{"prompt":"a fox","references":["ref.png"],"aspect_ratio":"1:1","count":2}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.image.Prompt != "a fox" || m.image.AspectRatio != "1:1" || len(m.image.References) != 1 || m.image.References[0].MediaType != "image/png" {
		t.Fatalf("request = %+v", m.image)
	}
	if len(res.Attachments) != 2 || res.Attachments[0].URI() == res.Attachments[1].URI() {
		t.Fatalf("attachments = %v", res.Attachments)
	}
	for _, a := range res.Attachments {
		path := strings.TrimPrefix(a.URI(), "file://")
		if !strings.Contains(res.Text, path) || a.SHA256() == "" || filepath.Ext(path) != ".png" {
			t.Fatalf("result %q does not name %s (pin %q)", res.Text, path, a.SHA256())
		}
		// The saved path works as a reference in a later call.
		if _, err := tools["generate_image"].Execute(ctx, call(t, "generate_image", `{"prompt":"again","references":["`+path+`"]}`), nil); err != nil {
			t.Fatalf("saved image as reference: %v", err)
		}
	}
}

func TestMediaInputsAreChecked(t *testing.T) {
	tools, _, ws, ctx := setup(t)
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("just text"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"generate_image": `{"prompt":"p","references":["notes.txt"]}`,
		"transcribe":     `{"path":"notes.txt"}`,
		"generate_video": `{"prompt":"p","first_frame":"../escape.png"}`,
	} {
		if _, err := tools[name].Execute(ctx, call(t, name, input), nil); err == nil {
			t.Errorf("%s accepted %s", name, input)
		}
	}
	if _, err := tools["generate_image"].Execute(ctx, call(t, "generate_image", `{}`), nil); err == nil {
		t.Error("generate_image accepted no prompt")
	}
	if _, err := tools["generate_video"].Execute(ctx, call(t, "generate_video", `{}`), nil); err == nil {
		t.Error("generate_video accepted neither prompt nor frame")
	}
}

func TestTranscribeVoiceNote(t *testing.T) {
	tools, m, ws, ctx := setup(t)
	ogg := append([]byte("OggS\x00\x02"), make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(ws, "note.ogg"), ogg, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := tools["transcribe"].Execute(ctx, call(t, "transcribe", `{"path":"note.ogg","language":"id"}`), nil)
	if err != nil || res.Text != "halo" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if m.audio.Audio.MediaType != "audio/ogg" || m.audio.Language != "id" {
		t.Fatalf("request = %s %s", m.audio.Audio.MediaType, m.audio.Language)
	}
}

func TestSpeakAndVideoAttach(t *testing.T) {
	tools, m, ws, ctx := setup(t)
	res, err := tools["speak"].Execute(ctx, call(t, "speak", `{"text":"halo"}`), nil)
	if err != nil || len(res.Attachments) != 1 || res.Attachments[0].Modality() != enum.ModalityAudio {
		t.Fatalf("speak = %+v, %v", res, err)
	}
	if err := os.WriteFile(filepath.Join(ws, "first.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	var streamed []string
	res, err = tools["generate_video"].Execute(ctx, call(t, "generate_video", `{"prompt":"p","first_frame":"first.png","duration":5}`),
		func(chunk string) { streamed = append(streamed, chunk) })
	if err != nil || len(res.Attachments) != 1 || res.Attachments[0].MediaType() != "video/mp4" {
		t.Fatalf("video = %+v, %v", res, err)
	}
	if m.video.FirstFrame == nil || m.video.Duration != 5 || len(streamed) == 0 {
		t.Fatalf("request = %+v, streamed %q", m.video, streamed)
	}
}
