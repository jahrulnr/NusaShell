// Package messages implements the application.AIProvider port for the
// Anthropic Messages API with SSE streaming, tool use, and ephemeral prompt
// caching.
package messages

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	aiutil "nusashell/infrastructure/ai/internal"
)

const anthropicVersion = "2023-06-01"

// Adapter talks to the Anthropic Messages API with SSE streaming,
// tool use, and ephemeral prompt caching.
type Adapter struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func (a *Adapter) Kind() domain.ProviderKind { return domain.ProviderMessages }

// messagesURL appends the Messages operation. When the base already carries
// a version or a full endpoint it is honored verbatim; a bare root (e.g.
// https://api.anthropic.com or a gateway's /api/anthropic compat root) gets
// the Anthropic-compatible convention suffix /v1/messages — this is how
// Anthropic-compatible servers (including GLM/Zhipu) expose the endpoint.
func (a *Adapter) messagesURL() string {
	base := strings.TrimRight(a.BaseURL, "/")
	if strings.HasSuffix(base, "/messages") {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

func (a *Adapter) headers() map[string]string {
	h := map[string]string{
		"x-api-key":         a.APIKey,
		"anthropic-version": anthropicVersion,
	}
	if aiutil.IsOpenRouterURL(a.BaseURL) {
		for k, v := range aiutil.OpenRouterAttributionHeaders() {
			h[k] = v
		}
	}
	return h
}

type anthropicContentBlock struct {
	Type         string           `json:"type,omitempty"`
	Text         string           `json:"text,omitempty"`
	ID           string           `json:"id,omitempty"`
	Name         string           `json:"name,omitempty"`
	Input        json.RawMessage  `json:"input,omitempty"`
	ToolUseID    string           `json:"tool_use_id,omitempty"`
	Content      string           `json:"content,omitempty"`
	Source       *anthropicSource `json:"source,omitempty"`
	Title        string           `json:"title,omitempty"`
	CacheControl *cacheControl    `json:"cache_control,omitempty"`
}

type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type cacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      json.RawMessage    `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicToolDef `json:"tools,omitempty"`
	Stream      bool               `json:"stream"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	TopK        *int               `json:"top_k,omitempty"`
}

type anthropicToolDef struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	CacheControl *cacheControl  `json:"cache_control,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// contentBlocks converts a ChatMessage list into Anthropic messages with
// tool_use / tool_result blocks.
func toAnthropicMessages(msgs []application.ChatMessage) []anthropicMessage {
	var out []anthropicMessage
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: aiutil.MustJSON(anthropicUserContent(m)),
			})
		case "assistant":
			var blocks []anthropicContentBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: json.RawMessage(tc.Args),
				})
			}
			if len(blocks) == 0 {
				continue
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: aiutil.MustJSON(blocks)})
		case "tool":
			out = append(out, anthropicMessage{
				Role: "user",
				Content: aiutil.MustJSON([]anthropicContentBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolResult.ToolCallID,
					Content:   m.ToolResult.Content,
				}}),
			})
		}
	}
	return out
}

func anthropicUserContent(message application.ChatMessage) []anthropicContentBlock {
	blocks := make([]anthropicContentBlock, 0, 1+len(message.Attachments))
	if message.Content != "" {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: message.Content})
	}
	for _, attachment := range message.Attachments {
		switch attachment.Type {
		case "text":
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: aiutil.TextAttachmentContent(attachment)})
		case "image":
			blocks = append(blocks, anthropicContentBlock{
				Type: "image", Source: &anthropicSource{
					Type: "base64", MediaType: attachment.MediaType, Data: aiutil.DataURLBase64(attachment.DataURL),
				},
			})
		case "file":
			blocks = append(blocks, anthropicContentBlock{
				Type: "document", Title: attachment.Name, Source: &anthropicSource{
					Type: "base64", MediaType: attachment.MediaType, Data: aiutil.DataURLBase64(attachment.DataURL),
				},
			})
		}
	}
	return blocks
}

func buildAnthropicRequest(req application.ChatRequest, stream bool) anthropicRequest {
	out := anthropicRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Messages:    toAnthropicMessages(req.Messages),
		Stream:      stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		TopK:        req.TopK,
	}
	cacheTTL := ""
	if req.PromptCache != nil && req.PromptCache.TTL == "1h" {
		cacheTTL = "1h"
	}
	if req.System != "" {
		if req.PromptCaching {
			out.System = aiutil.MustJSON([]anthropicContentBlock{{
				Type: "text", Text: req.System, CacheControl: &cacheControl{Type: "ephemeral", TTL: cacheTTL},
			}})
		} else {
			out.System = aiutil.MustJSON([]anthropicContentBlock{{Type: "text", Text: req.System}})
		}
	}
	for _, t := range req.Tools {
		def := anthropicToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
		if req.PromptCaching {
			def.CacheControl = &cacheControl{Type: "ephemeral", TTL: cacheTTL}
		}
		out.Tools = append(out.Tools, def)
	}
	return out
}

type anthropicNonStreamResponse struct {
	Content []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	} `json:"content"`
	Usage anthropicUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *Adapter) Complete(ctx context.Context, req application.ChatRequest) (application.ChatResponse, error) {
	var out anthropicNonStreamResponse
	if err := aiutil.DoJSON(ctx, a.Client, http.MethodPost, a.messagesURL(), a.headers(), buildAnthropicRequest(req, false), &out); err != nil {
		return application.ChatResponse{}, err
	}
	if out.Error != nil {
		return application.ChatResponse{}, fmt.Errorf("provider error: %s", out.Error.Message)
	}
	resp := application.ChatResponse{
		Usage: application.ChatUsage{
			InputTokens:  out.Usage.InputTokens,
			OutputTokens: out.Usage.OutputTokens,
			CacheRead:    out.Usage.CacheReadInputTokens,
			CacheWrite:   out.Usage.CacheCreationInputTokens,
		},
	}
	for _, block := range out.Content {
		switch block.Type {
		case "text":
			resp.Content += block.Text
		case "thinking":
			resp.Reasoning += block.Thinking
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, domain.ToolCall{
				ID: block.ID, Name: block.Name, Args: aiutil.RepairToolCallArguments(string(block.Input)),
			})
		}
	}
	return resp, nil
}

func (a *Adapter) Stream(ctx context.Context, req application.ChatRequest, onDelta, onReasoning func(string)) (application.ChatResponse, error) {
	resp, err := aiutil.OpenSSE(ctx, a.Client, a.messagesURL(), a.headers(), buildAnthropicRequest(req, true))
	if err != nil {
		if aiutil.IsStreamUnsupportedError(err) {
			return aiutil.StreamFallbackToComplete(ctx, a, req, onDelta, onReasoning)
		}
		if aiutil.ShouldRetryWithoutImages(err, req.Messages, ctx) {
			stripped := req
			stripped.Messages = aiutil.StripImages(req.Messages)
			return a.Stream(ctx, stripped, onDelta, onReasoning)
		}
		return application.ChatResponse{}, err
	}
	defer resp.Body.Close()

	var result application.ChatResponse
	toolByIndex := map[int]*domain.ToolCall{}
	completed := false
	err = aiutil.ReadSSE(ctx, resp.Body, aiutil.DefaultIdleTimeout, func(ev aiutil.Event) error {
		if ev.Event == "ping" {
			return nil
		}
		var frame struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			ContentBlock *struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content_block"`
			Message *struct {
				Usage anthropicUsage `json:"usage"`
			} `json:"message"`
			Usage *anthropicUsage `json:"usage"`
		}
		if err := aiutil.DecodeData(ev, &frame); err != nil {
			return err
		}
		switch frame.Type {
		case "message_start":
			if frame.Message != nil {
				result.Usage = anthropicUsageToChat(frame.Message.Usage)
			}
		case "content_block_start":
			if frame.ContentBlock != nil && frame.ContentBlock.Type == "tool_use" {
				toolByIndex[frame.Index] = &domain.ToolCall{ID: frame.ContentBlock.ID, Name: frame.ContentBlock.Name}
			}
		case "content_block_delta":
			switch frame.Delta.Type {
			case "text_delta":
				result.Content += frame.Delta.Text
				onDelta(frame.Delta.Text)
			case "thinking_delta":
				result.Reasoning += frame.Delta.Thinking
				if onReasoning != nil {
					onReasoning(frame.Delta.Thinking)
				}
			case "input_json_delta":
				if acc := toolByIndex[frame.Index]; acc != nil {
					acc.Args += frame.Delta.PartialJSON
				}
			}
		case "message_delta":
			if frame.Usage != nil {
				result.Usage = mergeAnthropicUsage(result.Usage, *frame.Usage)
			}
		case "message_stop":
			completed = true
		case "error":
			return fmt.Errorf("provider stream error: %s", ev.Data)
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
			return aiutil.StreamFallbackToComplete(ctx, a, req, onDelta, onReasoning)
		}
		return result, emptyErr
	}
	return result, nil
}

// ListModels fetches the Anthropic model catalog (id, context window, pricing).
func (a *Adapter) ListModels(ctx context.Context, _ string) ([]domain.Model, error) {
	base := strings.TrimRight(a.BaseURL, "/")
	url := base
	if !strings.HasSuffix(url, "/models") {
		if strings.HasSuffix(url, "/v1") {
			url += "/models"
		} else {
			url += "/v1/models"
		}
	}
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			DisplayName   string `json:"display_name"`
			ContextWindow int    `json:"context_window"`
			Pricing       struct {
				Input string `json:"input"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := aiutil.DoJSON(ctx, a.Client, http.MethodGet, url, a.headers(), nil, &out); err != nil {
		return nil, err
	}
	models := make([]domain.Model, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		model := domain.Model{ID: m.ID, Context: m.ContextWindow, Description: m.DisplayName}
		// pricing strings look like "3.0" (USD per MTok)
		if v, err := aiutil.ParseFloat(m.Pricing.Input); err == nil {
			model.InputCost = v
		}
		models = append(models, model)
	}
	return models, nil
}

func anthropicUsageToChat(u anthropicUsage) application.ChatUsage {
	return application.ChatUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CacheRead:    u.CacheReadInputTokens,
		CacheWrite:   u.CacheCreationInputTokens,
	}
}

func mergeAnthropicUsage(current application.ChatUsage, delta anthropicUsage) application.ChatUsage {
	merged := anthropicUsageToChat(delta)
	if merged.InputTokens == 0 {
		merged.InputTokens = current.InputTokens
	}
	if merged.CacheRead == 0 {
		merged.CacheRead = current.CacheRead
	}
	if merged.CacheWrite == 0 {
		merged.CacheWrite = current.CacheWrite
	}
	if merged.OutputTokens == 0 {
		merged.OutputTokens = current.OutputTokens
	}
	return merged
}
