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
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
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
			Content          *string          `json:"content"`
			ReasoningContent *string          `json:"reasoning_content"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content   *string          `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
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
				content = openAIUserContent(m)
			}
			out = append(out, openAIMessage{Role: m.Role, Content: content})
		case "assistant":
			content := m.Content
			msg := openAIMessage{Role: "assistant", Content: content}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
					ID:       tc.ID,
					Type:     "function",
					Function: openAIFunction{Name: tc.Name, Arguments: tc.Args},
				})
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
// carry image attachments (e.g. read_image). The text content comes first,
// followed by image_url blocks for each attachment.
func openAIToolContent(result *application.ToolResult) []map[string]any {
	blocks := make([]map[string]any, 0, 1+len(result.Attachments))
	if result.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": result.Content})
	}
	for _, att := range result.Attachments {
		if att.Type == "image" {
			blocks = append(blocks, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": att.DataURL},
			})
		}
	}
	return blocks
}

func openAIUserContent(message application.ChatMessage) []map[string]any {
	blocks := make([]map[string]any, 0, 1+len(message.Attachments))
	if message.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": message.Content})
	}
	for _, attachment := range message.Attachments {
		switch attachment.Type {
		case "text":
			blocks = append(blocks, map[string]any{"type": "text", "text": aiutil.TextAttachmentContent(attachment)})
		case "image":
			blocks = append(blocks, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": attachment.DataURL},
			})
		case "file":
			// Chat Completions has no portable file-input part. Electron keeps
			// the document visible to the model as a descriptive text block.
			blocks = append(blocks, map[string]any{"type": "text", "text": aiutil.DocumentAttachmentContent(attachment)})
		}
	}
	return blocks
}

func openAITools(tools []application.ToolDef) []openAITool {
	var out []openAITool
	for _, t := range tools {
		out = append(out, openAITool{
			Type: "function",
			Function: map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}
	return out
}

func buildRequest(req application.ChatRequest, stream bool) openAIRequest {
	r := openAIRequest{
		Model:            req.Model,
		Messages:         toOpenAIMessages(req),
		Tools:            openAITools(req.Tools),
		Stream:           stream,
		MaxTokens:        req.MaxTokens,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
	}
	if req.Effort != "" && req.Effort != "auto" {
		r.ReasoningEffort = req.Effort
	}
	if req.PromptCache != nil && req.PromptCache.Mode != "off" && req.PromptCache.Key != "" {
		r.PromptCacheKey = req.PromptCache.Key
	}
	return r
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
			result.Usage = application.ChatUsage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != nil {
				result.Content += *ch.Delta.Content
				onDelta(*ch.Delta.Content)
			}
			// reasoning models (DeepSeek, Qwen, …) stream thinking here;
			// null deltas between segments must not append anything
			if ch.Delta.ReasoningContent != nil {
				result.Reasoning += *ch.Delta.ReasoningContent
				if onReasoning != nil {
					onReasoning(*ch.Delta.ReasoningContent)
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
	resp := application.ChatResponse{
		Content:    aiutil.Deref(ch.Message.Content),
		StopReason: aiutil.Deref(ch.FinishReason),
	}
	if out.Usage != nil {
		resp.Usage = application.ChatUsage{
			InputTokens:  out.Usage.PromptTokens,
			OutputTokens: out.Usage.CompletionTokens,
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
