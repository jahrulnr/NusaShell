package ai

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"nusashell/application"
	"nusashell/domain"
)

// OpenAIAdapter talks to any OpenAI-compatible /chat/completions endpoint.
type OpenAIAdapter struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func (o *OpenAIAdapter) Kind() domain.ProviderKind { return domain.ProviderChat }

func (o *OpenAIAdapter) chatURL() string {
	return joinEndpoint(o.BaseURL, "/chat/completions")
}

func (o *OpenAIAdapter) headers() map[string]string {
	h := map[string]string{}
	if o.APIKey != "" {
		h["Authorization"] = "Bearer " + o.APIKey
	}
	if isOpenRouterURL(o.BaseURL) {
		for k, v := range openRouterAttributionHeaders() {
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
	Model           string          `json:"model"`
	Messages        []openAIMessage `json:"messages"`
	Tools           []openAITool    `json:"tools,omitempty"`
	Stream          bool            `json:"stream"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
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
			content := m.ToolResult.Content
			out = append(out, openAIMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: m.ToolResult.ToolCallID,
			})
		}
	}
	return out
}

func openAIUserContent(message application.ChatMessage) []map[string]any {
	blocks := make([]map[string]any, 0, 1+len(message.Attachments))
	if message.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": message.Content})
	}
	for _, attachment := range message.Attachments {
		switch attachment.Type {
		case "text":
			blocks = append(blocks, map[string]any{"type": "text", "text": textAttachmentContent(attachment)})
		case "image":
			blocks = append(blocks, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": attachment.DataURL},
			})
		case "file":
			// Chat Completions has no portable file-input part. Electron keeps
			// the document visible to the model as a descriptive text block.
			blocks = append(blocks, map[string]any{"type": "text", "text": documentAttachmentContent(attachment)})
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
		Model:     req.Model,
		Messages:  toOpenAIMessages(req),
		Tools:     openAITools(req.Tools),
		Stream:    stream,
		MaxTokens: req.MaxTokens,
	}
	if req.Effort != "" && req.Effort != "auto" {
		r.ReasoningEffort = req.Effort
	}
	return r
}

func (o *OpenAIAdapter) Complete(ctx context.Context, req application.ChatRequest) (application.ChatResponse, error) {
	var out openAIResponse
	if err := doJSON(ctx, o.Client, http.MethodPost, o.chatURL(), o.headers(), buildRequest(req, false), &out); err != nil {
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

func (o *OpenAIAdapter) Stream(ctx context.Context, req application.ChatRequest, onDelta, onReasoning func(string)) (application.ChatResponse, error) {
	resp, err := openSSE(ctx, o.Client, o.chatURL(), o.headers(), buildRequest(req, true))
	if err != nil {
		if isStreamUnsupportedError(err) {
			return streamFallbackToComplete(ctx, o, req, onDelta, onReasoning)
		}
		if shouldRetryWithoutImages(err, req.Messages, ctx) {
			stripped := req
			stripped.Messages = stripImages(req.Messages)
			return o.Stream(ctx, stripped, onDelta, onReasoning)
		}
		return application.ChatResponse{}, err
	}
	defer resp.Body.Close()

	var result application.ChatResponse
	toolAcc := map[int]*domain.ToolCall{}
	completed := false
	err = readSSE(ctx, resp.Body, defaultIdleTimeout, func(ev sseEvent) error {
		if ev.Data == "[DONE]" {
			completed = true
			return nil
		}
		var chunk openAIChunk
		if err := decodeData(ev, &chunk); err != nil {
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
		return result, retryableSSEReadError(err)
	}
	if !completed {
		emptyErr := incompleteSSEError()
		if isIncompleteEmptyStream(emptyErr, result) {
			return streamFallbackToComplete(ctx, o, req, onDelta, onReasoning)
		}
		return result, emptyErr
	}
	indexes := make([]int, 0, len(toolAcc))
	for index := range toolAcc {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		tc := toolAcc[index]
		tc.Args = repairToolCallArguments(tc.Args)
		result.ToolCalls = append(result.ToolCalls, *tc)
	}
	return result, nil
}

func (o *OpenAIAdapter) ListModels(ctx context.Context, apiKey string) ([]domain.Model, error) {
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
	if err := doJSON(ctx, o.Client, http.MethodGet, url, headers, nil, &out); err != nil {
		return nil, err
	}
	models := make([]domain.Model, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		model := domain.Model{ID: m.ID, Context: m.ContextLength, MaxOutput: m.MaxTokens, Description: m.Description, SupportedEfforts: normalizeEfforts(m.Reasoning.SupportedEfforts), DefaultEffort: normalizeEffort(m.Reasoning.DefaultEffort)}
		if v, err := parseFloat(m.Pricing.Prompt); err == nil {
			model.InputCost = v * 1_000_000
		}
		models = append(models, model)
	}
	return models, nil
}

func (o *OpenAIAdapter) responseFromOpenAI(out openAIResponse) (application.ChatResponse, error) {
	if len(out.Choices) == 0 {
		return application.ChatResponse{}, fmt.Errorf("provider returned no choices")
	}
	ch := out.Choices[0]
	resp := application.ChatResponse{
		Content:    deref(ch.Message.Content),
		StopReason: deref(ch.FinishReason),
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
			Args: repairToolCallArguments(tc.Function.Arguments),
		})
	}
	return resp, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
