package media

import (
	"context"
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
	clock "nusashell/pkg/time"
	"nusashell/pkg/yamlmd"
)

const (
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

// ExecuteGenerateImage handles the generate_image tool call.
func (s *Service) ExecuteGenerateImage(call Call, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	started := clock.NewTime().Time()
	if s.imageGenSem != nil {
		select {
		case s.imageGenSem <- struct{}{}:
		case <-call.ctx().Done():
			err := call.ctx().Err()
			return "error: " + err.Error(), nil, err
		}
		defer func() { <-s.imageGenSem }()
	}

	args, err := parseGenerateImageArgs(toolCall.Args)
	if err != nil {
		return failGenerateImage(err.Error())
	}
	if strings.TrimSpace(settings.ImageProviderID) == "" || strings.TrimSpace(settings.ImageModelID) == "" {
		return imageGenUnconfiguredHint, nil, fmt.Errorf("%s", imageGenUnconfiguredHint)
	}
	provider, apiKey, ok := s.resolve(settings.ImageProviderID)
	if !ok {
		msg := fmt.Sprintf("Image generation provider %q was not found or is disabled. Ask the user to pick an enabled OpenAI or OpenRouter image model in Settings → Image generation.", s.name(settings.ImageProviderID))
		return msg, nil, fmt.Errorf("%s", msg)
	}
	// Codex multi-account routing: pick the sticky account token before
	// building the generator so rate-limit/circuit state from the router
	// (shared with chat turns) applies to image generation too.
	if s.prepareCodex != nil && provider != nil && provider.Kind == domain.ProviderCodex {
		prepared, prepErr := s.prepareCodex(call.ConversationID, provider, apiKey)
		if prepErr != nil {
			return failGenerateImage(prepErr.Error())
		}
		if prepared != "" {
			apiKey = prepared
		}
	}
	if s.imageGen == nil {
		return failGenerateImage("Image generation is not available in this build.")
	}
	if s.attachments == nil {
		return failGenerateImage("attachment store is not configured")
	}

	refs, err := loadImageReferences(args.ReferencedImagePaths)
	if err != nil {
		return failGenerateImage(err.Error())
	}

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
		TurnID:     call.TurnID,
	}
	if req.TurnID == "" {
		// Fall back to the tool-call id when no run/turn id is plumbed.
		req.TurnID = toolCall.ID
	}
	result, err := s.generateImage(call, provider, apiKey, req)
	if err != nil {
		msg := FormatImageGenFailure(err, provider.Kind)
		return msg, nil, fmt.Errorf("%s", strings.TrimPrefix(msg, "error: "))
	}

	atts, paths, err := s.PersistGeneratedImages(call.ConversationID, toolCall.ID, result)
	if err != nil {
		return failGenerateImage(err.Error())
	}
	elapsed := clock.NewTime().Since(started).Milliseconds()
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

func loadImageReferences(paths []string) ([]ImageReference, error) {
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

// PersistGeneratedImages writes generated image bytes through generatedmedia.Save.
func (s *Service) PersistGeneratedImages(conversationID, toolCallID string, result *ImageGenResult) ([]domain.Attachment, []string, error) {
	if result == nil || len(result.Images) == 0 {
		return nil, nil, fmt.Errorf("image provider returned no images")
	}
	atts := make([]domain.Attachment, 0, len(result.Images))
	paths := make([]string, 0, len(result.Images))
	for i, img := range result.Images {
		if len(img.Bytes) == 0 {
			return nil, nil, fmt.Errorf("generated image %d is empty", i+1)
		}
		att, path, err := s.SaveGenerated(conversationID,
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

func (s *Service) generateImage(call Call, provider *domain.Provider, apiKey string, req ImageGenRequest) (*ImageGenResult, error) {
	if s.imageGen == nil {
		return nil, fmt.Errorf("Image generation is not available in this build.")
	}
	generator, err := s.imageGen(call.ctx(), provider, apiKey)
	if err != nil {
		return nil, err
	}
	maxAttempts := domain.MaxProviderAttempts
	var result *ImageGenResult
	for retry := 1; ; retry++ {
		result, err = generator.Generate(call.ctx(), req)
		if err == nil || retry >= maxAttempts || s.retryDelay == nil {
			break
		}
		// Codex multi-account failover: on usage-limit 429, plain 429, or
		// 403 entitlement failures, switch to another account before falling
		// back to the shared backoff policy. One request per account is
		// made — the router blocks the failed account, so the next pick
		// advances (no blind billable retry on the same account).
		if s.failoverCodex != nil && provider != nil && provider.Kind == domain.ProviderCodex {
			newKey, retryGen, replaced := s.failoverCodex(call.ctx(), call.ConversationID, provider, apiKey, err)
			if replaced != nil {
				return nil, replaced
			}
			if retryGen {
				if newKey == "" || newKey == apiKey {
					break
				}
				apiKey = newKey
				generator, err = s.imageGen(call.ctx(), provider, apiKey)
				if err != nil {
					return nil, err
				}
				continue
			}
		}
		delay, retryable := s.retryDelay(err, retry)
		if !retryable {
			break
		}
		s.write("warn", "image", "retrying image generation (%d/%d) after %s: %v", retry, maxAttempts, delay.Round(time.Millisecond), err)
		if s.waitRetry != nil {
			if serr := s.waitRetry(call.ctx(), delay); serr != nil {
				return nil, serr
			}
		}
	}
	return result, err
}

// FormatImageGenFailure maps generator errors to the tool-output string.
// kind identifies the provider kind so provider-specific guidance (e.g.
// Codex account entitlement) can be added without misleading other backends.
func FormatImageGenFailure(err error, kind domain.ProviderKind) string {
	if err == nil {
		return "error: image generation failed"
	}
	if errors.Is(err, context.Canceled) {
		return "error: image generation interrupted"
	}
	var upstream *domain.ProviderError
	if errors.As(err, &upstream) {
		if upstream.StatusCode == 403 && kind == domain.ProviderCodex {
			return "error: the Codex image backend rejected this account (HTTP 403 Forbidden). This usually means the account has no image generation access (e.g. ChatGPT Free). Pick an account with image support in Settings → Providers → Codex, or retry with another account."
		}
		if upstream.StatusCode == 429 {
			// Image quota exhaustion (Codex: 429 usage_limit_reached with
			// limit_id=image_gen) is a hard stop, not a retryable blip.
			if strings.Contains(strings.ToLower(upstream.Error()), "usage limit") {
				return "error: " + strings.TrimSpace(strings.TrimPrefix(upstream.Error(), "error:")) + " Configure a different image model in Settings → Image generation, or retry later."
			}
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
