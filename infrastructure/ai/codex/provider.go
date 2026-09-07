package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nusashell/infrastructure/ai/core"
)

const (
	DefaultInstructions = "You are a helpful coding assistant running inside NusaShell."
	DefaultBaseURL      = "https://chatgpt.com/backend-api/codex"
	DefaultOriginator   = "codex_cli_rs"
	CodexUserAgent      = "codex_cli_rs/0.1.0 (NusaShell; x86_64) generic"
)

// Config configures the ChatGPT Codex Responses transport. APIKey is the
// OAuth access token supplied by the application credential boundary; this
// package does not own token refresh or account selection.
type Config struct {
	APIKey            string
	APIKeyFunc        func(context.Context) (string, error)
	BaseURL           string
	HTTPClient        HTTPClient
	Transport         http.RoundTripper
	Headers           map[string]string
	StreamIdleTimeout time.Duration
	// AccountID is sent as ChatGPT-Account-ID for multi-account ChatGPT
	// sessions. Empty skips the header.
	AccountID string
	// InstallationID is sent as x-codex-installation-id so the Codex
	// backend can route requests from the same install to the same cache
	// shard. Empty skips the header.
	InstallationID string
}

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Provider struct {
	cfg Config
}

func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" && cfg.APIKeyFunc == nil {
		return nil, fmt.Errorf("codex: access token is required")
	}
	if cfg.HTTPClient != nil && cfg.Transport != nil {
		return nil, fmt.Errorf("codex: HTTPClient and Transport are mutually exclusive")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.HTTPClient == nil {
		transport := cfg.Transport
		if transport == nil {
			transport = http.DefaultTransport
		}
		cfg.HTTPClient = &http.Client{Transport: transport}
	}
	return &Provider{cfg: cfg}, nil
}

func Factory(cfg Config) (core.Provider, error) {
	return New(cfg)
}

func (p *Provider) Name() string { return "codex" }

func (p *Provider) Chat(ctx context.Context, req *core.Request) (*core.Response, error) {
	stream, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	return core.Collect(stream)
}

func (p *Provider) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	wire, compaction, err := p.buildRequest(ctx, req)
	if err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, core.NewProviderErrorWithCause(p.Name(), core.ErrorTypeInternal, "codex: marshal responses request", err)
	}
	idleTimeout := p.cfg.StreamIdleTimeout
	streamCtx := ctx
	var cancel context.CancelFunc
	if idleTimeout > 0 {
		streamCtx, cancel = context.WithCancel(ctx)
	}
	open := func() (*ResponsesStream, error) {
		return p.openResponsesStream(streamCtx, body, req.ProviderOptions)
	}
	raw, err := open()
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}

	var stream core.Stream = &providerStream{raw: raw, model: req.Model}
	if compaction {
		stream = &compactionProviderStream{
			raw:      raw,
			model:    req.Model,
			open:     open,
			maxRetry: CompactionV2MaxRetries(MaxRemoteCompactionV2StreamRetries),
		}
	}
	if cancel == nil {
		return stream, nil
	}
	return core.WithStreamIdleWatchdog(stream, cancel, idleTimeout, p.Name()), nil
}

func (p *Provider) openResponsesStream(ctx context.Context, body []byte, options core.ProviderOptions) (*ResponsesStream, error) {
	token, err := p.accessToken(ctx)
	if err != nil {
		return nil, core.WrapValidationError(p.Name(), err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.responsesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, core.NewProviderErrorWithCause(p.Name(), core.ErrorTypeInternal, "codex: create responses request", err)
	}
	p.setHeaders(httpReq, token, options)
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, core.NewNetworkError(p.Name(), "codex: responses stream request failed", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, core.NewHTTPError(p.Name(), resp.StatusCode, string(data))
	}
	return NewResponsesStream(resp.Body), nil
}

func (p *Provider) accessToken(ctx context.Context) (string, error) {
	if p.cfg.APIKeyFunc != nil {
		token, err := p.cfg.APIKeyFunc(ctx)
		if err != nil {
			return "", fmt.Errorf("codex: resolve access token: %w", err)
		}
		if strings.TrimSpace(token) == "" {
			return "", fmt.Errorf("codex: access token is empty")
		}
		return token, nil
	}
	return p.cfg.APIKey, nil
}

func (p *Provider) responsesURL() string {
	base := strings.TrimRight(p.cfg.BaseURL, "/")
	if strings.HasSuffix(base, "/responses") {
		return base
	}
	return base + "/responses"
}

func (p *Provider) setHeaders(req *http.Request, token string, options core.ProviderOptions) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("originator", DefaultOriginator)
	req.Header.Set("User-Agent", CodexUserAgent)
	if p.cfg.AccountID != "" {
		req.Header.Set("ChatGPT-Account-ID", p.cfg.AccountID)
	}
	if p.cfg.InstallationID != "" {
		req.Header.Set(InstallationIDHeader, p.cfg.InstallationID)
	}
	for key, value := range p.cfg.Headers {
		req.Header.Set(key, value)
	}
	if sessionID, ok := stringOption(options, "session_id"); ok {
		req.Header.Set("session-id", sessionID)
		req.Header.Set("thread-id", sessionID)
		req.Header.Set("x-client-request-id", sessionID)
	}
}

func stringOption(options core.ProviderOptions, key string) (string, bool) {
	if options == nil {
		return "", false
	}
	value, ok := options[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return strings.TrimSpace(text), ok && strings.TrimSpace(text) != ""
}

func (p *Provider) buildRequest(ctx context.Context, req *core.Request) (*ResponsesAPIRequest, bool, error) {
	if req == nil {
		return nil, false, fmt.Errorf("request cannot be nil")
	}
	if strings.TrimSpace(req.Model) == "" {
		return nil, false, fmt.Errorf("model is required")
	}
	instructions, input, err := requestInput(req.Messages)
	if err != nil {
		return nil, false, err
	}
	if blob, ok := stringOption(req.ProviderOptions, "compaction_items"); ok {
		prefixMessages, err := intOption(req.ProviderOptions, "compaction_prefix_messages")
		if err != nil {
			return nil, false, err
		}
		if prefixMessages < 0 || prefixMessages > len(req.Messages) {
			return nil, false, fmt.Errorf("compaction_prefix_messages must be between 0 and %d", len(req.Messages))
		}
		prefixInstructions, prefixInput, err := requestInput(req.Messages[:prefixMessages])
		if err != nil {
			return nil, false, err
		}
		suffixInstructions, suffixInput, err := requestInput(req.Messages[prefixMessages:])
		if err != nil {
			return nil, false, err
		}
		instructions = strings.TrimSpace(strings.Join([]string{prefixInstructions, suffixInstructions}, "\n\n"))
		input, err = codexHistoryWithCompaction(prefixInput, suffixInput, blob)
		if err != nil {
			return nil, false, err
		}
	}
	if len(input) == 0 {
		input = []ResponseItem{NewTextMessage(RoleUser, ContentInputText, "...")}
	}
	compaction, err := boolOption(req.ProviderOptions, "compaction_trigger")
	if err != nil {
		return nil, false, err
	}
	if _, ok := req.ProviderOptions["context_management"]; ok {
		return nil, false, fmt.Errorf("context_management is not supported by Codex")
	}

	effectiveInstructions := strings.TrimSpace(instructions)
	if effectiveInstructions == "" {
		effectiveInstructions = DefaultInstructions
	}
	tools := coreTools(req.Tools)
	reasoning, err := codexReasoning(req.Thinking, req.ProviderOptions)
	if err != nil {
		return nil, false, err
	}
	var wire *ResponsesAPIRequest
	if compaction {
		wire = BuildCompactionV2Request(CompactionPrompt{
			Model:        req.Model,
			Instructions: effectiveInstructions,
			Input:        input,
			Tools:        tools,
			Reasoning:    reasoning,
		})
	} else {
		wire = &ResponsesAPIRequest{
			Model:        req.Model,
			Instructions: effectiveInstructions,
			Input:        input,
			Tools:        tools,
			Reasoning:    reasoning,
			Store:        false,
			Stream:       true,
		}
	}
	if wire.Reasoning != nil {
		wire.Include = []string{"reasoning.encrypted_content"}
	}
	if key, ok := stringOption(req.ProviderOptions, "prompt_cache_key"); ok {
		wire.PromptCacheKey = key
	}
	return wire, compaction, nil
}

func intOption(options core.ProviderOptions, key string) (int, error) {
	if options == nil {
		return 0, nil
	}
	value, ok := options[key]
	if !ok {
		return 0, nil
	}
	integer, ok := value.(int)
	if !ok {
		return 0, fmt.Errorf("provider option %q must be an int", key)
	}
	return integer, nil
}

func boolOption(options core.ProviderOptions, key string) (bool, error) {
	if options == nil {
		return false, nil
	}
	value, ok := options[key]
	if !ok {
		return false, nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("provider option %q must be a bool", key)
	}
	return boolean, nil
}

func codexHistoryWithCompaction(prefix, suffix []ResponseItem, blob string) ([]ResponseItem, error) {
	var rawItems []json.RawMessage
	if err := json.Unmarshal([]byte(blob), &rawItems); err != nil {
		return nil, fmt.Errorf("codex: compaction items must be a JSON array: %w", err)
	}
	if len(rawItems) != 1 {
		return nil, fmt.Errorf("codex: compaction items must contain exactly one item, got %d", len(rawItems))
	}
	var compaction ResponseItem
	if err := json.Unmarshal(rawItems[0], &compaction); err != nil {
		return nil, fmt.Errorf("codex: decode compaction item: %w", err)
	}
	if !compaction.IsCompaction() {
		return nil, fmt.Errorf("codex: compaction items contain %q, want compaction", compaction.Type)
	}
	history := BuildRetainedHistory(prefix, compaction)
	return append(history, suffix...), nil
}

func requestInput(messages []core.Message) (string, []ResponseItem, error) {
	var instructions []string
	var input []ResponseItem
	// Codex function_call_output only carries a string. Non-text blocks
	// (image/audio/video from tool results, e.g. generate_image or
	// read_media) are deferred and reinjected as a follow-up user message
	// with input_image/input_audio items so the vision-capable model still
	// sees the media in the next round. This mirrors the OpenAI Responses
	// adapter's deferredMedia pattern; without it, a tool result carrying an
	// ImageBlock fails with "block core.ImageBlock cannot be represented as
	// text".
	var deferred []ContentItem
	flushDeferred := func() {
		if len(deferred) == 0 {
			return
		}
		input = append(input, ResponseItem{Type: ItemTypeMessage, Role: RoleUser, Content: deferred})
		deferred = nil
	}
	for index, message := range messages {
		if message.Role == core.RoleSystem {
			text, err := blocksText(message.Blocks)
			if err != nil {
				return "", nil, fmt.Errorf("codex: system message[%d]: %w", index, err)
			}
			if strings.TrimSpace(text) != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		items, media, err := messageItems(message)
		if err != nil {
			return "", nil, fmt.Errorf("codex: message[%d]: %w", index, err)
		}
		// Media must wait until every tool result for the preceding
		// assistant tool_calls batch is emitted, otherwise the provider sees
		// a user message between tool results and rejects the request.
		if message.Role == core.RoleUser || message.Role == core.RoleAssistant {
			flushDeferred()
		}
		input = append(input, items...)
		if len(media) > 0 {
			deferred = append(deferred, media...)
		}
	}
	flushDeferred()
	return strings.Join(instructions, "\n"), input, nil
}

func messageItems(message core.Message) ([]ResponseItem, []ContentItem, error) {
	switch message.Role {
	case core.RoleUser:
		content, err := contentItems(message.Blocks, ContentInputText)
		if err != nil {
			return nil, nil, err
		}
		return []ResponseItem{{Type: ItemTypeMessage, Role: RoleUser, Content: content}}, nil, nil
	case core.RoleAssistant:
		var out []ResponseItem
		for _, block := range message.Blocks {
			switch value := block.(type) {
			case core.ReasoningBlock:
				item, err := reasoningItem(value)
				if err != nil {
					return nil, nil, err
				}
				out = append(out, item)
			case core.TextBlock:
				if strings.TrimSpace(value.Text) != "" {
					out = append(out, ResponseItem{
						Type:    ItemTypeMessage,
						Role:    RoleAssistant,
						Content: []ContentItem{{Type: ContentOutputText, Text: value.Text}},
					})
				}
			case core.ToolUseBlock:
				out = append(out, ResponseItem{
					Type:      ItemTypeFunctionCall,
					CallID:    value.ID,
					Name:      value.Name,
					Arguments: string(value.Arguments),
				})
			}
		}
		return out, nil, nil
	case core.RoleTool:
		var out []ResponseItem
		var media []ContentItem
		for _, block := range message.Blocks {
			result, ok := block.(core.ToolResultBlock)
			if !ok {
				continue
			}
			text, toolMedia, err := toolResultParts(result.Content)
			if err != nil {
				return nil, nil, err
			}
			output, err := json.Marshal(text)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, ResponseItem{
				Type:   ItemTypeFunctionCallOutput,
				CallID: result.ToolUseID,
				Output: output,
			})
			if len(toolMedia) > 0 {
				media = append(media, toolMedia...)
			}
		}
		return out, media, nil
	default:
		return nil, nil, fmt.Errorf("unsupported message role %q", message.Role)
	}
}

// toolResultParts splits a tool result's content into the text payload
// (serialized as the function_call_output string) and any media blocks that
// must be reinjected as user message items. Media never fails the request;
// only non-text, non-media blocks are rejected.
func toolResultParts(blocks []core.Block) (string, []ContentItem, error) {
	var parts []string
	var media []ContentItem
	for _, block := range blocks {
		switch value := block.(type) {
		case core.TextBlock:
			parts = append(parts, value.Text)
		case core.ReasoningBlock:
			parts = append(parts, value.Text)
		case core.ImageBlock:
			url := value.URL
			if url == "" && len(value.Data) > 0 {
				url = "data:" + value.MIME + ";base64," + base64.StdEncoding.EncodeToString(value.Data)
			}
			if url != "" {
				media = append(media, ContentItem{Type: ContentInputImage, ImageURL: url, Detail: value.Detail})
			}
		case core.AudioBlock:
			if len(value.Data) > 0 {
				media = append(media, ContentItem{
					Type:     ContentInputAudio,
					AudioURL: "data:" + value.MIME + ";base64," + base64.StdEncoding.EncodeToString(value.Data),
				})
			}
		case core.VideoBlock:
			if value.URL != "" {
				media = append(media, ContentItem{Type: ContentInputImage, ImageURL: value.URL})
			}
		default:
			return "", nil, fmt.Errorf("block %T cannot be represented as text", block)
		}
	}
	return strings.Join(parts, ""), media, nil
}

func contentItems(blocks []core.Block, textType string) ([]ContentItem, error) {
	var out []ContentItem
	for _, block := range blocks {
		switch value := block.(type) {
		case core.TextBlock:
			if value.Text != "" {
				out = append(out, ContentItem{Type: textType, Text: value.Text})
			}
		case core.ImageBlock:
			url := value.URL
			if url == "" && len(value.Data) > 0 {
				url = "data:" + value.MIME + ";base64," + base64.StdEncoding.EncodeToString(value.Data)
			}
			if url != "" {
				out = append(out, ContentItem{Type: ContentInputImage, ImageURL: url, Detail: value.Detail})
			}
		case core.AudioBlock:
			url := ""
			if len(value.Data) > 0 {
				url = "data:" + value.MIME + ";base64," + base64.StdEncoding.EncodeToString(value.Data)
			}
			if url != "" {
				out = append(out, ContentItem{Type: ContentInputAudio, AudioURL: url})
			}
		case core.VideoBlock:
			if value.URL != "" {
				out = append(out, ContentItem{Type: ContentInputImage, ImageURL: value.URL})
			}
		}
	}
	return out, nil
}

func blocksText(blocks []core.Block) (string, error) {
	var parts []string
	for _, block := range blocks {
		switch value := block.(type) {
		case core.TextBlock:
			parts = append(parts, value.Text)
		case core.ReasoningBlock:
			parts = append(parts, value.Text)
		default:
			return "", fmt.Errorf("block %T cannot be represented as text", block)
		}
	}
	return strings.Join(parts, ""), nil
}

func reasoningItem(block core.ReasoningBlock) (ResponseItem, error) {
	if len(block.Extra) > 0 {
		var item ResponseItem
		if err := json.Unmarshal(block.Extra, &item); err != nil {
			return ResponseItem{}, fmt.Errorf("decode reasoning replay: %w", err)
		}
		if item.Type == ItemTypeReasoning {
			return item, nil
		}
	}
	item := ResponseItem{Type: ItemTypeReasoning}
	if strings.TrimSpace(block.Text) != "" {
		item.Summary = []ReasoningSummary{{Type: "summary_text", Text: block.Text}}
	}
	return item, nil
}

func codexReasoning(thinking *core.Thinking, options core.ProviderOptions) (*Reasoning, error) {
	summary := "auto"
	if value, ok := options["reasoning_summary"]; ok {
		configured, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("codex: reasoning_summary must be a string")
		}
		summary = strings.ToLower(strings.TrimSpace(configured))
		switch summary {
		case "auto", "concise", "detailed", "none":
		default:
			return nil, fmt.Errorf("codex: reasoning_summary must be one of auto, concise, detailed, none, got %q", summary)
		}
	}
	if thinking != nil && thinking.Mode == core.ThinkingDisabled {
		return nil, nil
	}
	effort := "low"
	if thinking != nil && strings.TrimSpace(thinking.Effort) != "" {
		effort = thinking.Effort
	}
	if summary == "none" {
		summary = ""
	}
	return &Reasoning{Effort: effort, Summary: summary}, nil
}

func coreTools(tools []core.Tool) []json.RawMessage {
	if len(tools) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = core.Schema(`{"type":"object","properties":{}}`)
		}
		wire := map[string]json.RawMessage{
			"type":       json.RawMessage(`"function"`),
			"name":       json.RawMessage(mustJSON(name)),
			"parameters": append(json.RawMessage(nil), parameters...),
		}
		if tool.Description != "" {
			wire["description"] = json.RawMessage(mustJSON(tool.Description))
		}
		raw, err := json.Marshal(wire)
		if err == nil {
			out = append(out, raw)
		}
	}
	return out
}

func mustJSON(value string) []byte {
	raw, _ := json.Marshal(value)
	return raw
}
