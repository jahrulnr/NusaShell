// Package corevideo is the video-generation family. Every surface is a
// job: submit, poll until it settles, then fetch the file. It speaks
// the /videos surface OpenRouter and compatible hosts share (OpenAI's
// own takes different fields and its references as files), and
// Gemini's predictLongRunning operations. The surface follows the
// model's wire style; OpenAI's dialect is keyed off its host.
package corevideo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
)

// DefaultPollInterval spaces status checks: a video takes minutes, and
// a tighter loop only spends the provider's rate limit.
const DefaultPollInterval = 5 * time.Second

type Service struct {
	c    *coremedia.Client
	poll time.Duration
}

// New builds the service; poll ≤ 0 takes DefaultPollInterval.
func New(c *coremedia.Client, poll time.Duration) *Service {
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	return &Service{c: c, poll: poll}
}

// job is the polled state every surface maps onto.
type job struct {
	done     bool
	failure  string
	status   string
	download string // where the finished file is fetched from
}

func (s *Service) GenerateVideo(ctx context.Context, prov *aggregate.Provider, model entity.Model, req dservice.VideoRequest, progress dservice.VideoProgress) (dservice.Media, error) {
	if strings.TrimSpace(req.Prompt) == "" && req.FirstFrame == nil {
		return dservice.Media{}, apperror.New(400, "a video needs a prompt or a first frame", nil)
	}
	style := model.WireStyle(prov.Style())
	if style == enum.StyleMessages {
		return dservice.Media{}, apperror.New(400, "this provider's messages surface cannot make video", nil)
	}
	var submit func(context.Context) (string, error)
	var check func(context.Context, string) (job, error)
	switch {
	case style == enum.StyleGenerateContent:
		submit = func(ctx context.Context) (string, error) { return s.submitGemini(ctx, prov, model, req) }
		check = func(ctx context.Context, name string) (job, error) { return s.checkGemini(ctx, prov, name) }
	case enum.OpenAI.MatchesEndpoint(prov.Endpoint()):
		submit = func(ctx context.Context) (string, error) { return s.submitOpenAI(ctx, prov, model, req) }
		check = func(ctx context.Context, id string) (job, error) { return s.checkREST(ctx, prov, style, id) }
	default:
		submit = func(ctx context.Context) (string, error) { return s.submitREST(ctx, prov, model, req) }
		check = func(ctx context.Context, id string) (job, error) { return s.checkREST(ctx, prov, style, id) }
	}
	id, err := submit(ctx)
	if err != nil {
		return dservice.Media{}, err
	}
	report := func(status string) {
		if progress != nil && status != "" {
			progress(status)
		}
	}
	report("submitted")
	stillRendering := func() error {
		return apperror.New(408, "the video was still rendering when the wait ended (job "+id+")", ctx.Err())
	}
	var last string
	for {
		j, err := check(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return dservice.Media{}, stillRendering()
			}
			return dservice.Media{}, err
		}
		if j.status != last {
			report(j.status)
			last = j.status
		}
		if j.failure != "" {
			return dservice.Media{}, apperror.New(502, "the video failed: "+j.failure, nil)
		}
		if j.done {
			if j.download == "" {
				return dservice.Media{}, apperror.New(502, "the video finished without a file", nil)
			}
			resp, err := s.c.Download(ctx, prov, style, j.download, wire.MaxAttachmentBytes(enum.ModalityVideo))
			if err != nil {
				return dservice.Media{}, err
			}
			mt := coremedia.MediaType(resp.Header.Get("Content-Type"), resp.Body)
			if !strings.HasPrefix(mt, "video/") {
				mt = "video/mp4"
			}
			return dservice.Media{MediaType: mt, Data: resp.Body}, nil
		}
		select {
		case <-ctx.Done():
			return dservice.Media{}, stillRendering()
		case <-time.After(s.poll):
		}
	}
}

// submitREST posts the JSON job OpenRouter and compatible hosts take:
// frames pin the first and last image, references carry further images
// or sound.
func (s *Service) submitREST(ctx context.Context, prov *aggregate.Provider, model entity.Model, req dservice.VideoRequest) (string, error) {
	type mediaURL struct {
		URL string `json:"url"`
	}
	type part struct {
		Type      string    `json:"type"`
		ImageURL  *mediaURL `json:"image_url,omitempty"`
		AudioURL  *mediaURL `json:"audio_url,omitempty"`
		VideoURL  *mediaURL `json:"video_url,omitempty"`
		FrameType string    `json:"frame_type,omitempty"`
	}
	toPart := func(m dservice.Media) part {
		u := &mediaURL{URL: wire.DataURI(m.MediaType, m.Data)}
		switch {
		case strings.HasPrefix(m.MediaType, "audio/"):
			return part{Type: "audio_url", AudioURL: u}
		case strings.HasPrefix(m.MediaType, "video/"):
			return part{Type: "video_url", VideoURL: u}
		}
		return part{Type: "image_url", ImageURL: u}
	}
	var frames, refs []part
	if req.FirstFrame != nil {
		p := toPart(*req.FirstFrame)
		p.FrameType = "first_frame"
		frames = append(frames, p)
	}
	if req.LastFrame != nil {
		p := toPart(*req.LastFrame)
		p.FrameType = "last_frame"
		frames = append(frames, p)
	}
	for _, r := range req.References {
		refs = append(refs, toPart(r))
	}
	body, err := coremedia.JSON(struct {
		Model         string `json:"model"`
		Prompt        string `json:"prompt,omitempty"`
		Duration      int    `json:"duration,omitempty"`
		Resolution    string `json:"resolution,omitempty"`
		AspectRatio   string `json:"aspect_ratio,omitempty"`
		FrameImages   []part `json:"frame_images,omitempty"`
		References    []part `json:"input_references,omitempty"`
		GenerateAudio *bool  `json:"generate_audio,omitempty"`
	}{model.ID(), req.Prompt, req.Duration, req.Resolution, req.AspectRatio, frames, refs, req.GenerateAudio})
	if err != nil {
		return "", err
	}
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodPost, Path: "videos", Body: body, ContentType: "application/json",
		Style: model.WireStyle(prov.Style()), Log: "video.submit",
	})
	if err != nil {
		return "", err
	}
	return jobID(resp.Body)
}

// submitOpenAI posts OpenAI's own job shape: seconds and pixel size, and
// a first frame as an uploaded file.
func (s *Service) submitOpenAI(ctx context.Context, prov *aggregate.Provider, model entity.Model, req dservice.VideoRequest) (string, error) {
	if req.LastFrame != nil || len(req.References) > 0 {
		return "", apperror.New(400, "OpenAI video takes only a first frame as a reference", nil)
	}
	seconds := ""
	if req.Duration > 0 {
		seconds = strconv.Itoa(req.Duration)
	}
	size := req.Resolution
	if !strings.Contains(size, "x") {
		size = openAISize(req.AspectRatio, req.Resolution)
	}
	form := coremedia.NewForm()
	form.Field("model", model.ID())
	form.Field("prompt", req.Prompt)
	form.Field("seconds", seconds)
	form.Field("size", size)
	if req.FirstFrame != nil {
		form.File("input_reference", "first-frame"+frameExt(req.FirstFrame.MediaType), req.FirstFrame.MediaType, req.FirstFrame.Data)
	}
	body, contentType, err := form.Close()
	if err != nil {
		return "", err
	}
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodPost, Path: "videos", Body: body, ContentType: contentType,
		Style: model.WireStyle(prov.Style()), Log: "video.submit",
	})
	if err != nil {
		return "", err
	}
	return jobID(resp.Body)
}

func jobID(body []byte) (string, error) {
	if err := coremedia.EnvelopeError(body); err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &out) != nil || out.ID == "" {
		return "", apperror.New(502, "the provider accepted no video job", nil)
	}
	return out.ID, nil
}

// checkREST polls videos/{id}. Both dialects answer a status; OpenRouter
// adds where the file lives, OpenAI serves it at videos/{id}/content.
func (s *Service) checkREST(ctx context.Context, prov *aggregate.Provider, style enum.Style, id string) (job, error) {
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodGet, Path: "videos/" + url.PathEscape(id), Style: style, Log: "video.poll",
	})
	if err != nil {
		return job{}, err
	}
	var out struct {
		Status       string          `json:"status"`
		StatusDetail string          `json:"status_detail"`
		Progress     *float64        `json:"progress"`
		Error        json.RawMessage `json:"error"`
		URLs         []string        `json:"urls"`
		UnsignedURLs []string        `json:"unsigned_urls"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return job{}, apperror.New(502, "malformed upstream body", err)
	}
	status := out.Status
	if out.StatusDetail != "" {
		status += " (" + out.StatusDetail + ")"
	}
	if out.Progress != nil {
		status += fmt.Sprintf(" %.0f%%", *out.Progress)
	}
	j := job{status: status}
	switch out.Status {
	case "completed", "succeeded":
		j.done = true
		switch {
		case len(out.UnsignedURLs) > 0:
			j.download = out.UnsignedURLs[0]
		case len(out.URLs) > 0:
			j.download = out.URLs[0]
		default:
			j.download = "videos/" + url.PathEscape(id) + "/content"
		}
	case "failed", "cancelled", "canceled", "expired":
		j.failure = errorText(out.Error)
		if j.failure == "" {
			j.failure = out.Status
		}
	}
	return j, nil
}

// submitGemini starts a predictLongRunning operation and answers its name.
func (s *Service) submitGemini(ctx context.Context, prov *aggregate.Provider, model entity.Model, req dservice.VideoRequest) (string, error) {
	if len(req.References) > 0 {
		return "", apperror.New(400, "this video surface takes only first and last frames as references", nil)
	}
	instance := map[string]any{"prompt": req.Prompt}
	image := func(m *dservice.Media) map[string]string {
		return map[string]string{"bytesBase64Encoded": base64.StdEncoding.EncodeToString(m.Data), "mimeType": m.MediaType}
	}
	if req.FirstFrame != nil {
		instance["image"] = image(req.FirstFrame)
	}
	if req.LastFrame != nil {
		instance["lastFrame"] = image(req.LastFrame)
	}
	params := map[string]any{}
	if req.AspectRatio != "" {
		params["aspectRatio"] = req.AspectRatio
	}
	if req.Resolution != "" {
		params["resolution"] = req.Resolution
	}
	if req.Duration > 0 {
		params["durationSeconds"] = req.Duration
	}
	if req.GenerateAudio != nil {
		params["generateAudio"] = *req.GenerateAudio
	}
	body, err := coremedia.JSON(map[string]any{"instances": []any{instance}, "parameters": params})
	if err != nil {
		return "", err
	}
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodPost, Path: coremedia.GeminiURL(prov, model.ID(), "predictLongRunning"),
		Body: body, ContentType: "application/json", Style: enum.StyleGenerateContent, Log: "video.submit",
	})
	if err != nil {
		return "", err
	}
	var op struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(resp.Body, &op) != nil || op.Name == "" {
		return "", apperror.New(502, "the provider accepted no video job", nil)
	}
	return op.Name, nil
}

// checkGemini reads an operation; a finished one names the sample's file.
func (s *Service) checkGemini(ctx context.Context, prov *aggregate.Provider, name string) (job, error) {
	// The operation name is a relative resource path ("models/x/operations/y");
	// each segment is escaped so the name cannot leave the endpoint.
	segs := strings.Split(name, "/")
	for i, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return job{}, apperror.New(502, "the provider named an unusable video job", nil)
		}
		segs[i] = url.PathEscape(seg)
	}
	resp, err := s.c.Do(ctx, prov, coremedia.Request{
		Method: http.MethodGet, Path: strings.Join(segs, "/"), Style: enum.StyleGenerateContent, Log: "video.poll",
	})
	if err != nil {
		return job{}, err
	}
	var op struct {
		Done     bool            `json:"done"`
		Error    json.RawMessage `json:"error"`
		Response struct {
			GenerateVideoResponse struct {
				GeneratedSamples []struct {
					Video struct {
						URI string `json:"uri"`
					} `json:"video"`
				} `json:"generatedSamples"`
				RaiMediaFilteredReasons []string `json:"raiMediaFilteredReasons"`
			} `json:"generateVideoResponse"`
		} `json:"response"`
	}
	if err := json.Unmarshal(resp.Body, &op); err != nil {
		return job{}, apperror.New(502, "malformed upstream body", err)
	}
	if !op.Done {
		return job{status: "rendering"}, nil
	}
	if msg := errorText(op.Error); msg != "" {
		return job{failure: msg}, nil
	}
	out := op.Response.GenerateVideoResponse
	if len(out.GeneratedSamples) == 0 || out.GeneratedSamples[0].Video.URI == "" {
		reason := strings.Join(out.RaiMediaFilteredReasons, "; ")
		if reason == "" {
			reason = "no video was returned"
		}
		return job{failure: reason}, nil
	}
	return job{done: true, status: "completed", download: out.GeneratedSamples[0].Video.URI}, nil
}

// errorText reads a failure that is a string or an {"message"} object.
func errorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return e.Message
	}
	return string(raw)
}

// openAISize maps an aspect ratio and a resolution tier onto the pixel
// sizes OpenAI's video models take; "" leaves the model's default.
func openAISize(aspect, resolution string) string {
	tall := false
	if a, b, ok := strings.Cut(aspect, ":"); ok {
		w, _ := strconv.Atoi(a)
		h, _ := strconv.Atoi(b)
		tall = h > w
	} else if aspect == "" && resolution == "" {
		return ""
	}
	long, short := "1280", "720"
	if strings.HasPrefix(resolution, "1080") {
		long, short = "1792", "1024"
	}
	if tall {
		return short + "x" + long
	}
	return long + "x" + short
}

func frameExt(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	}
	return ".png"
}
