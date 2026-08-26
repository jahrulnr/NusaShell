// Package chatcompletion implements the application.AIProvider port for
// any OpenAI-compatible /chat/completions endpoint.
package chatcompletion

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	aiutil "nusashell/infrastructure/ai/internal"
)

// Adapter talks to any OpenAI-compatible /chat/completions endpoint.
type Adapter struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func (o *Adapter) Kind() domain.ProviderKind { return domain.ProviderChat }

func (o *Adapter) chatURL() string {
	return aiutil.JoinEndpoint(o.BaseURL, "/chat/completions")
}

func (o *Adapter) headers() map[string]string {
	h := map[string]string{}
	if o.APIKey != "" {
		h["Authorization"] = "Bearer " + o.APIKey
	}
	if aiutil.IsOpenRouterURL(o.BaseURL) {
		for k, v := range aiutil.OpenRouterAttributionHeaders() {
			h[k] = v
		}
	}
	return h
}

type openAIMessage struct {
	Role             string           `json:"role"`
	Content          any              `json:"content,omitempty"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
}

type openAIToolCall struct {
	Index    int            `json:"index"`
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function map[string]any `json:"function"`
}

type openAIRequest struct {
	Model            string          `json:"model"`
	Messages         []openAIMessage `json:"messages"`
	Tools            []openAITool    `json:"tools,omitempty"`
	Stream           bool            `json:"stream"`
	MaxTokens        int             `json:"max_tokens,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	PromptCacheKey   string          `json:"prompt_cache_key,omitempty"`
}

type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			// OpenRouter exposes thinking via "reasoning" (not
			// "reasoning_content") in both streaming deltas and
			// non-streaming messages. Fallback below merges the two.
			Reasoning *string          `json:"reasoning"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content          *string          `json:"content"`
			ReasoningContent *string          `json:"reasoning_content"`
			Reasoning        *string          `json:"reasoning"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func toOpenAIMessages(req application.ChatRequest) []openAIMessage {
	var out []openAIMessage
	for _, m := range req.Messages {
		switch m.Role {
		case "user", "system":
			content := any(m.Content)
			if m.Role == "user" && len(m.Attachments) > 0 {
				blocks := make([]map[string]any, 0, 1+len(m.Attachments))
				if m.Content != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
				}
				for _, attachment := range m.Attachments {
					switch attachment.Type {
					case "text":
						blocks = append(blocks, map[string]any{"type": "text", "text": aiutil.TextAttachmentContent(attachment)})
					case "audio":
						blocks = append(blocks, inputAudioBlock(attachment))
					case "video":
						blocks = append(blocks, aiutil.VideoURLBlock(attachment))
					case "image":
						blocks = append(blocks, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": attachment.DataURL, "detail": "auto"},
						})
					case "file":
						// Chat Completions has no portable file-input part. Electron keeps
						// the document visible to the model as a descriptive text block.
						blocks = append(blocks, map[string]any{"type": "text", "text": aiutil.DocumentAttachmentContent(attachment)})
					}
				}
				content = blocks
			}
			out = append(out, openAIMessage{Role: m.Role, Content: content})
		case "assistant":
			content := m.Content
			msg := openAIMessage{Role: "assistant", Content: content}
			for _, tc := range m.ToolCalls {
				// Auto-heal: sanitize hallucinated tool names that violate
				// the OpenAI function name pattern (e.g. "terminal:exec").
				// Pairing is by tool_call_id, not by name.
				msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
					ID:       tc.ID,
					Type:     "function",
					Function: openAIFunction{Name: aiutil.SanitizeToolName(tc.Name), Arguments: tc.Args},
				})
			}
			// Reasoning replay: thinking-mode upstreams (DeepSeek V4, GLM,
			// Kimi, ox-alpha, etc.) require reasoning_content to be echoed
			// back on every assistant message in subsequent turns, or they
			// 400 with "reasoning_content must be passed back". When the
			// flag is set, inject the persisted reasoning text. If the
			// reasoning is empty, inject a non-empty placeholder — some
			// providers (MiMo) reject an absent field, others (DeepSeek)
			// accept absent but reject empty-string.
			if req.ReasoningReplay {
				if m.Reasoning != "" && !domain.IsReasoningPlaceholder(m.Reasoning) {
					msg.ReasoningContent = m.Reasoning
				} else {
					msg.ReasoningContent = domain.ReasoningPlaceholder
				}
			}
			out = append(out, msg)
		case "tool":
			content := any(m.ToolResult.Content)
			if len(m.ToolResult.Attachments) > 0 {
				content = openAIToolContent(m.ToolResult)
			}
			out = append(out, openAIMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: m.ToolResult.ToolCallID,
			})
		}
	}
	return out
}

// openAIToolContent builds a multimodal content array for tool results that
// carry image/audio/video attachments (e.g. read_media). Images use the image_url transport; audio uses input_audio;
// video uses video_url (OpenRouter's dedicated video content type — OpenAI
// does not support video natively, and sending video through image_url
// causes providers to reject it with HTTP 400 because they attempt image
// decoding on a video payload). Video blocks only reach this function for
// models with Video=true; non-video models have video stripped by
// filterToolAttachmentsByCaps before the request is built.
func openAIToolContent(result *application.ToolResult) []map[string]any {
	blocks := make([]map[string]any, 0, 1+len(result.Attachments))
	if result.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": result.Content})
	}
	for _, att := range result.Attachments {
		switch att.Type {
		case "audio":
			blocks = append(blocks, inputAudioBlock(att))
		case "video":
			blocks = append(blocks, aiutil.VideoURLBlock(att))
		case "image":
			blocks = append(blocks, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": att.DataURL, "detail": "auto"},
			})
		}
	}
	return blocks
}

// inputAudioBlock encodes an audio attachment as the Chat Completions
// input_audio part. Delegates to aiutil.InputAudioBlock which is shared
// with the Responses API handler.
func inputAudioBlock(att domain.Attachment) map[string]any {
	return aiutil.InputAudioBlock(att)
}

func buildRequest(req application.ChatRequest, stream bool) openAIRequest {
	tools := make([]openAITool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, openAITool{
			Type: "function",
			Function: map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	r := openAIRequest{
		Model:            req.Model,
		Messages:         toOpenAIMessages(req),
		Tools:            tools,
		Stream:           stream,
		MaxTokens:        req.MaxTokens,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
	}
	if req.Effort != "" && req.Effort != "auto" && !containsParam(req.StripParams, "reasoning_effort") {
		r.ReasoningEffort = req.Effort
	}
	// Apply learned strip params: null out sampling fields the upstream
	// has rejected with a 400 "Unsupported parameter" so we don't keep
	// sending them and burning a retry.
	if containsParam(req.StripParams, "temperature") {
		r.Temperature = nil
	}
	if containsParam(req.StripParams, "top_p") {
		r.TopP = nil
	}
	if containsParam(req.StripParams, "frequency_penalty") {
		r.FrequencyPenalty = nil
	}
	if containsParam(req.StripParams, "presence_penalty") {
		r.PresencePenalty = nil
	}
	if req.PromptCache != nil && req.PromptCache.Mode != "off" && req.PromptCache.Key != "" {
		r.PromptCacheKey = req.PromptCache.Key
	}
	return r
}

// containsParam reports whether the strip list includes name. The list is
// case-insensitive (learned entries are lowercased on record).
func containsParam(strip []string, name string) bool {
	target := strings.ToLower(name)
	for _, p := range strip {
		if strings.ToLower(p) == target {
			return true
		}
	}
	return false
}

func (o *Adapter) Complete(ctx context.Context, req application.ChatRequest) (application.ChatResponse, error) {
	var out openAIResponse
	if err := aiutil.DoJSON(ctx, o.Client, http.MethodPost, o.chatURL(), o.headers(), buildRequest(req, false), &out); err != nil {
		return application.ChatResponse{}, err
	}
	if out.Error != nil {
		return application.ChatResponse{}, fmt.Errorf("provider error: %s", out.Error.Message)
	}
	resp, err := o.responseFromOpenAI(out)
	if err != nil {
		return application.ChatResponse{}, err
	}
	return resp, nil
}

func (o *Adapter) Stream(ctx context.Context, req application.ChatRequest, onDelta, onReasoning func(string)) (application.ChatResponse, error) {
	resp, err := aiutil.OpenSSE(ctx, o.Client, o.chatURL(), o.headers(), buildRequest(req, true))
	if err != nil {
		if aiutil.IsStreamUnsupportedError(err) {
			return aiutil.StreamFallbackToComplete(ctx, o, req, onDelta, onReasoning)
		}
		if aiutil.ShouldRetryWithoutImages(err, req.Messages, ctx) {
			stripped := req
			stripped.Messages = aiutil.StripImages(req.Messages)
			return o.Stream(ctx, stripped, onDelta, onReasoning)
		}
		return application.ChatResponse{}, err
	}
	defer resp.Body.Close()

	var result application.ChatResponse
	toolAcc := map[int]*domain.ToolCall{}
	completed := false
	err = aiutil.ReadSSE(ctx, resp.Body, aiutil.DefaultIdleTimeout, func(ev aiutil.Event) error {
		if ev.Data == "[DONE]" {
			completed = true
			return nil
		}
		var chunk openAIChunk
		if err := aiutil.DecodeData(ev, &chunk); err != nil {
			return err
		}
		if chunk.Usage != nil {
			// Normalize: OpenAI-style prompt_tokens is the TOTAL prompt
			// (uncached + cached). Subtract cached_tokens so InputTokens
			// is the UNCACHED input, matching the Anthropic convention.
			cached := chunk.Usage.PromptTokensDetails.CachedTokens
			uncached := chunk.Usage.PromptTokens - cached
			if uncached < 0 {
				uncached = 0
			}
			result.Usage = application.ChatUsage{
				InputTokens:  uncached,
				OutputTokens: chunk.Usage.CompletionTokens,
				CacheRead:    cached,
			}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != nil {
				result.Content += *ch.Delta.Content
				onDelta(*ch.Delta.Content)
			}
			// reasoning models (DeepSeek, Qwen, …) stream thinking here;
			// null deltas between segments must not append anything
			// OpenRouter uses "reasoning" while native OpenAI/DeepSeek use
			// "reasoning_content" — fall back to either field.
			reasoningDelta := ch.Delta.ReasoningContent
			if reasoningDelta == nil {
				reasoningDelta = ch.Delta.Reasoning
			}
			if reasoningDelta != nil {
				// Strip the internal replay placeholder from streamed
				// reasoning — models may echo it (#9573-style echo loop).
				cleaned := strings.ReplaceAll(*reasoningDelta, domain.ReasoningPlaceholder, "")
				if cleaned != "" {
					result.Reasoning += cleaned
					if onReasoning != nil {
						onReasoning(cleaned)
					}
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				acc := toolAcc[tc.Index]
				if acc == nil {
					acc = &domain.ToolCall{ID: tc.ID, Name: tc.Function.Name}
					toolAcc[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.ID = tc.ID
				}
				if tc.Function.Name != "" {
					acc.Name = tc.Function.Name
				}
				acc.Args += tc.Function.Arguments
			}
		}
		return nil
	})
	if err != nil {
		return result, aiutil.RetryableSSEReadError(err)
	}
	// Finalize accumulated tool calls before the incomplete-stream check so
	// that a stream which closed without the terminator but carried tool-call
	// deltas is NOT misclassified as empty (which would silently discard the
	// tool calls and fall back to non-streaming).
	indexes := make([]int, 0, len(toolAcc))
	for index := range toolAcc {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		tc := toolAcc[index]
		tc.Args = aiutil.RepairToolCallArguments(tc.Args)
		result.ToolCalls = append(result.ToolCalls, *tc)
	}
	if !completed {
		emptyErr := aiutil.IncompleteSSEError()
		if aiutil.IsIncompleteEmptyStream(emptyErr, result) {
			return aiutil.StreamFallbackToComplete(ctx, o, req, onDelta, onReasoning)
		}
		return result, emptyErr
	}
	// Stream completed but produced no content, reasoning, or tool calls.
	// This happens with unstable upstream gateways that return a 200 with
	// an empty SSE body. Fall back to a non-streaming Complete request once
	// — such gateways often serve non-streaming fine, so the turn continues
	// instead of failing. If Complete is also empty, surface a retryable
	// transport error so the agent retry loop can re-request rather than
	// silently ending the turn with no output.
	if result.Content == "" && result.Reasoning == "" && len(result.ToolCalls) == 0 {
		resp, fallbackErr := aiutil.StreamFallbackToComplete(ctx, o, req, onDelta, onReasoning)
		if fallbackErr != nil {
			return resp, fallbackErr
		}
		if resp.Content != "" || resp.Reasoning != "" || len(resp.ToolCalls) != 0 {
			return resp, nil
		}
		return resp, &application.UpstreamError{
			Kind:      application.KindSSETransport,
			Temporary: true,
			Err:       fmt.Errorf("provider returned empty content"),
		}
	}
	return result, nil
}

func (o *Adapter) ListModels(ctx context.Context, apiKey string) ([]domain.Model, error) {
	base := strings.TrimRight(o.BaseURL, "/")
	url := base + "/models"
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
			MaxTokens     int    `json:"max_tokens"`
			Description   string `json:"description"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
			Reasoning struct {
				SupportedEfforts []string `json:"supported_efforts"`
				DefaultEffort    string   `json:"default_effort"`
			} `json:"reasoning"`
		} `json:"data"`
	}
	if err := aiutil.DoJSON(ctx, o.Client, http.MethodGet, url, headers, nil, &out); err != nil {
		return nil, err
	}
	models := make([]domain.Model, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		model := domain.Model{ID: m.ID, Context: m.ContextLength, MaxOutput: m.MaxTokens, Description: m.Description, SupportedEfforts: aiutil.NormalizeEfforts(m.Reasoning.SupportedEfforts), DefaultEffort: aiutil.NormalizeEffort(m.Reasoning.DefaultEffort)}
		if v, err := aiutil.ParseFloat(m.Pricing.Prompt); err == nil {
			model.InputCost = v * 1_000_000
		}
		models = append(models, model)
	}
	return models, nil
}

func (o *Adapter) responseFromOpenAI(out openAIResponse) (application.ChatResponse, error) {
	if len(out.Choices) == 0 {
		return application.ChatResponse{}, fmt.Errorf("provider returned no choices")
	}
	ch := out.Choices[0]
	// OpenRouter returns thinking via "reasoning"; native OpenAI/DeepSeek
	// use "reasoning_content". Fall back to either field.
	reasoningText := ch.Message.ReasoningContent
	if reasoningText == nil {
		reasoningText = ch.Message.Reasoning
	}
	resp := application.ChatResponse{
		Content:    aiutil.Deref(ch.Message.Content),
		Reasoning:  strings.ReplaceAll(aiutil.Deref(reasoningText), domain.ReasoningPlaceholder, ""),
		StopReason: aiutil.Deref(ch.FinishReason),
	}
	if out.Usage != nil {
		// OpenAI-style providers report prompt_tokens as the TOTAL prompt
		// (uncached + cached). Normalize to the Anthropic convention where
		// InputTokens is the UNCACHED input only, so downstream telemetry
		// (cost, charts, ContextTokens) is consistent across providers.
		cached := out.Usage.PromptTokensDetails.CachedTokens
		uncached := out.Usage.PromptTokens - cached
		if uncached < 0 {
			uncached = 0
		}
		resp.Usage = application.ChatUsage{
			InputTokens:  uncached,
			OutputTokens: out.Usage.CompletionTokens,
			CacheRead:    cached,
		}
	}
	for _, tc := range ch.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, domain.ToolCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: aiutil.RepairToolCallArguments(tc.Function.Arguments),
		})
	}
	return resp, nil
}
