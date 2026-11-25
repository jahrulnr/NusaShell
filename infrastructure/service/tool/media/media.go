// Package toolmedia offers the media models the owner assigned in
// Settings → Models as tools: generate_image, generate_video, speak, and
// transcribe. Which model runs is the application's decision
// (dservice.MediaModels); these tools read the files a call names, keep
// what comes back as files of the thread, and attach them so the thread
// shows them, chats receive them, and the model can see or reuse them.
package toolmedia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
	"github.com/jahrulnr/nusashell/infrastructure/system/patharg"
)

// maxReferences bounds the images one call may hand a model.
const maxReferences = 8

// TranscribeToolName is the catalog name of the transcription tool; the
// encoder's omitted-attachment placeholder points at it for audio a model
// cannot take, so the pointer and the tool cannot drift apart.
const TranscribeToolName = "transcribe"

// VideoWait bounds one generate_video call: a render that is still
// running then is reported as unfinished rather than holding the turn.
const VideoWait = 30 * time.Minute

// Deps are what every media tool needs.
type Deps struct {
	Models    dservice.MediaModels
	Outputs   drepo.ToolOutput
	Workspace drepo.Workspace
	// Root is the default directory relative paths resolve in when the
	// turn binds no workspace.
	Root string
}

func (d Deps) validate() error {
	if d.Models == nil || d.Outputs == nil || d.Workspace == nil {
		return errors.New("media tools need models, an output store, and a workspace")
	}
	return nil
}

// Tools builds the four media tools.
func Tools(d Deps) ([]dservice.Tool, error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	root, err := d.Workspace.CheckRoot(d.Root)
	if err != nil {
		return nil, err
	}
	d.Root = root
	specs := []struct {
		name, desc, params string
		run                func(context.Context, Deps, entity.ToolCall, dservice.ToolEmitter) (dservice.ToolResult, error)
	}{
		{"generate_image",
			"Make images with the image model set in Settings → Models. Give references (paths of images) to edit them or draw from them. The images are attached to the result and saved; the result names each saved path, which works as a reference or a video frame in later calls and can be copied into the workspace with exec.",
			`{"type":"object","properties":{
"prompt":{"type":"string","description":"What to draw, or how to change the references"},
"references":{"type":"array","items":{"type":"string"},"maxItems":8,"description":"Image paths (workspace-relative or absolute) to edit or draw from"},
"aspect_ratio":{"type":"string","description":"For example 1:1, 16:9, 9:16, 4:3"},
"size":{"type":"string","description":"Pixel size such as 1024x1024, or a tier such as 2K; leave out for the model's default"},
"count":{"type":"integer","minimum":1,"maximum":4,"description":"How many images; default 1"}},
"required":["prompt"]}`, generateImage},
		{"generate_video",
			"Make a video with the video model set in Settings → Models: from a prompt, from a first (and last) frame image, or from reference images or a soundtrack. Rendering takes minutes; progress is streamed. The video is attached to the result and saved; the result names its path.",
			`{"type":"object","properties":{
"prompt":{"type":"string","description":"What happens in the video"},
"first_frame":{"type":"string","description":"Path of the image the video opens on"},
"last_frame":{"type":"string","description":"Path of the image the video ends on"},
"references":{"type":"array","items":{"type":"string"},"maxItems":8,"description":"Paths of further reference images, or an audio file the video follows, for models that take them"},
"duration":{"type":"integer","minimum":1,"description":"Seconds; leave out for the model's default"},
"aspect_ratio":{"type":"string","description":"For example 16:9, 9:16, 1:1"},
"resolution":{"type":"string","description":"For example 720p or 1080p"},
"audio":{"type":"boolean","description":"Whether the model should also make sound, for models that can"}}}`, generateVideo},
		{"speak",
			"Read text aloud with the speech model set in Settings → Models. The audio is attached to the result (a chat receives it as a voice message) and saved; the result names its path.",
			`{"type":"object","properties":{
"text":{"type":"string","description":"What to say"},
"voice":{"type":"string","description":"The provider's voice id; leave out for the model's default"},
"instructions":{"type":"string","description":"How to say it: tone, pace, accent — for models that take guidance"},
"speed":{"type":"number","minimum":0.25,"maximum":4,"description":"1 is normal"}},
"required":["text"]}`, speak},
		{TranscribeToolName,
			"Turn speech in an audio or video file into text with the speech-to-text model set in Settings → Models.",
			`{"type":"object","properties":{
"path":{"type":"string","description":"Audio or video path (workspace-relative or absolute)"},
"language":{"type":"string","description":"ISO-639-1 code such as en or id; leave out to detect"},
"prompt":{"type":"string","description":"Names, terms, or context that help spell the transcript"}},
"required":["path"]}`, transcribe},
	}
	tools := make([]dservice.Tool, 0, len(specs))
	for _, s := range specs {
		schema, err := valueobject.NewToolSchema(s.name, s.desc, json.RawMessage(s.params), true, enum.ToolPermissionAgent)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
		tools = append(tools, &tool{schema: schema, deps: d, run: s.run})
	}
	return tools, nil
}

type tool struct {
	schema valueobject.ToolSchema
	deps   Deps
	run    func(context.Context, Deps, entity.ToolCall, dservice.ToolEmitter) (dservice.ToolResult, error)
}

func (t *tool) Schema() valueobject.ToolSchema { return t.schema }

func (t *tool) Execute(ctx context.Context, call entity.ToolCall, emit dservice.ToolEmitter) (dservice.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return dservice.ToolResult{}, err
	}
	return t.run(ctx, t.deps, call, emit)
}

func generateImage(ctx context.Context, d Deps, call entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	var in struct {
		Prompt      string   `json:"prompt"`
		References  []string `json:"references"`
		AspectRatio string   `json:"aspect_ratio"`
		Size        string   `json:"size"`
		Count       int      `json:"count"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &in); err != nil {
		return dservice.ToolResult{}, fmt.Errorf("invalid generate_image input: %w", err)
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return dservice.ToolResult{}, errors.New("prompt is required")
	}
	if len(in.References) > maxReferences {
		return dservice.ToolResult{}, fmt.Errorf("at most %d references", maxReferences)
	}
	req := dservice.ImageRequest{Prompt: in.Prompt, AspectRatio: in.AspectRatio, Size: in.Size, Count: in.Count}
	for _, p := range in.References {
		m, err := d.read(ctx, p, enum.ModalityImage)
		if err != nil {
			return dservice.ToolResult{}, err
		}
		req.References = append(req.References, m)
	}
	images, err := d.Models.GenerateImage(ctx, req)
	if err != nil {
		return dservice.ToolResult{}, err
	}
	return d.keep(ctx, call, images, "image")
}

func generateVideo(ctx context.Context, d Deps, call entity.ToolCall, emit dservice.ToolEmitter) (dservice.ToolResult, error) {
	var in struct {
		Prompt      string   `json:"prompt"`
		FirstFrame  string   `json:"first_frame"`
		LastFrame   string   `json:"last_frame"`
		References  []string `json:"references"`
		Duration    int      `json:"duration"`
		AspectRatio string   `json:"aspect_ratio"`
		Resolution  string   `json:"resolution"`
		Audio       *bool    `json:"audio"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &in); err != nil {
		return dservice.ToolResult{}, fmt.Errorf("invalid generate_video input: %w", err)
	}
	if strings.TrimSpace(in.Prompt) == "" && in.FirstFrame == "" {
		return dservice.ToolResult{}, errors.New("give a prompt, a first_frame, or both")
	}
	if len(in.References) > maxReferences {
		return dservice.ToolResult{}, fmt.Errorf("at most %d references", maxReferences)
	}
	req := dservice.VideoRequest{
		Prompt: in.Prompt, Duration: in.Duration, AspectRatio: in.AspectRatio,
		Resolution: in.Resolution, GenerateAudio: in.Audio,
	}
	for _, f := range []struct {
		path string
		dst  **dservice.Media
	}{{in.FirstFrame, &req.FirstFrame}, {in.LastFrame, &req.LastFrame}} {
		if f.path == "" {
			continue
		}
		m, err := d.read(ctx, f.path, enum.ModalityImage)
		if err != nil {
			return dservice.ToolResult{}, err
		}
		*f.dst = &m
	}
	for _, p := range in.References {
		m, err := d.read(ctx, p, enum.ModalityImage, enum.ModalityAudio, enum.ModalityVideo)
		if err != nil {
			return dservice.ToolResult{}, err
		}
		req.References = append(req.References, m)
	}
	waitCtx, cancel := context.WithTimeout(ctx, VideoWait)
	defer cancel()
	video, err := d.Models.GenerateVideo(waitCtx, req, func(status string) {
		if emit != nil {
			emit("video: " + status + "\n")
		}
	})
	if err != nil {
		if ctx.Err() != nil {
			return dservice.ToolResult{}, ctx.Err()
		}
		return dservice.ToolResult{}, err
	}
	return d.keep(ctx, call, []dservice.Media{video}, "video")
}

func speak(ctx context.Context, d Deps, call entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	var in struct {
		Text         string   `json:"text"`
		Voice        string   `json:"voice"`
		Instructions string   `json:"instructions"`
		Speed        *float64 `json:"speed"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &in); err != nil {
		return dservice.ToolResult{}, fmt.Errorf("invalid speak input: %w", err)
	}
	if strings.TrimSpace(in.Text) == "" {
		return dservice.ToolResult{}, errors.New("text is required")
	}
	audio, err := d.Models.Speak(ctx, dservice.SpeechRequest{Text: in.Text, Voice: in.Voice, Instructions: in.Instructions, Speed: in.Speed})
	if err != nil {
		return dservice.ToolResult{}, err
	}
	return d.keep(ctx, call, []dservice.Media{audio}, "audio")
}

func transcribe(ctx context.Context, d Deps, call entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	var in struct {
		Path     string `json:"path"`
		Language string `json:"language"`
		Prompt   string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &in); err != nil {
		return dservice.ToolResult{}, fmt.Errorf("invalid transcribe input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return dservice.ToolResult{}, errors.New("path is required")
	}
	audio, err := d.read(ctx, in.Path, enum.ModalityAudio, enum.ModalityVideo)
	if err != nil {
		return dservice.ToolResult{}, err
	}
	text, err := d.Models.Transcribe(ctx, dservice.TranscriptionRequest{Audio: audio, Language: in.Language, Prompt: in.Prompt})
	if err != nil {
		return dservice.ToolResult{}, err
	}
	if text == "" {
		return dservice.ToolResult{Text: "(no speech was heard)"}, nil
	}
	return dservice.ToolResult{Text: text}, nil
}

// read loads a file a call names: a relative path inside the turn's
// workspace (the repository session confines it), an absolute path on
// the host. Its type is sniffed from the bytes — the extension is the
// caller's word — and must be one of kinds; the size is capped for the
// kind before the read.
func (d Deps) read(ctx context.Context, path string, kinds ...enum.Modality) (m dservice.Media, retErr error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return dservice.Media{}, errors.New("an empty path was given")
	}
	expanded, err := patharg.Expand(path)
	if err != nil {
		return dservice.Media{}, err
	}
	path = expanded
	var f io.ReadCloser
	var size int64
	if filepath.IsAbs(path) {
		file, info, err := d.Workspace.OpenHost(ctx, path)
		if err != nil {
			return dservice.Media{}, fmt.Errorf("%q is not a readable regular file: %w", path, err)
		}
		f, size = file, info.Size()
	} else {
		dir := dservice.WorkspaceDir(ctx)
		if dir == "" {
			dir = d.Root
		}
		root, err := d.Workspace.Open(ctx, dir)
		if err != nil {
			return dservice.Media{}, err
		}
		defer func() {
			if err := root.Close(); retErr == nil {
				retErr = err
			}
		}()
		file, info, err := root.OpenRegular(path)
		if err != nil {
			return dservice.Media{}, fmt.Errorf("%q is not a readable regular file: %w", path, err)
		}
		f, size = file, info.Size()
	}
	defer f.Close()
	limit := int64(0)
	for _, k := range kinds {
		limit = max(limit, wire.MaxAttachmentBytes(k))
	}
	if size > limit {
		return dservice.Media{}, fmt.Errorf("%q is larger than the %d MiB limit", path, limit>>20)
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return dservice.Media{}, err
	}
	if int64(len(raw)) > limit {
		return dservice.Media{}, fmt.Errorf("%q is larger than the %d MiB limit", path, limit>>20)
	}
	mediaType := dservice.SniffMediaType(raw, path)
	att, err := valueobject.NewAttachment("data:", mediaType)
	if err != nil {
		return dservice.Media{}, err
	}
	for _, k := range kinds {
		if att.Modality() == k {
			return dservice.Media{MediaType: mediaType, Data: raw}, nil
		}
	}
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	return dservice.Media{}, fmt.Errorf("%q is %s, not %s", path, mediaType, strings.Join(names, " or "))
}

// keep stores each payload as a file of the thread and attaches it; one
// past the send-time cap for its kind is kept but not attached, so the
// model is never handed a reference that cannot resolve.
func (d Deps) keep(ctx context.Context, call entity.ToolCall, media []dservice.Media, kind string) (dservice.ToolResult, error) {
	convID := dservice.ConversationID(ctx)
	var out dservice.ToolResult
	var lines []string
	for i, m := range media {
		id := call.ID()
		if len(media) > 1 {
			id = fmt.Sprintf("%s-%d", id, i+1)
		}
		att, err := d.Outputs.Keep(ctx, convID, id, m.MediaType, m.Data)
		if err != nil {
			return dservice.ToolResult{}, fmt.Errorf("the %s was made but could not be saved: %w", kind, err)
		}
		path := strings.TrimPrefix(att.URI(), "file://")
		line := fmt.Sprintf("- %s (%s, %s)", path, m.MediaType, humanSize(len(m.Data)))
		if int64(len(m.Data)) > wire.MaxAttachmentBytes(att.Modality()) {
			line += " — too large to attach; saved only"
		} else {
			out.Attachments = append(out.Attachments, att)
		}
		lines = append(lines, line)
	}
	noun := kind
	if len(media) > 1 {
		noun += "s"
	}
	out.Text = fmt.Sprintf("made %d %s, attached and saved at:\n%s", len(media), noun, strings.Join(lines, "\n"))
	return out, nil
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
