// Package responses implements the application.AIProvider port for the
// OpenAI Responses API (/v1/responses) with SSE streaming, function calling,
// and usage including cached tokens.
package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/chatcompletion"
	aiutil "nusashell/infrastructure/ai/internal"
)

// Adapter talks to the OpenAI Responses API (/v1/responses) with
// SSE streaming, function calling, and usage including cached tokens.
type Adapter struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func (r *Adapter) Kind() domain.ProviderKind { return domain.ProviderResponses }

func (r *Adapter) responsesURL() string {
	return aiutil.JoinEndpoint(r.BaseURL, "/responses")
}

// chatFallback returns an OpenAI chat-completions adapter sharing this
// adapter's base URL, API key, and HTTP client. Used when the Responses API
// is not available (404/405/unsupported), falling back to /chat/completions.
func (r *Adapter) chatFallback() *chatcompletion.Adapter {
	return &chatcompletion.Adapter{BaseURL: r.BaseURL, APIKey: r.APIKey, Client: r.Client}
}

func (r *Adapter) headers() map[string]string {
	h := map[string]string{}
	if r.APIKey != "" {
		h["Authorization"] = "Bearer " + r.APIKey
	}
	if aiutil.IsOpenRouterURL(r.BaseURL) {
		for k, v := range aiutil.OpenRouterAttributionHeaders() {
			h[k] = v
		}
	}
	return h
}

// ---- wire types ----

type responsesInputItem struct {
	Role    string          `json:"role,omitempty"`
	Type    string          `json:"type,omitempty"` // function_call | function_call_output
	Content json.RawMessage `json:"content,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Args    string          `json:"arguments,omitempty"`
	// Summary carries reasoning summary text for {type:"reasoning"} input
	// items (reasoning replay). Each entry is {type:"summary_text",text:"..."}.
	Summary []responsesReasoningSummary `json:"summary,omitempty"`
	// Output is a JSON value: a string for text-only tool results, or an
	// array of input_text / input_image items when the tool returned
	// attachments (read_media, generate_image). RawMessage keeps both shapes.
	Output json.RawMessage `json:"output,omitempty"`
}

// responsesReasoningSummary is one entry in a reasoning item's summary
// array, used both for response decoding and request input (replay).
type responsesReasoningSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesContentBlock struct {
	Type   string `json:"type,omitempty"`
	Text   string `json:"text,omitempty"`
	CallID string `json:"call_id,omitempty"`
	Name   string `json:"name,omitempty"`
	Args   string `json:"arguments,omitempty"`
}

type responsesRequest struct {
	Model              string               `json:"model"`
	Instructions       string               `json:"instructions,omitempty"`
	Input              []responsesInputItem `json:"input,omitempty"`
	Tools              []responsesToolDef   `json:"tools,omitempty"`
	Stream             bool                 `json:"stream"`
	MaxOutputTokens    int                  `json:"max_output_tokens,omitempty"`
	Reasoning          *responsesReasoning  `json:"reasoning,omitempty"`
	Temperature        *float64             `json:"temperature,omitempty"`
	TopP               *float64             `json:"top_p,omitempty"`
	FrequencyPenalty   *float64             `json:"frequency_penalty,omitempty"`
	PresencePenalty    *float64             `json:"presence_penalty,omitempty"`
	PromptCacheKey     string               `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions *responsesCacheOpts  `json:"prompt_cache_options,omitempty"`
}

type responsesCacheOpts struct {
	Mode string `json:"mode"`
	TTL  string `json:"ttl,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type responsesToolDef struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// toResponsesInput flattens ChatMessages into the canonical Responses API
// input shape: message items carry string or array content, function calls
// and their outputs are top-level items. Every message item MUST include
// type:"message" — the Responses API validates input items as a union of
// {type:"message",...} | {type:"reasoning",...} | {type:"function_call",...}
// | {type:"function_call_output",...} and rejects items without a type
// field (strict providers like Stealth return HTTP 400 invalid_prompt).
//
// When reasoningReplay is true, a {type:"reasoning"} item is emitted before
// each assistant message item carrying the persisted reasoning summary.
// Thinking-mode upstreams (GLM, DeepSeek V4, Kimi, ox-alpha) require this
// to preserve reasoning state across turns — without it the model loses
// its chain-of-thought and degrades into tool-call loops.
func toResponsesInput(msgs []application.ChatMessage, reasoningReplay bool) []responsesInputItem {
	var out []responsesInputItem
	for _, m := range msgs {
		switch m.Role {
		case "user":
			content := aiutil.StrJSON(m.Content)
			if len(m.Attachments) > 0 {
				blocks := make([]map[string]any, 0, 1+len(m.Attachments))
				if m.Content != "" {
					blocks = append(blocks, map[string]any{"type": "input_text", "text": m.Content})
				}
				for _, attachment := range m.Attachments {
					switch attachment.Type {
					case "text":
						blocks = append(blocks, map[string]any{"type": "input_text", "text": aiutil.TextAttachmentContent(attachment)})
					case "audio":
						blocks = append(blocks, aiutil.InputAudioBlock(attachment))
					case "video":
						blocks = append(blocks, aiutil.VideoURLBlock(attachment))
					case "image":
						blocks = append(blocks, aiutil.InputImageBlock(attachment))
					case "file":
						blocks = append(blocks, map[string]any{
							"type": "input_file", "file_data": attachment.DataURL, "filename": attachment.Name,
						})
					}
				}
				content = aiutil.MustJSON(blocks)
			}
			out = append(out, responsesInputItem{Type: "message", Role: "user", Content: content})
		case "assistant":
			// Reasoning replay: emit a reasoning item before the assistant
			// message so the upstream can reconstruct the thinking state.
			// The summary text is the persisted reasoning from the prior
			// turn. When empty, inject a non-empty placeholder — some
			// providers reject an absent reasoning item, others reject an
			// empty summary.
			if reasoningReplay {
				summaryText := m.Reasoning
				if summaryText == "" || domain.IsReasoningPlaceholder(summaryText) {
					summaryText = domain.ReasoningPlaceholder
				}
				out = append(out, responsesInputItem{
					Type:    "reasoning",
					Summary: []responsesReasoningSummary{{Type: "summary_text", Text: summaryText}},
				})
			}
			if m.Content != "" {
				out = append(out, responsesInputItem{Type: "message", Role: "assistant", Content: aiutil.StrJSON(m.Content)})
			}
			for _, tc := range m.ToolCalls {
				// Auto-heal: models occasionally hallucinate tool names with
				// characters the Responses API rejects (pattern ^[a-zA-Z0-9_-]+$),
				// e.g. "terminal:exec" or "fs.read". Sanitize on the wire so a
				// bad name in history does not make the whole conversation
				// unreplayable. Pairing is by call_id, not by name.
				out = append(out, responsesInputItem{
					Type: "function_call", CallID: tc.ID, Name: aiutil.SanitizeToolName(tc.Name), Args: tc.Args,
				})
			}
		case "tool":
			var output json.RawMessage
			if m.ToolResult == nil {
				output = json.RawMessage(`""`)
			} else if len(m.ToolResult.Attachments) == 0 {
				if b, err := json.Marshal(m.ToolResult.Content); err == nil {
					output = b
				} else {
					output = json.RawMessage(`""`)
				}
			} else {
				output = aiutil.MustJSON(responsesToolOutputContent(m.ToolResult))
			}
			out = append(out, responsesInputItem{
				Type:   "function_call_output",
				CallID: m.ToolResult.ToolCallID,
				Output: output,
			})
		}
	}
	return out
}

// responsesToolOutputContent builds a multimodal content array for tool
// results that carry image/audio/video attachments. Images use input_image;
// audio uses input_audio; video uses video_url (OpenRouter's dedicated
// video content type — OpenAI does not support video natively, and sending
// video through input_image causes providers to reject it with HTTP 400).
// Video blocks only reach this function for models with Video=true;
// non-video models have video stripped by filterToolAttachmentsByCaps
// before the request is built.
func responsesToolOutputContent(result *application.ToolResult) []map[string]any {
	blocks := make([]map[string]any, 0, 1+len(result.Attachments))
	if result.Content != "" {
		blocks = append(blocks, map[string]any{"type": "input_text", "text": result.Content})
	}
	for _, att := range result.Attachments {
		switch att.Type {
		case "audio":
			blocks = append(blocks, aiutil.InputAudioBlock(att))
		case "video":
			blocks = append(blocks, aiutil.VideoURLBlock(att))
		case "image":
			blocks = append(blocks, aiutil.InputImageBlock(att))
		}
	}
	return blocks
}

func buildResponsesRequest(req application.ChatRequest, stream bool) responsesRequest {
	out := responsesRequest{
		Model:            req.Model,
		Instructions:     req.System,
		Input:            toResponsesInput(req.Messages, req.ReasoningReplay),
		Stream:           stream,
		MaxOutputTokens:  req.MaxTokens,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
	}
	if req.Effort != "" && req.Effort != "auto" {
		out.Reasoning = &responsesReasoning{Effort: req.Effort}
	}
	if req.PromptCache != nil && req.PromptCache.Mode != "off" {
		if req.PromptCache.Key != "" {
			out.PromptCacheKey = req.PromptCache.Key
		}
		if req.PromptCache.Mode == "explicit" {
			out.PromptCacheOptions = &responsesCacheOpts{Mode: "explicit", TTL: req.PromptCache.TTL}
		}
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, responsesToolDef{
			Type: "function", Name: t.Name, Description: t.Description, Parameters: t.InputSchema,
		})
	}
	return out
}

type responsesNonStreamResponse struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"summary"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
		Args   string `json:"arguments"`
	} `json:"output"`
	Usage responsesUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (r *Adapter) Complete(ctx context.Context, req application.ChatRequest) (application.ChatResponse, error) {
	var out responsesNonStreamResponse
	if err := aiutil.DoJSON(ctx, r.Client, http.MethodPost, r.responsesURL(), r.headers(), buildResponsesRequest(req, false), &out); err != nil {
		if aiutil.IsResponsesUnsupported(err) {
			return r.chatFallback().Complete(ctx, req)
		}
		return application.ChatResponse{}, err
	}
	if out.Error != nil {
		return application.ChatResponse{}, fmt.Errorf("provider error: %s", out.Error.Message)
	}
	// Normalize: OpenAI Responses API reports input_tokens as the TOTAL
	// prompt (uncached + cached). Subtract cached_tokens so InputTokens is
	// the UNCACHED input, matching the Anthropic convention.
	cached := out.Usage.InputTokensDetails.CachedTokens
	uncached := out.Usage.InputTokens - cached
	if uncached < 0 {
		uncached = 0
	}
	resp := application.ChatResponse{
		Usage: application.ChatUsage{
			InputTokens:  uncached,
			OutputTokens: out.Usage.OutputTokens,
			CacheRead:    cached,
		},
	}
	for _, item := range out.Output {
		switch item.Type {
		case "message":
			for _, block := range item.Content {
				if block.Type == "output_text" {
					resp.Content += block.Text
				}
			}
		case "reasoning":
			for _, s := range item.Summary {
				resp.Reasoning += s.Text
			}
			// Strip the internal replay placeholder from user-visible
			// reasoning — models may echo it (#9573-style echo loop).
			resp.Reasoning = strings.ReplaceAll(resp.Reasoning, domain.ReasoningPlaceholder, "")
		case "function_call":
			resp.ToolCalls = append(resp.ToolCalls, domain.ToolCall{
				ID: item.CallID, Name: item.Name, Args: aiutil.RepairToolCallArguments(item.Args),
			})
		}
	}
	return resp, nil
}

func (r *Adapter) Stream(ctx context.Context, req application.ChatRequest, onDelta, onReasoning func(string)) (application.ChatResponse, error) {
	resp, err := aiutil.OpenSSE(ctx, r.Client, r.responsesURL(), r.headers(), buildResponsesRequest(req, true))
	if err != nil {
		if aiutil.IsResponsesUnsupported(err) {
			return r.chatFallback().Stream(ctx, req, onDelta, onReasoning)
		}
		if aiutil.IsStreamUnsupportedError(err) {
			return aiutil.StreamFallbackToComplete(ctx, r, req, onDelta, onReasoning)
		}
		if aiutil.ShouldRetryWithoutImages(err, req.Messages, ctx) {
			stripped := req
			stripped.Messages = aiutil.StripImages(req.Messages)
			return r.Stream(ctx, stripped, onDelta, onReasoning)
		}
		return application.ChatResponse{}, err
	}
	defer resp.Body.Close()

	var result application.ChatResponse
	toolByIndex := map[int]*domain.ToolCall{}
	streamErr := error(nil)
	completed := false
	readErr := aiutil.ReadSSE(ctx, resp.Body, aiutil.DefaultIdleTimeout, func(ev aiutil.Event) error {
		// gateways (OpenRouter) terminate Responses streams with the chat
		// completions sentinel
		if ev.Data == "[DONE]" {
			completed = true
			return nil
		}
		var frame struct {
			Type        string `json:"type"`
			OutputIndex int    `json:"output_index"`
			Delta       string `json:"delta"`
			Item        *struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			} `json:"item"`
			Response *struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
				Usage responsesUsage `json:"usage"`
			} `json:"response"`
		}
		if err := aiutil.DecodeData(ev, &frame); err != nil {
			return err
		}
		switch frame.Type {
		case "response.output_text.delta":
			result.Content += frame.Delta
			onDelta(frame.Delta)
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			// Strip the internal replay placeholder from streamed
			// reasoning — models may echo it (#9573-style echo loop).
			cleaned := strings.ReplaceAll(frame.Delta, domain.ReasoningPlaceholder, "")
			if cleaned != "" {
				result.Reasoning += cleaned
				if onReasoning != nil {
					onReasoning(cleaned)
				}
			}
		case "response.output_item.added":
			if frame.Item != nil && frame.Item.Type == "function_call" {
				toolByIndex[frame.OutputIndex] = &domain.ToolCall{ID: frame.Item.CallID, Name: frame.Item.Name}
			}
		case "response.function_call_arguments.delta":
			if acc := toolByIndex[frame.OutputIndex]; acc != nil {
				acc.Args += frame.Delta
			}
		case "response.completed":
			completed = true
			if frame.Response != nil {
				// Normalize: OpenAI Responses API reports input_tokens as
				// the TOTAL prompt (uncached + cached). Subtract cached
				// tokens so InputTokens is the UNCACHED input, matching
				// the Anthropic convention.
				cached := frame.Response.Usage.InputTokensDetails.CachedTokens
				uncached := frame.Response.Usage.InputTokens - cached
				if uncached < 0 {
					uncached = 0
				}
				result.Usage = application.ChatUsage{
					InputTokens:  uncached,
					OutputTokens: frame.Response.Usage.OutputTokens,
					CacheRead:    cached,
				}
			}
		case "response.failed":
			msg := "provider stream failed"
			if frame.Response != nil && frame.Response.Error != nil {
				msg = frame.Response.Error.Message
			}
			streamErr = &application.UpstreamError{
				Kind:      application.KindSSETransport,
				Temporary: true,
				Err:       fmt.Errorf("%s", msg),
			}
			return streamErr
		}
		return nil
	})
	if readErr != nil {
		return result, aiutil.RetryableSSEReadError(readErr)
	}
	if streamErr != nil {
		return result, streamErr
	}
	// Finalize accumulated tool calls before the incomplete-stream check so
	// that a stream which closed without the terminator but carried tool-call
	// deltas is NOT misclassified as empty (which would silently discard the
	// tool calls and fall back to non-streaming).
	seen := map[string]bool{}
	for _, tc := range toolByIndex {
		if tc.ID == "" || seen[tc.ID] {
			continue
		}
		seen[tc.ID] = true
		tc.Args = aiutil.RepairToolCallArguments(tc.Args)
		result.ToolCalls = append(result.ToolCalls, *tc)
	}
	if !completed {
		emptyErr := aiutil.IncompleteSSEError()
		if aiutil.IsIncompleteEmptyStream(emptyErr, result) {
			return aiutil.StreamFallbackToComplete(ctx, r, req, onDelta, onReasoning)
		}
		return result, emptyErr
	}
	// Stream completed but produced no content, reasoning, or tool calls.
	// This happens with unstable upstream gateways (e.g. AGY/OmniRoute
	// proxying to Gemini) that return a 200 with an empty SSE body. Fall
	// back to a non-streaming Complete request once — such gateways often
	// serve non-streaming fine, so the turn continues instead of failing.
	// If Complete is also empty, surface a retryable transport error so the
	// agent retry loop can re-request rather than silently ending the turn.
	if result.Content == "" && result.Reasoning == "" && len(result.ToolCalls) == 0 {
		resp, fallbackErr := aiutil.StreamFallbackToComplete(ctx, r, req, onDelta, onReasoning)
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

func (r *Adapter) ListModels(ctx context.Context, apiKey string) ([]domain.Model, error) {
	base := strings.TrimRight(r.BaseURL, "/")
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
	if err := aiutil.DoJSON(ctx, r.Client, http.MethodGet, url, headers, nil, &out); err != nil {
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
