// Package ai is the composition root for AI provider adapters. It wires
// the ported litellm provider subpackages (anthropic, openai, openrouter)
// into the application.AIProvider contract via a single Adapter that
// switches on the provider kind.
//
// The litellm providers speak the shared core.Request/Response model
// (Blocks-based). This package owns the boundary translation:
// application.ChatRequest/ChatResponse <-> core.Request/Response, and
// litellm errors -> application.UpstreamError so the application retry
// loop keeps working unchanged.
package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/ai/anthropic"
	"nusashell/infrastructure/ai/core"
	aiutil "nusashell/infrastructure/ai/internal"
	"nusashell/infrastructure/ai/openai"
	"nusashell/infrastructure/ai/openrouter"
)

// Adapter implements application.AIProvider for every supported provider
// kind. The provider-specific litellm adapter is selected per call by Kind.
// OpenRouter is not a separate stored kind — a chat-kind provider whose
// BaseURL is an OpenRouter host gets the OpenRouter adapter (extra headers,
// reasoning_details, cache_retention) automatically.
type Adapter struct {
	ProviderKind domain.ProviderKind
	OpenRouter   bool
	BaseURL      string
	APIKey       string
	Client       *http.Client
}

func (a *Adapter) Kind() domain.ProviderKind { return a.ProviderKind }

// toLitellmRequest translates an application ChatRequest into the shared
// litellm Blocks-based model. Provider-specific semantics that have no
// litellm field (prompt caching, learned strip params, reasoning replay)
// are applied here, at the boundary.
func (a *Adapter) toLitellmRequest(req application.ChatRequest) *core.Request {
	out := &core.Request{
		Model:            req.Model,
		MaxTokens:        intPtrIf(req.MaxTokens > 0, req.MaxTokens),
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		TopK:             req.TopK,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Thinking:         thinkingFromEffort(req.Effort),
	}
	// Learned strip params: null out sampling fields the upstream has
	// rejected with a 400 "Unsupported parameter" so we don't keep
	// sending them and burning a retry.
	if hasParam(req.StripParams, "temperature") {
		out.Temperature = nil
	}
	if hasParam(req.StripParams, "top_p") {
		out.TopP = nil
	}
	if hasParam(req.StripParams, "top_k") {
		out.TopK = nil
	}
	if hasParam(req.StripParams, "frequency_penalty") {
		out.FrequencyPenalty = nil
	}
	if hasParam(req.StripParams, "presence_penalty") {
		out.PresencePenalty = nil
	}
	if hasParam(req.StripParams, "reasoning_effort") {
		out.Thinking = nil
	}

	if req.System != "" {
		systemBlock := core.TextBlock{Text: req.System}
		// Messages (Anthropic) and chat-kind OpenRouter aggregators both
		// cache via block-level cache_control. The Responses wire caches
		// via prompt_cache_key (provider option below); marking the system
		// block here would turn it into an input developer item with a
		// breakpoint instead of instructions.
		if a.ProviderKind == domain.ProviderMessages && req.PromptCaching && req.PromptCache != nil && req.PromptCache.Mode != "off" {
			systemBlock.Cache = cacheControlFor(req.PromptCache.TTL)
		}
		if a.ProviderKind == domain.ProviderChat && a.OpenRouter && req.PromptCaching && req.PromptCache != nil && req.PromptCache.Mode != "off" {
			systemBlock.Cache = cacheControlFor(req.PromptCache.TTL)
		}
		out.Messages = append(out.Messages, core.Message{Role: core.RoleSystem, Blocks: []core.Block{systemBlock}})
	}
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, chatMessageToLitellm(m, req))
	}
	for _, t := range req.Tools {
		tool, err := core.NewTool(t.Name, t.Description, t.InputSchema)
		if err != nil {
			continue // invalid schema: skip the tool rather than failing the turn
		}
		if req.PromptCaching && req.PromptCache != nil && req.PromptCache.Mode != "off" {
			// Anthropic supports cache_control on tool definitions; the
			// openai/responses providers ignore the block cache field.
			out.Tools = append(out.Tools, tool)
			continue
		}
		out.Tools = append(out.Tools, tool)
	}
	if req.PromptCache != nil && req.PromptCache.Mode != "off" {
		switch a.ProviderKind {
		case domain.ProviderResponses:
			if req.PromptCache.Key != "" {
				out.ProviderOptions = core.ProviderOptions{"prompt_cache_key": req.PromptCache.Key}
			}
		}
	}
	return out
}

// cacheControlFor maps a PromptCachePolicy TTL to the Anthropic
// cache_control TTL value. Empty means the 5m default.
func cacheControlFor(ttl string) *core.CacheControl {
	if ttl == "1h" {
		return &core.CacheControl{Type: core.CacheTypeEphemeral, TTL: core.CacheTTL1h}
	}
	return &core.CacheControl{Type: core.CacheTypeEphemeral}
}

// thinkingFromEffort maps the app-level effort to the shared Thinking
// model. "auto" and "" mean "omit on the wire" (provider default);
// "none" explicitly disables thinking; any other level enables it.
func thinkingFromEffort(effort string) *core.Thinking {
	switch effort {
	case "", "auto":
		return nil
	case "none":
		return &core.Thinking{Mode: core.ThinkingDisabled}
	default:
		return &core.Thinking{Mode: core.ThinkingEnabled, Effort: effort}
	}
}

// chatMessageToLitellm converts one application ChatMessage into the
// litellm Blocks-based Message. Attachments map to Image/Audio/Video
// blocks; text and file attachments become descriptive text blocks.
func chatMessageToLitellm(m application.ChatMessage, req application.ChatRequest) core.Message {
	switch m.Role {
	case "user":
		return core.Message{Role: core.RoleUser, Blocks: userBlocks(m)}
	case "assistant":
		blocks := []core.Block{}
		if m.Content != "" {
			blocks = append(blocks, core.TextBlock{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, core.ToolUseBlock{
				ID:        tc.ID,
				Name:      aiutil.SanitizeToolName(tc.Name),
				Arguments: jsonRaw(tc.Args),
			})
		}
		// Reasoning replay: thinking-mode upstreams require the persisted
		// reasoning echoed back on every assistant message in subsequent
		// turns. Anthropic is unaffected (it does not round-trip plain
		// text reasoning), so replay only applies to chat/responses kinds.
		if req.ReasoningReplay && m.Reasoning != "" {
			blocks = append(blocks, core.ReasoningBlock{Text: m.Reasoning})
		}
		return core.Message{Role: core.RoleAssistant, Blocks: blocks}
	case "tool":
		if m.ToolResult == nil {
			return core.Message{Role: core.RoleTool, Blocks: []core.Block{core.ToolResultBlock{ToolUseID: "", Content: []core.Block{core.TextBlock{Text: ""}}}}}
		}
		return core.Message{Role: core.RoleTool, Blocks: []core.Block{
			core.ToolResultBlock{ToolUseID: m.ToolResult.ToolCallID, Content: toolResultBlocks(m.ToolResult)},
		}}
	default:
		return core.Message{Role: core.RoleUser, Blocks: userBlocks(m)}
	}
}

func userBlocks(m application.ChatMessage) []core.Block {
	blocks := make([]core.Block, 0, 1+len(m.Attachments))
	if m.Content != "" {
		blocks = append(blocks, core.TextBlock{Text: m.Content})
	}
	for _, att := range m.Attachments {
		switch att.Type {
		case "text":
			blocks = append(blocks, core.TextBlock{Text: aiutil.TextAttachmentContent(att)})
		case "image":
			blocks = append(blocks, core.ImageBlock{URL: att.DataURL, Data: dataURLBytes(att.DataURL), MIME: att.MediaType})
		case "audio":
			blocks = append(blocks, core.AudioBlock{Data: dataURLBytes(att.DataURL), MIME: att.MediaType})
		case "video":
			blocks = append(blocks, core.VideoBlock{URL: att.DataURL, MIME: att.MediaType})
		case "file":
			// No portable file part in chat wire formats; keep the document
			// visible to the model as a descriptive text block.
			blocks = append(blocks, core.TextBlock{Text: aiutil.DocumentAttachmentContent(att)})
		}
	}
	return blocks
}

func toolResultBlocks(result *application.ToolResult) []core.Block {
	blocks := make([]core.Block, 0, 1+len(result.Attachments))
	if result.Content != "" {
		blocks = append(blocks, core.TextBlock{Text: result.Content})
	}
	for _, att := range result.Attachments {
		switch att.Type {
		case "image":
			blocks = append(blocks, core.ImageBlock{URL: att.DataURL, Data: dataURLBytes(att.DataURL), MIME: att.MediaType})
		case "audio":
			blocks = append(blocks, core.AudioBlock{Data: dataURLBytes(att.DataURL), MIME: att.MediaType})
		case "video":
			blocks = append(blocks, core.VideoBlock{URL: att.DataURL, MIME: att.MediaType})
		}
	}
	return blocks
}

func dataURLBytes(dataURL string) []byte {
	if dataURL == "" {
		return nil
	}
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return nil
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil
	}
	return b
}

func jsonRaw(s string) []byte {
	if s == "" {
		return []byte("{}")
	}
	return []byte(s)
}

// providerFor builds the litellm provider for this adapter's kind.
func (a *Adapter) providerFor() (core.Provider, error) {
	switch {
	case a.ProviderKind == domain.ProviderMessages:
		return anthropic.New(anthropic.Config{APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client})
	case a.ProviderKind == domain.ProviderResponses:
		return openai.New(openai.Config{API: openai.APIResponses, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client})
	case a.ProviderKind == domain.ProviderChat && a.OpenRouter:
		return openrouter.New(openrouter.Config{APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client, APIKeyOptional: a.APIKey == ""})
	case a.ProviderKind == domain.ProviderChat:
		return openai.New(openai.Config{API: openai.APIChat, APIKey: a.APIKey, BaseURL: a.BaseURL, HTTPClient: a.Client})
	default:
		return nil, &application.ErrUnsupportedProvider{Kind: string(a.ProviderKind)}
	}
}

// Complete implements application.AIProvider.
func (a *Adapter) Complete(ctx context.Context, req application.ChatRequest) (application.ChatResponse, error) {
	provider, err := a.providerFor()
	if err != nil {
		return application.ChatResponse{}, err
	}
	resp, err := provider.Chat(ctx, a.toLitellmRequest(req))
	if err != nil {
		return application.ChatResponse{}, mapError(err, a.ProviderKind)
	}
	return chatResponseFrom(resp), nil
}

// Stream implements application.AIProvider. Text and reasoning deltas are
// forwarded to onDelta/onReasoning as they arrive.
func (a *Adapter) Stream(ctx context.Context, req application.ChatRequest, onDelta, onReasoning func(string)) (application.ChatResponse, error) {
	provider, err := a.providerFor()
	if err != nil {
		return application.ChatResponse{}, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := provider.Stream(streamCtx, a.toLitellmRequest(req))
	if err != nil {
		return application.ChatResponse{}, mapError(err, a.ProviderKind)
	}
	defer stream.Close()
	stream = core.WithStreamIdleWatchdog(stream, cancel, aiutil.DefaultIdleTimeout, string(a.ProviderKind))

	lr, err := core.Handle(stream, func(event core.Event) error {
		switch e := event.(type) {
		case core.ContentDelta:
			if e.Text != "" && onDelta != nil {
				onDelta(e.Text)
			}
		case core.ReasoningDelta:
			if e.Text != "" && onReasoning != nil {
				onReasoning(e.Text)
			}
		}
		return nil
	})
	if err != nil {
		return application.ChatResponse{}, mapError(err, a.ProviderKind)
	}
	return chatResponseFrom(lr), nil
}

// mapError translates litellm errors into application.UpstreamError so the
// application retry loop keeps classifying by Kind/Temporary/RetryAfter.
func mapError(err error, kind domain.ProviderKind) error {
	if err == nil {
		return nil
	}
	if core.IsStreamIdleError(err) {
		return &application.UpstreamError{Kind: application.KindIdleTimeout, Temporary: true, Err: err}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &application.UpstreamError{Kind: application.KindConnect, Temporary: false, Err: err}
	}
	var le *core.LiteLLMError
	if errors.As(err, &le) {
		up := &application.UpstreamError{Err: err}
		if le.StatusCode != 0 {
			up.Kind = application.KindHTTPStatus
			up.StatusCode = le.StatusCode
			// Keep the historical "provider returned HTTP <n>: <body>"
			// message shape so error text stays greppable and the retry
			// classifier's body heuristics keep working.
			up.Err = fmt.Errorf("provider returned HTTP %d: %w", le.StatusCode, le)
			if le.RetryAfter > 0 {
				up.RetryAfter = time.Duration(le.RetryAfter) * time.Second
			}
		} else if core.IsNetworkError(err) || core.IsTimeoutError(err) {
			up.Kind = application.KindConnect
		} else {
			up.Kind = application.KindSSETransport
		}
		// Retryability mirrors the historical adapter behavior: a 429 with
		// no Retry-After fails fast (the retry classifier refuses unknown
		// rate-limit windows); a 429 with a window within the cutoff is
		// retryable; 5xx/network/timeout are retryable.
		up.Temporary = le.Retryable
		if le.StatusCode == 429 && le.RetryAfter == 0 {
			up.Temporary = false
		}
		return up
	}
	// Anything else (validation, programming errors) surfaces as-is.
	return err
}

func hasParam(strip []string, name string) bool {
	target := strings.ToLower(name)
	for _, p := range strip {
		if strings.ToLower(p) == target {
			return true
		}
	}
	return false
}

func intPtrIf(cond bool, v int) *int {
	if !cond {
		return nil
	}
	return &v
}

// chatResponseFrom converts a core.Response into application.ChatResponse.
func chatResponseFrom(resp *core.Response) application.ChatResponse {
	out := application.ChatResponse{
		Content:    resp.Text(),
		Reasoning:  resp.Reasoning(),
		StopReason: string(resp.FinishReason),
	}
	for _, call := range resp.ToolCalls() {
		out.ToolCalls = append(out.ToolCalls, domain.ToolCall{
			ID:   call.ID,
			Name: call.Name,
			Args: aiutil.RepairToolCallArguments(string(call.Arguments)),
		})
	}
	// Normalize: providers report input_tokens as the TOTAL prompt
	// (uncached + cached). Subtract cache reads so InputTokens is the
	// UNCACHED input, matching the app-wide telemetry convention.
	uncached := resp.Usage.InputTokens - resp.Usage.CacheReadTokens
	if uncached < 0 {
		uncached = 0
	}
	out.Usage = application.ChatUsage{
		InputTokens:  uncached,
		OutputTokens: resp.Usage.OutputTokens,
		CacheRead:    resp.Usage.CacheReadTokens,
		CacheWrite:   resp.Usage.CacheWriteTokens,
	}
	for _, w := range resp.Warnings {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %s", w.Code, w.Message))
	}
	return out
}

// ListModels implements application.ModelLister.
func (a *Adapter) ListModels(ctx context.Context, apiKey string) ([]domain.Model, error) {
	switch a.ProviderKind {
	case domain.ProviderMessages:
		return listAnthropicModels(ctx, a.BaseURL, a.APIKey, a.Client)
	default:
		headers := map[string]string{}
		if apiKey != "" {
			headers["Authorization"] = "Bearer " + apiKey
		}
		if a.OpenRouter {
			for k, v := range aiutil.OpenRouterAttributionHeaders() {
				headers[k] = v
			}
		}
		return listOpenAIModels(ctx, a.BaseURL, headers, a.Client)
	}
}
