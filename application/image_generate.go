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
	"unicode"

	"nusashell/domain"
)

const (
	maxConcurrentImageGens   = 2
	maxGeneratedImageBytes   = 8 * 1024 * 1024
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
	if err := a.acquireImageGen(run.Ctx); err != nil {
		return "error: " + err.Error(), nil, err
	}
	defer a.releaseImageGen()

	args, err := parseGenerateImageArgs(toolCall.Args)
	if err != nil {
		return failGenerateImage(err.Error())
	}
	if strings.TrimSpace(settings.ImageProviderID) == "" || strings.TrimSpace(settings.ImageModelID) == "" {
		return imageGenUnconfiguredHint, nil, fmt.Errorf("%s", imageGenUnconfiguredHint)
	}
	provider, apiKey, ok := a.resolveFallbackProvider(settings.ImageProviderID)
	if !ok {
		msg := fmt.Sprintf("Image generation provider %q was not found or is disabled. Ask the user to pick an enabled OpenAI, OpenRouter, or Codex image model in Settings → Image generation.", settings.ImageProviderID)
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
	result, err := a.generateImageWithCodexFailover(run, provider, apiKey, req)
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
	body := fmt.Sprintf("Image saved to %s. It is already displayed to the user in the UI — do not re-render it as a Markdown image or file link. To edit this image, pass its file_path in referenced_image_paths.", paths[0])
	if len(paths) > 1 {
		body = fmt.Sprintf("%d images saved (%s). They are already displayed to the user in the UI — do not re-render them as Markdown images or file links. To edit one, pass its file_path in referenced_image_paths.", len(paths), strings.Join(paths, ", "))
	}
	return yamlMDApp(meta, body), atts, nil
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
		media := sniffMediaType(data)
		if media == "" {
			media = sniffMediaType(data)
		}
		if !allowedGeneratedMediaType(media) {
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
		if len(img.Bytes) > maxGeneratedImageBytes {
			return nil, nil, fmt.Errorf("generated image is larger than 8 MiB (%d bytes) and was not saved", len(img.Bytes))
		}
		media := strings.TrimSpace(img.MediaType)
		if sniffed := sniffMediaType(img.Bytes); sniffed != "" {
			if media == "" || media == "application/octet-stream" {
				media = sniffed
			} else if sniffed != media {
				return nil, nil, fmt.Errorf("generated image media type %q does not match file signature %q", media, sniffed)
			}
		}
		if !allowedGeneratedMediaType(media) {
			if media == "" {
				media = "unknown"
			}
			return nil, nil, fmt.Errorf("unsupported generated image type %s; NusaShell saves PNG, JPEG, or WebP", media)
		}
		name := generatedImageName(toolCallID, i, len(result.Images), extForGeneratedMedia(media))
		path, err := a.Attachments.WriteBytes(conversationID, name, img.Bytes)
		if err != nil {
			return nil, nil, err
		}
		atts = append(atts, domain.Attachment{
			Type:      "image",
			Name:      name,
			MediaType: media,
			FilePath:  path,
		})
		paths = append(paths, path)
	}
	return atts, paths, nil
}

func allowedGeneratedMediaType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

func extForGeneratedMedia(mediaType string) string {
	switch strings.ToLower(mediaType) {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func generatedImageName(toolCallID string, index, total int, ext string) string {
	id := sanitizeFilePart(toolCallID)
	if id == "" {
		id = "image"
	}
	if total > 1 {
		return fmt.Sprintf("gen-%s-%d%s", id, index+1, ext)
	}
	return fmt.Sprintf("gen-%s%s", id, ext)
}

func sanitizeFilePart(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (a *App) generateImageWithCodexFailover(run *TurnRun, provider *domain.Provider, apiKey string, req ImageGenRequest) (*ImageGenResult, error) {
	accountID := peekCodexAccountID(apiKey)
	if provider != nil && provider.Kind == domain.ProviderCodex && a.CodexRouter != nil && a.Credentials != nil {
		accounts := a.listCodexAccountIDs(provider.ID)
		if len(accounts) > 0 {
			pick := a.CodexRouter.PickAccountDetailed(run.ConversationID, provider.ID, accounts)
			if pick.AccountID == "" {
				if pick.AllRateLimited {
					return nil, allCodexAccountsLimitedError(pick.EarliestReset)
				}
			} else if token, has, _ := a.Credentials.Get(accountKey(provider.ID, pick.AccountID)); has {
				apiKey = token
				accountID = pick.AccountID
			}
		}
	}
	tried := map[string]bool{}
	for {
		if a.ImageGeneratorFactory == nil {
			return nil, fmt.Errorf("Image generation is not available in this build.")
		}
		generator, err := a.ImageGeneratorFactory(provider, apiKey)
		if err != nil {
			return nil, err
		}
		result, err := a.generateImageWithRetry(run.Ctx, generator, req)
		if err == nil {
			return result, nil
		}
		if provider == nil || provider.Kind != domain.ProviderCodex || a.CodexRouter == nil || !isRateLimitError(err) {
			return result, err
		}
		key := accountID
		if key == "" {
			key = peekCodexAccountID(apiKey)
		}
		if key != "" {
			tried[key] = true
			cooldown := rateLimitCooldown(err)
			if cooldown > retryAfterCutoff {
				a.CodexRouter.MarkCircuitOpen(key, time.Now().Add(cooldown))
				a.log("warn", "image", "codex image circuit open: account %s usage exhausted for %s", key, cooldown.Round(time.Minute))
			} else {
				a.CodexRouter.MarkRateLimited(key, cooldown)
				a.log("warn", "image", "codex image rate-limited: account %s cooling down for %s", key, cooldown.Round(time.Second))
			}
		}
		accounts := a.listCodexAccountIDs(provider.ID)
		pick := a.CodexRouter.PickAccountDetailed(run.ConversationID, provider.ID, accounts)
		if pick.AccountID == "" || tried[pick.AccountID] {
			if pick.AllRateLimited && len(tried) > 0 {
				return result, allCodexAccountsLimitedError(pick.EarliestReset)
			}
			return result, err
		}
		token, has, _ := a.Credentials.Get(accountKey(provider.ID, pick.AccountID))
		if !has {
			return result, err
		}
		apiKey = token
		accountID = pick.AccountID
	}
}

func (a *App) generateImageWithRetry(ctx context.Context, generator ImageGenerator, req ImageGenRequest) (*ImageGenResult, error) {
	for retry := 1; ; retry++ {
		result, err := generator.Generate(ctx, req)
		if err == nil || retry >= maxProviderAttempts {
			return result, err
		}
		delay, retryable := providerRetryDelay(err, retry)
		if !retryable {
			return result, err
		}
		a.log("warn", "image", "retrying image generation (%d/%d) after %s: %v", retry, maxProviderAttempts, delay.Round(time.Millisecond), err)
		sleeper := a.retrySleeper
		if sleeper == nil {
			sleeper = sleepForRetry
		}
		if err := sleeper(ctx, delay); err != nil {
			return nil, err
		}
	}
}

func formatImageGenFailure(err error) string {
	if err == nil {
		return "error: image generation failed"
	}
	if errors.Is(err, context.Canceled) {
		return "error: image generation interrupted"
	}
	if msg := err.Error(); strings.Contains(msg, "all Codex accounts are rate-limited") {
		return "error: " + msg
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

func (a *App) acquireImageGen(ctx context.Context) error {
	if a == nil || a.imageGenSem == nil {
		return nil
	}
	select {
	case a.imageGenSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) releaseImageGen() {
	if a == nil || a.imageGenSem == nil {
		return
	}
	<-a.imageGenSem
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
			media = sniffMediaType(data)
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
