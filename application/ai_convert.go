package application

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// defaultIdleTimeout is the per-chunk stall window for SSE streams. Mirrors
// aiutil.DefaultIdleTimeout — duplicated here to avoid an import cycle
// (application → infrastructure/ai/internal → application).
const defaultIdleTimeout = 60 * time.Second

// ToCoreRequest translates an application ChatRequest into the shared
// core.Request (Blocks-based model). Provider-specific semantics that have
// no core field (prompt caching, learned strip params, reasoning replay)
// are applied here, at the boundary.
func ToCoreRequest(req ChatRequest, kind domain.ProviderKind, openRouter bool) *core.Request {
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
		if kind == domain.ProviderMessages && req.PromptCaching && req.PromptCache != nil && req.PromptCache.Mode != "off" {
			systemBlock.Cache = cacheControlFor(req.PromptCache.TTL)
		}
		if kind == domain.ProviderChat && openRouter && req.PromptCaching && req.PromptCache != nil && req.PromptCache.Mode != "off" {
			systemBlock.Cache = cacheControlFor(req.PromptCache.TTL)
		}
		out.Messages = append(out.Messages, core.Message{Role: core.RoleSystem, Blocks: []core.Block{systemBlock}})
	}
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, chatMessageToCore(m, req))
	}
	for _, t := range req.Tools {
		tool, err := core.NewTool(t.Name, t.Description, t.InputSchema)
		if err != nil {
			continue
		}
		out.Tools = append(out.Tools, tool)
	}
	if req.PromptCache != nil && req.PromptCache.Mode != "off" {
		switch kind {
		case domain.ProviderResponses:
			if req.PromptCache.Key != "" {
				out.ProviderOptions = core.ProviderOptions{"prompt_cache_key": req.PromptCache.Key}
			}
		}
	}
	return out
}

// FromCoreResponse converts a core.Response into application.ChatResponse.
func FromCoreResponse(resp *core.Response) ChatResponse {
	out := ChatResponse{
		Content:    resp.Text(),
		Reasoning:  resp.Reasoning(),
		StopReason: string(resp.FinishReason),
	}
	for _, call := range resp.ToolCalls() {
		out.ToolCalls = append(out.ToolCalls, domain.ToolCall{
			ID:   call.ID,
			Name: call.Name,
			Args: domain.RepairToolCallArguments(string(call.Arguments)),
		})
	}
	out.Usage = ChatUsage{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CacheRead:    resp.Usage.CacheReadTokens,
		CacheWrite:   resp.Usage.CacheWriteTokens,
	}
	for _, w := range resp.Warnings {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %s", w.Code, w.Message))
	}
	return out
}

func cacheControlFor(ttl string) *core.CacheControl {
	if ttl == "1h" {
		return &core.CacheControl{Type: core.CacheTypeEphemeral, TTL: core.CacheTTL1h}
	}
	return &core.CacheControl{Type: core.CacheTypeEphemeral}
}

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

func chatMessageToCore(m ChatMessage, req ChatRequest) core.Message {
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
				Name:      domain.SanitizeToolName(tc.Name),
				Arguments: jsonRaw(tc.Args),
			})
		}
		if req.ReasoningReplay {
			if m.Reasoning != "" {
				blocks = append(blocks, core.ReasoningBlock{Text: m.Reasoning})
			} else {
				blocks = append(blocks, core.ReasoningBlock{Text: domain.ReasoningPlaceholder})
			}
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

func userBlocks(m ChatMessage) []core.Block {
	blocks := make([]core.Block, 0, 1+len(m.Attachments))
	if m.Content != "" {
		blocks = append(blocks, core.TextBlock{Text: m.Content})
	}
	for _, att := range m.Attachments {
		switch att.Type {
		case "text":
			blocks = append(blocks, core.TextBlock{Text: domain.TextAttachmentContent(att)})
		case "image":
			blocks = append(blocks, core.ImageBlock{URL: att.DataURL, Data: dataURLBytes(att.DataURL), MIME: att.MediaType})
		case "audio":
			blocks = append(blocks, core.AudioBlock{Data: dataURLBytes(att.DataURL), MIME: att.MediaType})
		case "video":
			blocks = append(blocks, core.VideoBlock{URL: att.DataURL, MIME: att.MediaType})
		case "file":
			blocks = append(blocks, core.TextBlock{Text: domain.DocumentAttachmentContent(att)})
		}
	}
	return blocks
}

func toolResultBlocks(result *ToolResult) []core.Block {
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

// ProviderContext bundles a core.Provider with the kind and openRouter flag
// needed for request conversion and error mapping. It replaces the old
// AIProvider interface methods (Complete/Stream) with thin wrappers that
// call core.Provider.Chat/Stream + ToCoreRequest/FromCoreResponse/MapCoreError.
type ProviderContext struct {
	Provider   core.Provider
	Kind       domain.ProviderKind
	OpenRouter bool
}

// Complete calls provider.Chat with the converted request and returns the
// converted response. Error mapping is applied so the retry loop keeps working.
func (pc ProviderContext) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	return CompleteViaCore(ctx, pc.Provider, req, pc.Kind, pc.OpenRouter)
}

// Stream calls provider.Stream, sets up the idle watchdog, dispatches
// content/reasoning deltas, and returns the converted response.
func (pc ProviderContext) Stream(ctx context.Context, req ChatRequest, onDelta, onReasoning func(string)) (ChatResponse, error) {
	return StreamViaCore(ctx, pc.Provider, req, pc.Kind, pc.OpenRouter, onDelta, onReasoning)
}

// NewProviderContext builds a ProviderContext from a domain.Provider and a
// core.Provider (typically returned by ProviderFactory).
func NewProviderContext(p *domain.Provider, provider core.Provider) ProviderContext {
	return ProviderContext{
		Provider:   provider,
		Kind:       p.Kind,
		OpenRouter: p.EffectiveDriver() == domain.ProviderDriverOpenRouter || domain.IsOpenRouterHost(p.Kind, p.BaseURL),
	}
}

// MapCoreError translates litellm/core errors into application.UpstreamError
// so the application retry loop keeps classifying by Kind/Temporary/RetryAfter.
func MapCoreError(err error, kind domain.ProviderKind) error {
	if err == nil {
		return nil
	}
	if core.IsStreamIdleError(err) {
		return &UpstreamError{Kind: KindIdleTimeout, Temporary: true, Err: err}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &UpstreamError{Kind: KindConnect, Temporary: false, Err: err}
	}
	var le *core.LiteLLMError
	if errors.As(err, &le) {
		up := &UpstreamError{Err: err}
		if le.StatusCode != 0 {
			up.Kind = KindHTTPStatus
			up.StatusCode = le.StatusCode
			up.Err = fmt.Errorf("provider returned HTTP %d: %w", le.StatusCode, le)
			if le.RetryAfter > 0 {
				up.RetryAfter = time.Duration(le.RetryAfter) * time.Second
			}
		} else if core.IsNetworkError(err) || core.IsTimeoutError(err) {
			up.Kind = KindConnect
		} else {
			up.Kind = KindSSETransport
		}
		up.Temporary = le.Retryable
		if le.StatusCode == 429 && le.RetryAfter == 0 {
			up.Temporary = false
		}
		return up
	}
	return err
}

// CompleteViaCore calls provider.Chat with the converted request and returns
// the converted response. Error mapping is applied so the retry loop keeps
// working.
func CompleteViaCore(ctx context.Context, provider core.Provider, req ChatRequest, kind domain.ProviderKind, openRouter bool) (ChatResponse, error) {
	resp, err := provider.Chat(ctx, ToCoreRequest(req, kind, openRouter))
	if err != nil {
		return ChatResponse{}, MapCoreError(err, kind)
	}
	return FromCoreResponse(resp), nil
}

// StreamViaCore calls provider.Stream, sets up the idle watchdog, dispatches
// content/reasoning deltas via core.HandleWith, and returns the converted
// response. Error mapping is applied.
func StreamViaCore(ctx context.Context, provider core.Provider, req ChatRequest, kind domain.ProviderKind, openRouter bool, onDelta, onReasoning func(string)) (ChatResponse, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := provider.Stream(streamCtx, ToCoreRequest(req, kind, openRouter))
	if err != nil {
		return ChatResponse{}, MapCoreError(err, kind)
	}
	defer stream.Close()
	stream = core.WithStreamIdleWatchdog(stream, cancel, defaultIdleTimeout, string(kind))

	lr, err := core.HandleWith(stream, core.StreamHandler{
		Content: func(text string) error {
			if text != "" && onDelta != nil {
				onDelta(text)
			}
			return nil
		},
		Reasoning: func(text string) error {
			if text != "" && onReasoning != nil {
				onReasoning(text)
			}
			return nil
		},
	})
	if err != nil {
		return ChatResponse{}, MapCoreError(err, kind)
	}
	return FromCoreResponse(lr), nil
}
