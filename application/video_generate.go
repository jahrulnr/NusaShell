package application

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/application/internal/service/generatedmedia"
	"nusashell/application/internal/service/yamlmd"
	"nusashell/domain"
)

const (
	maxVideoPromptChars   = 4000
	videoUnconfiguredHint = "No video generation model is configured. Ask the user to pick one in Settings → Video generation."
)

var allowedVideoResolutions = []string{"", "480p", "720p", "768p", "1080p", "1k", "2k", "4k"}

// executeGenerateVideo handles the generate_video tool call. Video
// generation is async upstream (submit/poll/download) and can take tens of
// seconds to minutes — blocking here is expected, cancellation is honored
// through run.Ctx, and there is deliberately no wall-clock timeout.
func (a *App) executeGenerateVideo(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	var args struct {
		Prompt               string   `json:"prompt"`
		DurationSec          int      `json:"duration_seconds"`
		Resolution           string   `json:"resolution"`
		ReferencedImagePaths []string `json:"referenced_image_paths"`
	}
	if err := json.Unmarshal([]byte(toolCall.Args), &args); err != nil {
		return "error: invalid arguments", nil, fmt.Errorf("invalid args: %w", err)
	}
	prompt := strings.TrimSpace(args.Prompt)
	if prompt == "" {
		return "error: prompt is required", nil, fmt.Errorf("prompt is required")
	}
	if len(prompt) > maxVideoPromptChars {
		return "error: prompt too long", nil, fmt.Errorf("prompt exceeds %d characters", maxVideoPromptChars)
	}
	res := strings.ToLower(strings.TrimSpace(args.Resolution))
	validRes := false
	for _, r := range allowedVideoResolutions {
		if res == r {
			validRes = true
			break
		}
	}
	if !validRes {
		return "error: resolution must be one of: 480p, 720p, 768p, 1080p, 1K, 2K, 4K", nil, fmt.Errorf("invalid resolution %q", args.Resolution)
	}
	if args.DurationSec < 0 {
		return "error: duration_seconds must be positive", nil, fmt.Errorf("negative duration")
	}
	if len(args.ReferencedImagePaths) > maxReferencedImages {
		return failGenerateVideo(fmt.Sprintf("referenced_image_paths accepts at most %d paths", maxReferencedImages))
	}
	for i, p := range args.ReferencedImagePaths {
		if strings.TrimSpace(p) == "" {
			return failGenerateVideo(fmt.Sprintf("referenced_image_paths[%d] is empty", i))
		}
		if !filepath.IsAbs(p) {
			return failGenerateVideo(fmt.Sprintf("referenced_image_paths must be absolute paths, got %q", p))
		}
	}

	if strings.TrimSpace(settings.VideoGenProviderID) == "" || strings.TrimSpace(settings.VideoGenModelID) == "" {
		return videoUnconfiguredHint, nil, fmt.Errorf("%s", videoUnconfiguredHint)
	}
	provider, apiKey, ok := a.resolveFallbackProvider(settings.VideoGenProviderID)
	if !ok {
		msg := fmt.Sprintf("Video generation provider %q was not found or is disabled.", a.providerNameByID(settings.VideoGenProviderID))
		return msg, nil, fmt.Errorf("%s", msg)
	}
	if a.VideoGeneratorFactory == nil {
		return failGenerateVideo("Video generation is not available in this build.")
	}
	generator, err := a.VideoGeneratorFactory(provider, apiKey)
	if err != nil {
		return failGenerateVideo(err.Error())
	}

	// Load reference images for image-to-video (i2v). Best-effort: if
	// the model doesn't support i2v, upstream will reject and the error
	// is surfaced verbatim. The model picker badges i2i-capable models
	// via Vision=true so the user/agent can check before calling.
	refs, err := a.loadImageReferences(args.ReferencedImagePaths)
	if err != nil {
		return failGenerateVideo(err.Error())
	}

	result, err := generator.Generate(run.Ctx, VideoGenRequest{
		Model: settings.VideoGenModelID, Prompt: prompt,
		DurationSec: args.DurationSec, Resolution: res,
		References: refs,
	})
	if err != nil {
		msg := err.Error()
		// Upstream duration/resolution validation errors are actionable —
		// surface them verbatim so the agent can retry within limits.
		return "error: " + msg, nil, fmt.Errorf("%s", msg)
	}
	if result == nil || len(result.Video) == 0 {
		return failGenerateVideo("video provider returned no video")
	}
	att, path, err := a.saveGeneratedMedia(run.ConversationID, "gen-"+generatedmedia.SanitizeFilePart(toolCall.ID), "video", result.Video, true)
	if err != nil {
		return failGenerateVideo(err.Error())
	}
	meta := map[string]any{
		"status": "completed", "provider": result.Provider, "model": result.Model,
		"media_type": att.MediaType, "file_path": path,
	}
	if result.JobID != "" {
		meta["job_id"] = result.JobID
	}
	if args.DurationSec > 0 {
		meta["duration_seconds"] = args.DurationSec
	}
	if res != "" {
		meta["resolution"] = res
	}
	if result.CostUSD > 0 {
		meta["cost_usd"] = result.CostUSD
	}
	if len(refs) > 0 {
		meta["reference_count"] = len(refs)
	}
	body := fmt.Sprintf("Video saved to %s.", path)
	if len(refs) > 0 {
		body = fmt.Sprintf("Video saved to %s (generated from %d reference image(s)).", path, len(refs))
	}
	return yamlmd.YAMLMD(meta, body), []domain.Attachment{att}, nil
}

func failGenerateVideo(msg string) (string, []domain.Attachment, error) {
	return "error: " + msg, nil, fmt.Errorf("%s", msg)
}
