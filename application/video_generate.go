package application

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/domain"
)

const (
	maxGeneratedVideoBytes = 100 << 20 // mirrors videogen download cap
	maxVideoPromptChars    = 4000
	videoUnconfiguredHint  = "No video generation model is configured. Ask the user to pick one in Settings → Video generation."
)

var allowedVideoResolutions = []string{"", "480p", "720p", "768p", "1080p", "1k", "2k", "4k"}

// executeGenerateVideo handles the generate_video tool call. Video
// generation is async upstream (submit/poll/download) and can take tens of
// seconds to minutes — blocking here is expected, cancellation is honored
// through run.Ctx, and there is deliberately no wall-clock timeout.
func (a *App) executeGenerateVideo(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	var args struct {
		Prompt      string `json:"prompt"`
		DurationSec int    `json:"duration_seconds"`
		Resolution  string `json:"resolution"`
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

	if strings.TrimSpace(settings.VideoProviderID) == "" || strings.TrimSpace(settings.VideoModelID) == "" {
		return videoUnconfiguredHint, nil, fmt.Errorf("%s", videoUnconfiguredHint)
	}
	provider, apiKey, ok := a.resolveFallbackProvider(settings.VideoProviderID)
	if !ok {
		msg := fmt.Sprintf("Video generation provider %q was not found or is disabled.", settings.VideoProviderID)
		return msg, nil, fmt.Errorf("%s", msg)
	}
	if a.VideoGeneratorFactory == nil {
		return failGenerateVideo("Video generation is not available in this build.")
	}
	generator, err := a.VideoGeneratorFactory(provider, apiKey)
	if err != nil {
		return failGenerateVideo(err.Error())
	}

	result, err := generator.Generate(run.Ctx, VideoGenRequest{
		Model: settings.VideoModelID, Prompt: prompt,
		DurationSec: args.DurationSec, Resolution: res,
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
	if len(result.Video) > maxGeneratedVideoBytes {
		return failGenerateVideo(fmt.Sprintf("generated video exceeds %d bytes", maxGeneratedVideoBytes))
	}
	if a.Attachments == nil {
		return failGenerateVideo("attachment store is not configured")
	}
	name := fmt.Sprintf("gen-%s.mp4", sanitizeFilePart(toolCall.ID))
	path, err := a.Attachments.WriteBytes(run.ConversationID, name, result.Video)
	if err != nil {
		return failGenerateVideo(err.Error())
	}
	att := domain.Attachment{
		Type: "video", Name: name, MediaType: result.MediaType,
		DataURL: fmt.Sprintf("data:%s;base64,%s", result.MediaType, base64.StdEncoding.EncodeToString(result.Video)), FilePath: path,
	}
	meta := map[string]any{
		"status": "completed", "provider": result.Provider, "model": result.Model,
		"media_type": result.MediaType, "file_path": path,
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
	body := fmt.Sprintf("Video saved to %s. The video attachment is already delivered to the user in the UI — do not re-render it as a Markdown link.", path)
	return yamlMDApp(meta, body), []domain.Attachment{att}, nil
}

func failGenerateVideo(msg string) (string, []domain.Attachment, error) {
	return "error: " + msg, nil, fmt.Errorf("%s", msg)
}
