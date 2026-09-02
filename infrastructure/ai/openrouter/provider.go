package openrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nusashell/infrastructure/ai/anthropic"
	"nusashell/infrastructure/ai/compat"
	"nusashell/infrastructure/ai/core"
	"nusashell/infrastructure/ai/openai"
)

const (
	defaultBaseURL          = "https://openrouter.ai/api/v1"
	openRouterRefererHeader = "HTTP-Referer"
	openRouterTitleHeader   = "X-Title"
)

type Config = compat.Config

const (
	APIChat      = "chat"
	APIMessages  = "messages"
	APIResponses = "responses"

	ProviderOptionCacheRetention = "cache_retention"
	ProviderOptionPromptCacheKey = "prompt_cache_key"
	ProviderOptionSessionID      = "session_id"
	ProviderOptionRouting        = "provider"
	structuredOutputsBeta        = "structured-outputs-2025-11-13"
)

func New(cfg Config) (*compat.Provider, error) {
	return compat.New(cfg, compat.Spec{
		Name: "openrouter",
		Endpoint: compat.EndpointSpec{
			BaseURL: defaultBaseURL,
		},
		Auth: compat.AuthSpec{APIKeyRequired: true},
		Headers: compat.HeaderSpec{
			Extra: map[string]string{
				openRouterRefererHeader: "https://github.com/jahrulnr/NusaShell",
				openRouterTitleHeader:   "NusaShell",
			},
			Request: mapHeaders,
		},
		Request: compat.RequestSpec{
			SupportsJSONSchema: true,
			Thinking:           mapThinking,
			ProviderOptions:    mapExtra,
			Messages:           mapMessages,
			CleanSchema:        cleanStrictSchema,
			AllowedProviderOptions: map[string]struct{}{
				ProviderOptionCacheRetention: {},
				ProviderOptionPromptCacheKey: {},
				ProviderOptionSessionID:      {},
				ProviderOptionRouting:        {},
			},
		},
		Response: compat.ResponseSpec{
			ModelFromResponse:         true,
			ContentAsInterface:        true,
			ReasoningFields:           []string{"reasoning_details", "reasoning", "reasoning_content"},
			HasCompletionTokenDetails: true,
		},
		Stream: compat.StreamSpec{
			ReasoningFields: []string{"reasoning_details", "reasoning", "reasoning_content"},
		},
		Features: compat.FeatureSpec{StrictTools: compat.StrictToolsForward},
		Capabilities: func(_ string, caps core.Capabilities) core.Capabilities {
			caps.Tools.StrictSchema = core.SupportPartial
			caps.Thinking.Supported = core.SupportPartial
			caps.Thinking.Disable = core.SupportPartial
			caps.Thinking.Efforts = []string{"minimal", "low", "medium", "high", "xhigh", "max"}
			caps.Thinking.BudgetTokens = core.SupportPartial
			caps.Thinking.IncludeOutput = core.SupportNo
			caps.Cache.Block = core.SupportYes
			caps.Cache.Retention = core.SupportYes
			caps.Usage.CacheWriteTokens = core.SupportYes
			caps.Structured.JSONSchema = core.SupportPartial
			caps.Structured.Strict = core.SupportPartial
			return caps
		},
	})
}

// NewForAPI builds the OpenRouter driver for the selected wire API. Chat uses
// OpenRouter's native compatibility provider; messages and responses delegate
// to the corresponding wire-format implementations while retaining the
// OpenRouter attribution headers.
func NewForAPI(cfg Config, api string) (core.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(api)) {
	case "", APIChat:
		return New(cfg)
	case APIResponses:
		cfgBaseURL := cfg.BaseURL
		if cfgBaseURL == "" {
			cfgBaseURL = defaultBaseURL
		}
		provider, err := openai.New(openai.Config{
			API:            openai.APIResponses,
			APIKey:         cfg.APIKey,
			APIKeyFunc:     cfg.APIKeyFunc,
			BaseURL:        cfgBaseURL,
			HTTPClient:     cfg.HTTPClient,
			Transport:      cfg.Transport,
			Retry:          cfg.Retry,
			UserAgent:      cfg.UserAgent,
			Headers:        openRouterHeaders(cfg.Headers),
			RequestHeaders: mapSessionHeader,
		})
		if err != nil {
			return nil, err
		}
		return namedProvider{Provider: provider, name: "openrouter"}, nil
	case APIMessages:
		cfgBaseURL := cfg.BaseURL
		if cfgBaseURL == "" {
			cfgBaseURL = defaultBaseURL
		}
		provider, err := anthropic.New(anthropic.Config{
			APIKey:         cfg.APIKey,
			APIKeyFunc:     cfg.APIKeyFunc,
			BaseURL:        cfgBaseURL,
			HTTPClient:     cfg.HTTPClient,
			Transport:      cfg.Transport,
			Retry:          cfg.Retry,
			UserAgent:      cfg.UserAgent,
			Headers:        openRouterHeaders(cfg.Headers),
			RequestHeaders: mapSessionHeader,
		})
		if err != nil {
			return nil, err
		}
		return namedProvider{Provider: provider, name: "openrouter"}, nil
	default:
		return nil, fmt.Errorf("openrouter: api must be messages, responses, or chat, got %q", api)
	}
}

type namedProvider struct {
	core.Provider
	name string
}

func (p namedProvider) Name() string {
	return p.name
}

func openRouterHeaders(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers)+2)
	for name, value := range headers {
		out[name] = value
	}
	if strings.TrimSpace(out[openRouterRefererHeader]) == "" {
		out[openRouterRefererHeader] = "https://github.com/jahrulnr/NusaShell"
	}
	if strings.TrimSpace(out[openRouterTitleHeader]) == "" {
		out[openRouterTitleHeader] = "NusaShell"
	}
	return out
}

func mapHeaders(headers http.Header, req *core.Request) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Model)), "anthropic/") {
		return
	}
	for _, tool := range req.Tools {
		if tool.Strict == core.StrictEnabled {
			appendHeaderValue(headers, "x-anthropic-beta", structuredOutputsBeta)
			return
		}
	}
}

// mapSessionHeader carries OpenRouter's per-request session affinity through
// the delegated Anthropic Messages and OpenAI Responses adapters. Those wire
// formats do not expose OpenRouter's session_id body field, but OpenRouter
// accepts the same value in x-session-id.
func mapSessionHeader(headers http.Header, options core.ProviderOptions) {
	if options == nil {
		return
	}
	sessionID, ok := options[ProviderOptionSessionID].(string)
	if ok && strings.TrimSpace(sessionID) != "" {
		headers.Set("x-session-id", sessionID)
	}
}

func appendHeaderValue(headers http.Header, name, value string) {
	current := headers.Get(name)
	for item := range strings.SplitSeq(current, ",") {
		if strings.TrimSpace(item) == value {
			return
		}
	}
	if current == "" {
		headers.Set(name, value)
		return
	}
	headers.Set(name, current+","+value)
}

func Factory(cfg Config) (core.Provider, error) {
	return New(cfg)
}

func mapThinking(thinking *core.Thinking, _ string) (map[string]any, error) {
	if thinking == nil || thinking.Mode == core.ThinkingUnspecified {
		return nil, nil
	}
	if thinking.Mode == core.ThinkingDisabled {
		return map[string]any{"reasoning": map[string]any{"effort": "none"}}, nil
	}
	if thinking.Mode != core.ThinkingEnabled {
		return nil, fmt.Errorf("openrouter: unsupported thinking mode %d", thinking.Mode)
	}
	reasoning := map[string]any{}
	if thinking.BudgetTokens != nil {
		if *thinking.BudgetTokens <= 0 {
			return nil, fmt.Errorf("openrouter: thinking budget_tokens must be positive")
		}
		reasoning["max_tokens"] = *thinking.BudgetTokens
	} else if thinking.Effort != "" {
		effort, err := reasoningEffort(thinking.Effort)
		if err != nil {
			return nil, err
		}
		reasoning["effort"] = effort
	} else {
		reasoning["enabled"] = true
	}
	return map[string]any{"reasoning": reasoning}, nil
}

func reasoningEffort(effort string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(effort))
	switch normalized {
	case "max", "xhigh", "high", "medium", "low", "minimal", "none":
		return normalized, nil
	default:
		return "", fmt.Errorf("openrouter: unsupported reasoning effort %q", effort)
	}
}

func mapExtra(options core.ProviderOptions, body map[string]any, req *core.Request) error {
	for key, value := range options {
		switch key {
		case ProviderOptionCacheRetention:
			retention, ok := value.(string)
			if !ok {
				return fmt.Errorf("openrouter: provider option %q must be string", key)
			}
			cc, err := cacheControl(retention, req.Model)
			if err != nil {
				return err
			}
			if cc != nil {
				body["cache_control"] = cc
			}
		case ProviderOptionSessionID:
			sessionID, ok := value.(string)
			if !ok {
				return fmt.Errorf("openrouter: provider option %q must be string", key)
			}
			if len(sessionID) > 256 {
				return fmt.Errorf("openrouter: provider option %q must be at most 256 characters", key)
			}
			body["session_id"] = sessionID
		case ProviderOptionPromptCacheKey:
			promptCacheKey, ok := value.(string)
			if !ok {
				return fmt.Errorf("openrouter: provider option %q must be string", key)
			}
			body["prompt_cache_key"] = promptCacheKey
		case ProviderOptionRouting:
			routing, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("openrouter: provider option %q must be an object", key)
			}
			copy := make(map[string]any, len(routing)+1)
			for field, fieldValue := range routing {
				copy[field] = fieldValue
			}
			body["provider"] = copy
		default:
			return fmt.Errorf("openrouter: unsupported provider option %q", key)
		}
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == core.ResponseFormatJSONSchema {
		routing, _ := body["provider"].(map[string]any)
		if routing == nil {
			routing = make(map[string]any, 1)
			body["provider"] = routing
		}
		routing["require_parameters"] = true
	}
	return nil
}

func cacheControl(retention, model string) (map[string]any, error) {
	if !strings.HasPrefix(strings.ToLower(model), "anthropic/") {
		return nil, fmt.Errorf("openrouter: cache_retention is only supported for anthropic models")
	}
	switch strings.ToLower(strings.TrimSpace(retention)) {
	case "none":
		return nil, nil
	case "long", "1h":
		return map[string]any{"type": "ephemeral", "ttl": "1h"}, nil
	case "short", "5m":
		return map[string]any{"type": "ephemeral"}, nil
	}
	return nil, fmt.Errorf("openrouter: unsupported cache_retention %q", retention)
}

func mapMessages(messages []core.Message) (any, error) {
	out := make([]map[string]any, 0, len(messages))
	var deferredMedia []map[string]any
	flushDeferredMedia := func() {
		if len(deferredMedia) == 0 {
			return
		}
		out = append(out, map[string]any{"role": "user", "content": deferredMedia})
		deferredMedia = nil
	}
	for i, msg := range messages {
		switch msg.Role {
		case core.RoleSystem, core.RoleUser, core.RoleAssistant:
			// Media is reinjected as a user message, but it must wait until
			// every tool result for the preceding assistant tool_calls message
			// is emitted. Otherwise providers such as DeepSeek see a user
			// message between tool results and reject the request as incomplete.
			flushDeferredMedia()
			content, toolCalls, reasoning, err := mapBlocks(msg.Blocks)
			if err != nil {
				return nil, fmt.Errorf("messages[%d]: %w", i, err)
			}
			converted := map[string]any{"role": string(msg.Role)}
			if content != nil {
				converted["content"] = content
			}
			if len(toolCalls) > 0 {
				converted["tool_calls"] = toolCalls
			}
			for key, value := range reasoning {
				converted[key] = value
			}
			out = append(out, converted)
		case core.RoleTool:
			for _, block := range msg.Blocks {
				result, ok := block.(core.ToolResultBlock)
				if !ok {
					return nil, fmt.Errorf("messages[%d]: tool role only supports ToolResultBlock, got %T", i, block)
				}
				text, media, err := textAndMedia(result.Content)
				if err != nil {
					return nil, err
				}
				out = append(out, map[string]any{"role": "tool", "tool_call_id": result.ToolUseID, "content": text})
				// Chat-compat tool results only carry text. Non-text blocks
				// (image/audio/video from read_media) are reinjected as a
				// follow-up user message so the vision-capable model still
				// sees the media in the next round.
				if len(media) > 0 {
					deferredMedia = append(deferredMedia, media...)
				}
			}
		default:
			return nil, fmt.Errorf("messages[%d]: unsupported role %q", i, msg.Role)
		}
	}
	flushDeferredMedia()
	return out, nil
}

func mapBlocks(blocks []core.Block) (any, []map[string]any, map[string]any, error) {
	parts := make([]map[string]any, 0, len(blocks))
	var text strings.Builder
	var tools []map[string]any
	reasoning := make(map[string]any)
	for _, block := range blocks {
		switch b := block.(type) {
		case core.TextBlock:
			if b.Text == "" {
				continue
			}
			part := map[string]any{"type": "text", "text": b.Text}
			if b.Cache != nil {
				cache, err := mapCache(b.Cache)
				if err != nil {
					return nil, nil, nil, err
				}
				part["cache_control"] = cache
			}
			parts = append(parts, part)
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(b.Text)
		case core.ImageBlock:
			if b.URL == "" {
				return nil, nil, nil, fmt.Errorf("openrouter image blocks require URL")
			}
			image := map[string]any{"url": b.URL}
			if b.Detail != "" {
				image["detail"] = b.Detail
			}
			part := map[string]any{"type": "image_url", "image_url": image}
			if b.Cache != nil {
				cache, err := mapCache(b.Cache)
				if err != nil {
					return nil, nil, nil, err
				}
				part["cache_control"] = cache
			}
			parts = append(parts, part)
		case core.ToolUseBlock:
			tools = append(tools, map[string]any{
				"id":   b.ID,
				"type": "function",
				"function": map[string]any{
					"name":      b.Name,
					"arguments": string(b.Arguments),
				},
			})
		case core.ReasoningBlock:
			if b.Signature != "" || len(b.Redacted) > 0 {
				return nil, nil, nil, fmt.Errorf("OpenRouter Chat does not accept signed or redacted reasoning blocks in message history")
			}
			if err := putReasoning(reasoning, b); err != nil {
				return nil, nil, nil, err
			}
		default:
			return nil, nil, nil, fmt.Errorf("unsupported block %T", block)
		}
	}
	if len(parts) == 0 {
		return nil, tools, reasoning, nil
	}
	if len(parts) == 1 && parts[0]["type"] == "text" {
		if _, hasCache := parts[0]["cache_control"]; !hasCache {
			return text.String(), tools, reasoning, nil
		}
	}
	return parts, tools, reasoning, nil
}

func putReasoning(out map[string]any, block core.ReasoningBlock) error {
	if len(block.Extra) > 0 {
		var details []any
		if err := json.Unmarshal(block.Extra, &details); err != nil {
			return fmt.Errorf("OpenRouter reasoning_details must be a valid JSON array: %w", err)
		}
		out["reasoning_details"] = details
		delete(out, "reasoning")
		return nil
	}
	if block.Text == "" {
		if out["reasoning_details"] != nil {
			return nil
		}
		return fmt.Errorf("OpenRouter reasoning block has empty text with no details — reasoning was received from the provider but is missing on replay (input != output)")
	}
	if current, _ := out["reasoning"].(string); current != "" {
		out["reasoning"] = current + "\n\n" + block.Text
		return nil
	}
	out["reasoning"] = block.Text
	return nil
}

func mapCache(cache *core.CacheControl) (map[string]any, error) {
	if cache == nil {
		return nil, nil
	}
	cacheType := cache.Type
	if cacheType == "" {
		cacheType = core.CacheTypeEphemeral
	}
	if cacheType != core.CacheTypeEphemeral {
		return nil, fmt.Errorf("openrouter: unsupported cache type %q", cache.Type)
	}
	out := map[string]any{"type": cacheType}
	switch cache.TTL {
	case "", core.CacheTTL5m:
	case core.CacheTTL1h:
		out["ttl"] = cache.TTL
	default:
		return nil, fmt.Errorf("openrouter: unsupported cache ttl %q", cache.TTL)
	}
	return out, nil
}

// textAndMedia splits a tool result's content blocks into the text portion
// (which chat-compat tool results can carry) and the image blocks, which the
// caller reinjects as a follow-up user message so a vision-capable model
// still sees the image in the next round. Audio and video blocks are not
// supported in OpenRouter user messages and surface an error.
func textAndMedia(blocks []core.Block) (string, []map[string]any, error) {
	var text strings.Builder
	var media []map[string]any
	for _, block := range blocks {
		switch b := block.(type) {
		case core.TextBlock:
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(b.Text)
		case core.ImageBlock:
			if b.URL == "" {
				return "", nil, fmt.Errorf("openrouter tool result image blocks require URL")
			}
			image := map[string]any{"url": b.URL}
			if b.Detail != "" {
				image["detail"] = b.Detail
			}
			media = append(media, map[string]any{"type": "image_url", "image_url": image})
		default:
			return "", nil, fmt.Errorf("OpenRouter tool results only support text or image content, got %T", block)
		}
	}
	return text.String(), media, nil
}

func cleanStrictSchema(schema core.Schema, strict core.StrictMode) (any, error) {
	var decoded any
	if len(schema) == 0 {
		return map[string]any{"type": "object"}, nil
	}
	if err := json.Unmarshal(schema, &decoded); err != nil {
		return nil, err
	}
	if strict == core.StrictEnabled {
		return addAdditionalPropertiesFalse(decoded), nil
	}
	return decoded, nil
}

func addAdditionalPropertiesFalse(schema any) any {
	switch s := schema.(type) {
	case map[string]any:
		out := make(map[string]any, len(s)+1)
		for key, value := range s {
			out[key] = addAdditionalPropertiesFalse(value)
		}
		if out["type"] == "object" {
			out["additionalProperties"] = false
		}
		return out
	case []any:
		out := make([]any, len(s))
		for i, value := range s {
			out[i] = addAdditionalPropertiesFalse(value)
		}
		return out
	default:
		return schema
	}
}
