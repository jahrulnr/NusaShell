package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nusashell/application/service/attachments"
	"nusashell/application/service/generatedmedia"
	"nusashell/domain"
	"nusashell/pkg/yamlmd"
)

const (
	maxConcurrentImageGens   = 2
	maxGenerateImageN        = 4
	maxReferencedImages      = 5
	imageGenUnconfiguredHint = "No image generation model is configured. Ask the user to pick an image model in Settings → Image generation."
)

type generateImageArgs struct {
	Prompt               string   `json:"prompt"`
	Size                 string   `json:"size"`
	Quality              string   `json:"quality"`
	Background           string   `json:"background"`
	N                    int      `json:"n"`
	ReferencedImagePaths []string `json:"referenced_image_paths"`
}

func parseGenerateImageArgs(argsJSON string) (generateImageArgs, error) {
	var args generateImageArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return args, fmt.Errorf("invalid args: %w", err)
	}
	args.Prompt = strings.TrimSpace(args.Prompt)
	if args.Prompt == "" {
		return args, fmt.Errorf("prompt is required")
	}
	args.Size = strings.TrimSpace(args.Size)
	args.Quality = strings.TrimSpace(args.Quality)
	args.Background = strings.TrimSpace(args.Background)
	if err := validateImageEnum("size", args.Size, "auto", "1024x1024", "1536x1024", "1024x1536"); err != nil {
		return args, err
	}
	if err := validateImageEnum("quality", args.Quality, "auto", "low", "medium", "high"); err != nil {
		return args, err
	}
	if err := validateImageEnum("background", args.Background, "auto", "transparent", "opaque"); err != nil {
		return args, err
	}
	if args.N <= 0 {
		args.N = 1
	}
	if args.N > maxGenerateImageN {
		args.N = maxGenerateImageN
	}
	if len(args.ReferencedImagePaths) > maxReferencedImages {
		return args, fmt.Errorf("referenced_image_paths accepts at most %d paths", maxReferencedImages)
	}
	for i, p := range args.ReferencedImagePaths {
		p = strings.TrimSpace(p)
		args.ReferencedImagePaths[i] = p
		if p == "" {
			return args, fmt.Errorf("referenced_image_paths[%d] is empty", i)
		}
		if !filepath.IsAbs(p) {
			return args, fmt.Errorf("referenced_image_paths must be absolute paths, got %q", p)
		}
	}
	return args, nil
}

func validateImageEnum(field, value string, allowed ...string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, item := range allowed {
		if strings.EqualFold(value, item) {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", field, strings.Join(allowed, ", "))
}

func (a *App) executeGenerateImage(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	started := time.Now()
	if a.imageGenSem != nil {
		select {
		case a.imageGenSem <- struct{}{}:
		case <-run.Ctx.Done():
			err := run.Ctx.Err()
			return "error: " + err.Error(), nil, err
		}
	}
	defer func() {
		if a.imageGenSem != nil {
			<-a.imageGenSem
		}
	}()

	args, err := parseGenerateImageArgs(toolCall.Args)
	if err != nil {
		return failGenerateImage(err.Error())
	}
	if strings.TrimSpace(settings.ImageProviderID) == "" || strings.TrimSpace(settings.ImageModelID) == "" {
		return imageGenUnconfiguredHint, nil, fmt.Errorf("%s", imageGenUnconfiguredHint)
	}
	provider, apiKey, ok := a.resolveFallbackProvider(settings.ImageProviderID)
	if !ok {
		msg := fmt.Sprintf("Image generation provider %q was not found or is disabled. Ask the user to pick an enabled OpenAI or OpenRouter image model in Settings → Image generation.", a.providerNameByID(settings.ImageProviderID))
		return msg, nil, fmt.Errorf("%s", msg)
	}
	if a.ImageGeneratorFactory == nil {
		return failGenerateImage("Image generation is not available in this build.")
	}
	if a.Attachments == nil {
		return failGenerateImage("attachment store is not configured")
	}

	refs, err := a.loadImageReferences(args.ReferencedImagePaths)
	if err != nil {
		return failGenerateImage(err.Error())
	}

	// Validate i2i capability: if the user/agent passed reference images
	// (image-to-image / edit request), the configured image model must
	// accept image input. Most older image models are text-to-image only.
	// Sending references to a t2i-only model wastes a billed API call and
	// returns an opaque upstream error. Vision=true on an image-kind model
	// means input_modalities includes "image" (i2i capable).
	if len(refs) > 0 {
		if m := provider.FindModel(settings.ImageModelID); m != nil && !m.Vision {
			return failGenerateImage(fmt.Sprintf(
				"Model %q does not support image-to-image (editing with reference images). It only supports text-to-image. Ask the user to switch to an i2i-capable image model in Settings → Image generation, or retry without referenced_image_paths.",
				settings.ImageModelID))
		}
	}

	req := ImageGenRequest{
		Model:      settings.ImageModelID,
		Prompt:     args.Prompt,
		Size:       args.Size,
		Quality:    args.Quality,
		Background: args.Background,
		N:          args.N,
		References: refs,
		TurnID:     toolCall.ID,
	}
	result, err := a.generateImage(run, provider, apiKey, req)
	if err != nil {
		msg := formatImageGenFailure(err)
		return msg, nil, fmt.Errorf("%s", strings.TrimPrefix(msg, "error: "))
	}

	atts, paths, err := a.persistGeneratedImages(run.ConversationID, toolCall.ID, result)
	if err != nil {
		return failGenerateImage(err.Error())
	}
	elapsed := time.Since(started).Milliseconds()
	meta := map[string]any{
		"status":     "completed",
		"provider":   result.Provider,
		"model":      result.Model,
		"media_type": atts[0].MediaType,
		"file_path":  paths[0],
		"elapsed_ms": elapsed,
	}
	if size := strings.TrimSpace(args.Size); size != "" && !strings.EqualFold(size, "auto") {
		meta["size"] = size
	}
	if quality := strings.TrimSpace(args.Quality); quality != "" && !strings.EqualFold(quality, "auto") {
		meta["quality"] = quality
	}
	if result.UsageTokens > 0 {
		meta["usage_tokens"] = result.UsageTokens
	}
	if result.CostUSD > 0 {
		meta["cost_usd"] = result.CostUSD
	}
	if len(paths) > 1 {
		meta["file_paths"] = paths
	}
	body := fmt.Sprintf("Image saved to %s. To edit this image, pass its file_path in referenced_image_paths.", paths[0])
	if len(paths) > 1 {
		body = fmt.Sprintf("%d images saved (%s). They are already displayed to the user in the UI — do not re-render them as Markdown images or file links. To edit one, pass its file_path in referenced_image_paths.", len(paths), strings.Join(paths, ", "))
	}
	return yamlmd.MD(meta, body), atts, nil
}

// loadImageReferences reads referenced images directly from disk by
// absolute path. The filesystem is the single source of truth — no
// conversation-history lookup — so any readable image path works.
func (a *App) loadImageReferences(paths []string) ([]ImageReference, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]ImageReference, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read referenced image %q: %w", path, err)
		}
		media := attachments.SniffMediaType(data)
		if media == "" {
			media = attachments.SniffMediaType(data)
		}
		switch strings.ToLower(strings.TrimSpace(media)) {
		case "image/png", "image/jpeg", "image/webp":
		default:
			return nil, fmt.Errorf("referenced image %q has unsupported type %q", path, media)
		}
		out = append(out, ImageReference{MediaType: media, Data: data})
	}
	return out, nil
}

func decodeAttachmentDataURL(dataURL string) ([]byte, error) {
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return nil, fmt.Errorf("invalid data URL")
	}
	return base64.StdEncoding.DecodeString(data)
}

func (a *App) persistGeneratedImages(conversationID, toolCallID string, result *ImageGenResult) ([]domain.Attachment, []string, error) {
	if result == nil || len(result.Images) == 0 {
		return nil, nil, fmt.Errorf("image provider returned no images")
	}
	atts := make([]domain.Attachment, 0, len(result.Images))
	paths := make([]string, 0, len(result.Images))
	for i, img := range result.Images {
		if len(img.Bytes) == 0 {
			return nil, nil, fmt.Errorf("generated image %d is empty", i+1)
		}
		att, path, err := a.saveGeneratedMedia(conversationID,
			generatedImageBaseName(toolCallID, i, len(result.Images)), "image", img.Bytes, false)
		if err != nil {
			return nil, nil, err
		}
		switch att.MediaType {
		case "image/png", "image/jpeg", "image/webp":
		default:
			return nil, nil, fmt.Errorf("unsupported generated image type %s; NusaShell saves PNG, JPEG, or WebP", att.MediaType)
		}
		atts = append(atts, att)
		paths = append(paths, path)
	}
	return atts, paths, nil
}

// generatedImageBaseName builds the attachment base name for the index-th
// of total generated images ("gen-<id>" or "gen-<id>-<n>"); the extension
// comes from the sniffed media type in saveGeneratedMedia.
func generatedImageBaseName(toolCallID string, index, total int) string {
	id := generatedmedia.SanitizeFilePart(toolCallID)
	if id == "" {
		id = "image"
	}
	if total > 1 {
		return fmt.Sprintf("gen-%s-%d", id, index+1)
	}
	return "gen-" + id
}

// generateImage runs the configured image generator with the app-level
// retry loop. The Codex account failover was removed with the Codex
// provider; OpenAI/OpenRouter hosts serve image generation directly.
func (a *App) generateImage(run *TurnRun, provider *domain.Provider, apiKey string, req ImageGenRequest) (*ImageGenResult, error) {
	if a.ImageGeneratorFactory == nil {
		return nil, fmt.Errorf("Image generation is not available in this build.")
	}
	generator, err := a.ImageGeneratorFactory(provider, apiKey)
	if err != nil {
		return nil, err
	}
	var result *ImageGenResult
	for retry := 1; ; retry++ {
		result, err = generator.Generate(run.Ctx, req)
		if err == nil || retry >= maxProviderAttempts {
			break
		}
		delay, retryable := providerRetryDelay(err, retry)
		if !retryable {
			break
		}
		a.log("warn", "image", "retrying image generation (%d/%d) after %s: %v", retry, maxProviderAttempts, delay.Round(time.Millisecond), err)
		sleeper := a.retrySleeper
		if sleeper == nil {
			sleeper = sleepForRetry
		}
		if serr := sleeper(run.Ctx, delay); serr != nil {
			return nil, serr
		}
	}
	return result, err
}

func formatImageGenFailure(err error) string {
	if err == nil {
		return "error: image generation failed"
	}
	if errors.Is(err, context.Canceled) {
		return "error: image generation interrupted"
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		if upstream.StatusCode == 429 {
			reset := ""
			if upstream.RetryAfter > 0 {
				reset = fmt.Sprintf(" Rate limit resets in %s.", upstream.RetryAfter.Round(time.Second))
			}
			return "error: image generation rate-limited." + reset + " Configure a different image model in Settings, or retry later."
		}
		return "error: image generation failed: " + err.Error()
	}
	return "error: image generation failed: " + err.Error()
}

func failGenerateImage(msg string) (string, []domain.Attachment, error) {
	msg = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg), "error:"))
	if msg == "" {
		msg = "image generation failed"
	}
	return "error: " + msg, nil, fmt.Errorf("%s", msg)
}

func (a *App) hydrateAttachmentDataURLs(atts []domain.Attachment) []domain.Attachment {
	if a == nil || a.Attachments == nil || len(atts) == 0 {
		return atts
	}
	out := make([]domain.Attachment, len(atts))
	copy(out, atts)
	for i := range out {
		if out[i].DataURL != "" || out[i].FilePath == "" {
			continue
		}
		data, err := a.Attachments.ReadFile(out[i].FilePath)
		if err != nil || len(data) == 0 {
			continue
		}
		media := out[i].MediaType
		if media == "" {
			media = attachments.SniffMediaType(data)
		}
		if media == "" {
			continue
		}
		out[i].MediaType = media
		out[i].DataURL = "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return out
}

func (a *App) chatMessagesForProvider(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage {
	msgs := chatMessages(c, pendingMsgID, caps)
	if a == nil {
		return msgs
	}
	for i := range msgs {
		if len(msgs[i].Attachments) > 0 {
			msgs[i].Attachments = a.hydrateAttachmentDataURLs(msgs[i].Attachments)
		}
		if msgs[i].ToolResult != nil && len(msgs[i].ToolResult.Attachments) > 0 {
			msgs[i].ToolResult.Attachments = a.hydrateAttachmentDataURLs(msgs[i].ToolResult.Attachments)
		}
	}
	return msgs
}
