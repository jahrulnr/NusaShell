package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/infrastructure/ai/core"
)

func (p *Provider) buildRequest(req *core.Request, stream bool) (*chatRequest, error) {
	out := &chatRequest{
		Model:            req.Model,
		Stream:           stream,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		TopK:             req.TopK,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Stop:             append([]string(nil), req.Stop...),
		ToolChoice:       req.ToolChoice,
	}
	if stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if err := req.Thinking.Validate(); err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	// Reasoning models get max_completion_tokens (the gpt-5+ wire field);
	// all other models keep max_tokens. ReasoningEffort is set for EVERY
	// model: gateways (OpenRouter, DeepSeek, GLM) accept reasoning_effort
	// on non-gpt-5 models, so the upstream "thinking only for reasoning
	// chat models" gate is intentionally not ported.
	if p.isReasoningModel(req.Model) {
		out.MaxCompletionTokens = req.MaxTokens
	} else {
		out.MaxTokens = req.MaxTokens
	}
	if req.Thinking != nil && req.Thinking.Mode == core.ThinkingDisabled {
		out.ReasoningEffort = "none"
	}
	if req.Thinking != nil && req.Thinking.Mode == core.ThinkingEnabled {
		effort := reasoningEffort(req.Thinking)
		if effort == "" {
			effort = "medium"
		}
		if !supportsOpenAIReasoningEffort(effort) {
			return nil, fmt.Errorf("openai: unsupported reasoning_effort %q; use %s", effort, strings.Join(openAIReasoningEfforts(), ", "))
		}
		out.ReasoningEffort = effort
	}
	if req.ResponseFormat != nil {
		converted, err := convertResponseFormat(req.ResponseFormat)
		if err != nil {
			return nil, err
		}
		out.ResponseFormat = converted
	}
	if len(req.ProviderOptions) > 0 {
		if err := applyProviderOptions(out, req.ProviderOptions); err != nil {
			return nil, err
		}
	}
	if len(req.Tools) > 0 {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return nil, err
		}
		out.Tools = tools
	}
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return nil, err
	}
	out.Messages = messages
	return out, nil
}

func (p *Provider) isReasoningModel(model string) bool {
	model = openAIModelName(model)
	if strings.Contains(model, "chat") {
		return false
	}
	var major int
	_, err := fmt.Sscanf(model, "gpt-%d", &major)
	return err == nil && major >= 5
}

func reasoningEffort(thinking *core.Thinking) string {
	if thinking == nil {
		return ""
	}
	return thinking.Effort
}

func openAIModelName(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if _, after, ok := strings.Cut(model, "/"); ok {
		return after
	}
	return model
}

func openAIReasoningEfforts() []string {
	return []string{"low", "medium", "high", "xhigh", "max"}
}

func supportsOpenAIReasoningEffort(effort string) bool {
	for _, supported := range openAIReasoningEfforts() {
		if effort == supported {
			return true
		}
	}
	return false
}

func supportsOpenAIReasoningEffortValue(effort string) bool {
	if effort == "none" {
		return true
	}
	return supportsOpenAIReasoningEffort(effort)
}

func convertMessages(messages []core.Message) ([]chatMessage, error) {
	out := make([]chatMessage, 0, len(messages))
	var deferredMedia []contentPart
	flushDeferredMedia := func() {
		if len(deferredMedia) == 0 {
			return
		}
		out = append(out, chatMessage{Role: "user", Content: deferredMedia})
		deferredMedia = nil
	}
	for i, msg := range messages {
		converted := chatMessage{Role: string(msg.Role)}
		switch msg.Role {
		case core.RoleSystem, core.RoleUser, core.RoleAssistant:
			// Media is reinjected as a user message, but it must wait until
			// every tool result for the preceding assistant tool_calls message
			// is emitted. Otherwise providers such as DeepSeek see a user
			// message between tool results and reject the request as incomplete.
			flushDeferredMedia()
			content, toolCalls, reasoningContent, err := convertMessageBlocks(msg.Blocks)
			if err != nil {
				return nil, fmt.Errorf("openai: messages[%d]: %w", i, err)
			}
			converted.Content = content
			converted.ToolCalls = toolCalls
			converted.ReasoningContent = reasoningContent
		case core.RoleTool:
			toolMessages, media, err := convertToolMessage(msg.Blocks)
			if err != nil {
				return nil, fmt.Errorf("openai: messages[%d]: %w", i, err)
			}
			out = append(out, toolMessages...)
			if len(media) > 0 {
				deferredMedia = append(deferredMedia, media...)
			}
			continue
		default:
			return nil, fmt.Errorf("openai: unsupported role %q", msg.Role)
		}
		out = append(out, converted)
	}
	flushDeferredMedia()
	return out, nil
}

func convertMessageBlocks(blocks []core.Block) (any, []toolCall, string, error) {
	parts := make([]contentPart, 0, len(blocks))
	var text strings.Builder
	var toolCalls []toolCall
	var reasoning strings.Builder
	for _, block := range blocks {
		switch b := block.(type) {
		case core.TextBlock:
			if b.Text == "" {
				if b.Cache != nil {
					return nil, nil, "", fmt.Errorf("openai: cache breakpoint requires a non-empty text block")
				}
				continue
			}
			breakpoint, err := convertPromptCacheBreakpoint(b.Cache)
			if err != nil {
				return nil, nil, "", err
			}
			parts = append(parts, contentPart{Type: "text", Text: b.Text, PromptCacheBreakpoint: breakpoint})
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(b.Text)
		case core.ImageBlock:
			url, err := imageURLValue(b)
			if err != nil {
				return nil, nil, "", err
			}
			breakpoint, err := convertPromptCacheBreakpoint(b.Cache)
			if err != nil {
				return nil, nil, "", err
			}
			parts = append(parts, contentPart{
				Type: "image_url",
				ImageURL: &imageURL{
					URL:    url,
					Detail: b.Detail,
				},
				PromptCacheBreakpoint: breakpoint,
			})
		case core.AudioBlock:
			breakpoint, err := convertPromptCacheBreakpoint(b.Cache)
			if err != nil {
				return nil, nil, "", err
			}
			parts = append(parts, contentPart{
				Type: "input_audio",
				InputAudio: &inputAudio{
					Data:   base64.StdEncoding.EncodeToString(b.Data),
					Format: audioFormat(b),
				},
				PromptCacheBreakpoint: breakpoint,
			})
		case core.VideoBlock:
			if b.URL == "" {
				return nil, nil, "", fmt.Errorf("openai: video blocks require a URL or data URL")
			}
			breakpoint, err := convertPromptCacheBreakpoint(b.Cache)
			if err != nil {
				return nil, nil, "", err
			}
			parts = append(parts, contentPart{
				Type:                  "video_url",
				VideoURL:              &videoURL{URL: b.URL},
				PromptCacheBreakpoint: breakpoint,
			})
		case core.ToolUseBlock:
			if b.Cache != nil {
				return nil, nil, "", fmt.Errorf("OpenAI Chat does not support cache breakpoints on tool use blocks")
			}
			toolCalls = append(toolCalls, toolCall{
				ID:   b.ID,
				Type: "function",
				Function: toolCallFunc{
					Name:      b.Name,
					Arguments: string(b.Arguments),
				},
			})
		case core.ReasoningBlock:
			if b.Cache != nil {
				return nil, nil, "", fmt.Errorf("OpenAI Chat does not support cache breakpoints on reasoning blocks")
			}
			if b.Signature != "" || len(b.Redacted) > 0 || len(b.Extra) > 0 {
				return nil, nil, "", fmt.Errorf("OpenAI Chat does not accept signed, redacted, or provider-extra reasoning blocks in message history")
			}
			if b.Text == "" {
				return nil, nil, "", fmt.Errorf("OpenAI Chat reasoning block has empty text — reasoning was received from the provider but is missing on replay (input != output)")
			}
			if reasoning.Len() > 0 {
				reasoning.WriteString("\n")
			}
			reasoning.WriteString(b.Text)
		default:
			return nil, nil, "", fmt.Errorf("unsupported block %T", block)
		}
	}
	reasoningText := reasoning.String()
	if len(parts) == 0 {
		return nil, toolCalls, reasoningText, nil
	}
	if len(parts) == 1 && parts[0].Type == "text" && parts[0].PromptCacheBreakpoint == nil {
		return text.String(), toolCalls, reasoningText, nil
	}
	return parts, toolCalls, reasoningText, nil
}

func convertToolMessage(blocks []core.Block) ([]chatMessage, []contentPart, error) {
	out := make([]chatMessage, 0, len(blocks))
	var media []contentPart
	for _, block := range blocks {
		result, ok := block.(core.ToolResultBlock)
		if !ok {
			return nil, nil, fmt.Errorf("tool role only supports ToolResultBlock, got %T", block)
		}
		text, resultMedia, err := toolResultTextAndMedia(result.Content)
		if err != nil {
			return nil, nil, err
		}
		var content any = text
		if result.Cache != nil {
			breakpoint, err := convertPromptCacheBreakpoint(result.Cache)
			if err != nil {
				return nil, nil, err
			}
			content = []contentPart{{Type: "text", Text: text, PromptCacheBreakpoint: breakpoint}}
		}
		out = append(out, chatMessage{
			Role:       string(core.RoleTool),
			ToolCallID: result.ToolUseID,
			Content:    content,
		})
		// Chat Completions tool results only carry text. Non-text blocks
		// (image/audio/video from read_media) are reinjected as a follow-up
		// user message so the vision-capable model still sees the media in
		// the next round.
		if len(resultMedia) > 0 {
			media = append(media, resultMedia...)
		}
	}
	return out, media, nil
}

func convertPromptCacheBreakpoint(cache *core.CacheControl) (*promptCacheBreakpoint, error) {
	if cache == nil {
		return nil, nil
	}
	if cache.Type != "" && cache.Type != core.CacheTypeEphemeral {
		return nil, fmt.Errorf("openai: cache breakpoint type must be %q", core.CacheTypeEphemeral)
	}
	if cache.TTL != "" {
		return nil, fmt.Errorf("openai: cache breakpoint TTL must be set with prompt_cache_options.ttl")
	}
	return &promptCacheBreakpoint{Mode: "explicit"}, nil
}

// toolResultTextAndMedia splits a tool result's content blocks into the text
// portion (which Chat Completions tool results can carry as a string) and the
// non-text media blocks (image/audio/video), serialized as contentPart parts
// for the caller to reinject as a follow-up user message. Mirrors the compat
// provider's textAndMedia pattern.
func toolResultTextAndMedia(blocks []core.Block) (string, []contentPart, error) {
	var text strings.Builder
	var media []contentPart
	for _, block := range blocks {
		switch b := block.(type) {
		case core.TextBlock:
			if b.Cache != nil {
				return "", nil, fmt.Errorf("OpenAI Chat tool result content cache must be set on ToolResultBlock")
			}
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(b.Text)
		case core.ImageBlock:
			url, err := imageURLValue(b)
			if err != nil {
				return "", nil, err
			}
			media = append(media, contentPart{Type: "image_url", ImageURL: &imageURL{URL: url, Detail: b.Detail}})
		case core.AudioBlock:
			media = append(media, contentPart{Type: "input_audio", InputAudio: &inputAudio{Data: base64.StdEncoding.EncodeToString(b.Data), Format: audioFormat(b)}})
		case core.VideoBlock:
			if b.URL == "" {
				return "", nil, fmt.Errorf("OpenAI Chat video blocks require a URL or data URL")
			}
			media = append(media, contentPart{Type: "video_url", VideoURL: &videoURL{URL: b.URL}})
		default:
			return "", nil, fmt.Errorf("OpenAI Chat tool results unsupported content block %T", block)
		}
	}
	return text.String(), media, nil
}

// audioFormat normalizes an audio block's media type to the input_audio
// format name (mp3, wav, ogg, webm, flac). Unknown or empty media types
// default to mp3 (the most common TTS output).
func audioFormat(block core.AudioBlock) string {
	switch strings.ToLower(strings.TrimSpace(block.MIME)) {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "wav"
	case "audio/ogg", "audio/opus":
		return "ogg"
	case "audio/webm":
		return "webm"
	case "audio/flac", "audio/x-flac":
		return "flac"
	default:
		return "mp3"
	}
}

func imageURLValue(block core.ImageBlock) (string, error) {
	switch {
	case block.URL != "":
		return block.URL, nil
	case len(block.Data) > 0:
		if block.MIME == "" {
			return "", fmt.Errorf("inline image MIME is required")
		}
		return "data:" + block.MIME + ";base64," + base64.StdEncoding.EncodeToString(block.Data), nil
	case block.FileURI != "":
		return block.FileURI, nil
	default:
		return "", fmt.Errorf("image requires URL, data, or file URI")
	}
}

func convertTools(tools []core.Tool) ([]tool, error) {
	out := make([]tool, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" {
			return nil, fmt.Errorf("openai: tool name is required")
		}
		var params any = map[string]any{"type": "object"}
		if len(t.Parameters) > 0 {
			var decoded any
			if err := json.Unmarshal(t.Parameters, &decoded); err != nil {
				return nil, fmt.Errorf("openai: tool %q parameters must be valid JSON: %w", t.Name, err)
			}
			params = decoded
		}
		var strict *bool
		if t.Strict == core.StrictEnabled {
			normalised, err := normalizeStrictSchema(params)
			if err != nil {
				return nil, fmt.Errorf("openai: tool %q strict schema invalid: %w", t.Name, err)
			}
			params = normalised
			strict = core.Bool(true)
		} else if t.Strict == core.StrictDisabled {
			strict = core.Bool(false)
		}
		out = append(out, tool{
			Type: "function",
			Function: &toolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
				Strict:      strict,
			},
		})
	}
	return out, nil
}

func convertResponseFormat(format *core.ResponseFormat) (*responseFormat, error) {
	out := &responseFormat{Type: string(format.Type)}
	if format.Type != core.ResponseFormatJSONSchema {
		return out, nil
	}
	if format.JSONSchema == nil {
		return nil, fmt.Errorf("openai: json schema response format requires schema")
	}
	var schema any
	if len(format.JSONSchema.Schema) > 0 {
		if err := json.Unmarshal(format.JSONSchema.Schema, &schema); err != nil {
			return nil, fmt.Errorf("openai: response schema must be valid JSON: %w", err)
		}
	}
	var strict *bool
	switch format.JSONSchema.Strict {
	case core.StrictEnabled:
		normalised, err := normalizeStrictSchema(schema)
		if err != nil {
			return nil, fmt.Errorf("openai: response strict schema invalid: %w", err)
		}
		schema = normalised
		strict = core.Bool(true)
	case core.StrictDisabled:
		strict = core.Bool(false)
	}
	out.JSONSchema = &jsonSchema{
		Name:        format.JSONSchema.Name,
		Description: format.JSONSchema.Description,
		Schema:      schema,
		Strict:      strict,
	}
	return out, nil
}
