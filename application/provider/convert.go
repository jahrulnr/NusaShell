package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

const (
	// defaultIdleTimeout is the per-chunk stall window for ordinary SSE
	// streams. Mirrors aiutil.DefaultIdleTimeout — duplicated here to avoid an
	// import cycle (application → infrastructure/ai/internal → application).
	defaultIdleTimeout = 60 * time.Second
	// codexRemoteCompactionIdleTimeout mirrors Codex upstream's default
	// stream_idle_timeout. Remote compaction can legitimately have a long
	// first-token gap while the server processes a large retained history, so
	// it must not inherit the interactive-turn 60-second watchdog.
	codexRemoteCompactionIdleTimeout = 5 * time.Minute
)

func streamIdleTimeout(req ChatRequest, kind domain.ProviderKind) time.Duration {
	if kind == domain.ProviderCodex && req.RemoteCompaction {
		return codexRemoteCompactionIdleTimeout
	}
	return defaultIdleTimeout
}

// ToCoreRequest translates an application ChatRequest into the shared
// core.Request (Blocks-based model). Provider-specific semantics that have
// no core field (prompt caching, learned strip params, reasoning replay,
// MiniMax reasoning_split) are applied here, at the boundary.
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
	if kind == domain.ProviderCodex && req.ReasoningSummary != "" {
		setProviderOption(out, "reasoning_summary", req.ReasoningSummary)
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
		if emptyAssistantMessage(m) {
			continue
		}
		converted := chatMessageToCore(m, req, kind, openRouter)
		// An assistant entry must keep provider-visible payload after
		// conversion. A provider-specific opaque reasoning payload may be
		// stripped when switching API kinds, and an interrupted thinking
		// stream persists a reasoning-only message: on message-shaped
		// wires (Chat, Messages, Gemini) that serializes to a bare
		// reasoning field, which strict gateways reject ("content or
		// tool_calls must be set"). Item-based wires (Responses, Codex)
		// carry reasoning as a first-class input item, so a
		// reasoning-only entry stays legal there.
		if converted.Role == core.RoleAssistant && !assistantWirePayload(converted.Blocks, kind) {
			continue
		}
		out.Messages = append(out.Messages, converted)
	}
	for _, t := range req.Tools {
		tool, err := core.NewTool(t.Name, t.Description, t.InputSchema)
		if err != nil {
			continue
		}
		// Responses may implicitly close free-form objects when strict is
		// omitted. MCP arguments must accept the discovered tool's fields.
		if t.Name == "mcp_call" && (kind == domain.ProviderResponses || kind == domain.ProviderCodex) {
			tool.Strict = core.StrictDisabled
		}
		out.Tools = append(out.Tools, tool)
	}
	out.ToolChoice = req.ToolChoice
	applyPromptCache(out, req, kind, openRouter)
	// Provider pinning: only aggregator gateways (OpenRouter) understand
	// the provider object; direct providers must never receive it. An
	// empty route means auto/load-balanced — send nothing so OpenRouter
	// uses its default strategy.
	if openRouter && strings.TrimSpace(req.ProviderRoute) != "" && kind == domain.ProviderChat {
		setProviderOption(out, "provider", map[string]any{
			"order":           []string{strings.TrimSpace(req.ProviderRoute)},
			"allow_fallbacks": false,
		})
	}
	if req.CompactionBlob != "" && domain.ReplaysCompactionBlob(kind) {
		setProviderOption(out, "compaction_items", req.CompactionBlob)
		if kind == domain.ProviderCodex {
			prefixMessages := req.CompactionPrefixMessages
			if strings.TrimSpace(req.System) != "" {
				prefixMessages++
			}
			setProviderOption(out, "compaction_prefix_messages", prefixMessages)
		}
	}
	if req.ContextManagement != nil && kind == domain.ProviderResponses {
		setProviderOption(out, "context_management", req.ContextManagement)
	}
	if req.RemoteCompaction && kind == domain.ProviderCodex {
		setProviderOption(out, "compaction_trigger", true)
	}
	if kind == domain.ProviderCodex && strings.TrimSpace(req.ConversationID) != "" {
		setProviderOption(out, "session_id", strings.TrimSpace(req.ConversationID))
	}
	// MiniMax Chat Completions native format embeds thinking in <think>
	// tags inside content unless reasoning_split is on. OpenRouter uses
	// its reasoning object instead; Messages uses Anthropic thinking
	// blocks. A learned 400 can strip the extra field on retry.
	if kind == domain.ProviderChat && !openRouter && domain.RequiresReasoningSplit(req.Model) && !hasParam(req.StripParams, "reasoning_split") {
		setProviderOption(out, "reasoning_split", true)
	}
	return out
}

// FromCoreResponse converts a core.Response into application.ChatResponse.
func FromCoreResponse(resp *core.Response) ChatResponse {
	out := ChatResponse{
		Content:    resp.Text(),
		Reasoning:  resp.Reasoning(),
		Model:      resp.Model,
		Upstream:   resp.Upstream,
		StopReason: string(resp.FinishReason),
	}
	if extra := reasoningExtraFromBlocks(resp.Blocks); len(extra) > 0 {
		out.ReasoningExtra = extra
		out.ReasoningExtraFor = reasoningProducerFamily(resp)
	}
	for _, call := range resp.ToolCalls() {
		toolCall := domain.ToolCall{
			ID:   call.ID,
			Name: call.Name,
			Args: domain.RepairToolCallArguments(string(call.Arguments)),
		}
		// Gemini thought signatures must be replayed with the tool call on the
		// next turn; every other provider leaves Opaque nil.
		if call.Signature != "" {
			toolCall.Opaque = map[string]any{domain.ToolCallOpaqueThoughtSignature: call.Signature}
		}
		out.ToolCalls = append(out.ToolCalls, toolCall)
	}
	out.Usage = ChatUsage{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CacheRead:    resp.Usage.CacheReadTokens,
		CacheWrite:   resp.Usage.CacheWriteTokens,
		TotalTokens:  resp.Usage.TotalTokens,
	}
	for _, w := range resp.Warnings {
		out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %s", w.Code, w.Message))
	}
	if len(resp.CompactionItems) > 0 {
		out.CompactionItems = make([]json.RawMessage, len(resp.CompactionItems))
		for i, item := range resp.CompactionItems {
			out.CompactionItems[i] = append(json.RawMessage(nil), item...)
		}
	}
	return out
}

// reasoningExtraFromBlocks returns the last non-empty ReasoningBlock.Extra
// from a completed response (OpenAI-family encrypted_content replay state,
// Anthropic thinking signatures, Gemini thought signatures). Paired with
// Response.Reasoning() concatenating every block's text, a response with
// multiple signed thinking blocks replays merged text under the last
// signature — a known limitation while Anthropic interleaved thinking
// (multi-block turns) is not configured.
func reasoningExtraFromBlocks(blocks []core.Block) json.RawMessage {
	var extra json.RawMessage
	for _, block := range blocks {
		rb, ok := block.(core.ReasoningBlock)
		if !ok || len(rb.Extra) == 0 {
			continue
		}
		extra = append(json.RawMessage(nil), rb.Extra...)
	}
	return extra
}

// reasoningProducerFamily returns the provider family that produced a
// response, as stamped by the wire adapter. The values are plain lowercase
// family names like "anthropic", "openai", "openrouter", "gemini", or
// "codex". OpenRouter-delegated Messages/Responses adapters keep the inner
// wire's stamp ("anthropic"/"openai"), which is the family that produced —
// and can interpret — the opaque replay payload.
func reasoningProducerFamily(resp *core.Response) string {
	if resp == nil {
		return ""
	}
	if resp.Provider != "" {
		return strings.ToLower(resp.Provider)
	}
	return strings.ToLower(resp.Usage.Provider)
}

// reasoningFamilyForKind maps a request's target provider kind to the
// provider family that interprets its wire format. It is the fallback when
// a response does not report its producer (fakes in tests) and the gate
// that decides whether a persisted ReasoningExtra may reach this kind.
func reasoningFamilyForKind(kind domain.ProviderKind, openRouter bool) string {
	switch kind {
	case domain.ProviderMessages:
		return "anthropic"
	case domain.ProviderChat:
		if openRouter {
			return "openrouter"
		}
		return "openai"
	case domain.ProviderGemini:
		return "gemini"
	case domain.ProviderCodex:
		// Codex keeps its own family even though its wire shape matches
		// Responses: encrypted items issued by the Codex backend are not
		// replayable on the public Responses API, so codex-marked extras
		// are intentionally dropped when a conversation switches to a
		// Responses provider.
		return "codex"
	default:
		// Responses and any other OpenAI-compatible wire.
		return "openai"
	}
}

func cacheControlFor(ttl string) *core.CacheControl {
	cc := &core.CacheControl{Type: core.CacheTypeEphemeral, TTL: core.CacheTTL5m}
	if ttl == "1h" {
		cc.TTL = core.CacheTTL1h
	}
	return cc
}

func applyPromptCache(out *core.Request, req ChatRequest, kind domain.ProviderKind, openRouter bool) {
	if req.PromptCache == nil || req.PromptCache.Mode == "off" {
		return
	}
	if openRouter && req.PromptCache.Key != "" {
		// OpenRouter uses session_id for explicit provider stickiness and for
		// grouping requests in its Logs → Sessions view. Delegated Messages
		// and Responses adapters carry this option to the x-session-id header;
		// native Chat maps it to the documented body field.
		setProviderOption(out, "session_id", req.PromptCache.Key)
	}
	switch kind {
	case domain.ProviderResponses:
		if req.PromptCache.Key != "" {
			setProviderOption(out, "prompt_cache_key", req.PromptCache.Key)
		}
		if req.PromptCache.TTL == "30m" {
			setProviderOption(out, "prompt_cache_options", map[string]any{"ttl": "30m"})
		}
	case domain.ProviderCodex:
		if req.PromptCache.Key != "" {
			setProviderOption(out, "prompt_cache_key", req.PromptCache.Key)
		}
	case domain.ProviderChat:
		if req.PromptCache.Key != "" {
			setProviderOption(out, "prompt_cache_key", req.PromptCache.Key)
		}
		if req.PromptCache.TTL == "30m" {
			setProviderOption(out, "prompt_cache_options", map[string]any{"ttl": "30m"})
		}
	case domain.ProviderGemini:
		// Gemini caching is implicit server-side: there is no cache key,
		// block, or TTL to send, and cachedContentTokenCount is still
		// reported in usage.
	}
}

func setProviderOption(out *core.Request, key string, value any) {
	if out.ProviderOptions == nil {
		out.ProviderOptions = core.ProviderOptions{}
	}
	out.ProviderOptions[key] = value
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

func emptyAssistantMessage(m ChatMessage) bool {
	return m.Role == "assistant" &&
		strings.TrimSpace(m.Content) == "" &&
		strings.TrimSpace(m.Reasoning) == "" &&
		len(m.ToolCalls) == 0 &&
		len(m.ReasoningExtra) == 0
}

// assistantWirePayload reports whether a converted assistant message still
// carries content the target wire accepts on an assistant entry. Chat
// Completions, Anthropic Messages, and Gemini require a text or tool-use
// block — reasoning-only is invalid there (a reasoning field does not
// satisfy "content or tool_calls must be set"). Responses and Codex model
// reasoning as a first-class input item, so a reasoning-only entry is
// legal and keeps encrypted replay state alive.
func assistantWirePayload(blocks []core.Block, kind domain.ProviderKind) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		switch v := b.(type) {
		case core.TextBlock:
			if strings.TrimSpace(v.Text) != "" {
				return true
			}
		case core.ToolUseBlock:
			return true
		}
	}
	return kind == domain.ProviderResponses || kind == domain.ProviderCodex
}

// reasoningExtraForKind returns the opaque ReasoningExtra payload when the
// target provider kind can replay it. Foreign encrypted Codex/Responses
// items must not reach OpenAI Chat or Anthropic Messages history, and a
// payload whose recorded producer family (extraFor) does not match the
// family that interprets the target wire is dropped before the per-kind
// shape rules run. An empty marker is legacy data: the shape rules apply
// unchanged so persisted extras keep their prior behavior.
//
// Three provenance gates run before the shape rules:
//   - Producer instance: an extra may only replay to the provider instance
//     that produced it. Encrypted reasoning is credential/account scoped —
//     replaying one account's blob through another account of the same
//     family leaks state across trust boundaries and often fails upstream.
//     An empty ProviderID on either side is legacy data and does not gate.
//   - Upstream route: when the request pins an OpenRouter upstream
//     (ProviderRoute) and the extra was captured under a different
//     gateway-reported upstream, the payload would cross providers — drop
//     it. Auto-routed requests and extras with no recorded upstream never
//     gate: the upstream is only knowable after the response.
//   - OpenRouter format check: reasoning_details items may declare the
//     upstream wire family via "format"; a known format that provably
//     mismatches the requested model's vendor drops the extra rather than
//     sticky-failing upstream (the open-webui#28240 pattern).
func reasoningExtraForKind(m ChatMessage, req ChatRequest, kind domain.ProviderKind, openRouter bool) json.RawMessage {
	extra := m.ReasoningExtra
	if len(extra) == 0 {
		return nil
	}
	if m.ProviderID != "" && req.ProviderID != "" && m.ProviderID != req.ProviderID {
		return nil
	}
	if openRouter && m.ReasoningExtraUpstream != "" {
		if route := normalizeUpstreamRoute(req.ProviderRoute); route != "" && route != normalizeUpstreamRoute(m.ReasoningExtraUpstream) {
			return nil
		}
	}
	if producer := strings.ToLower(m.ReasoningExtraFor); producer != "" && producer != reasoningFamilyForKind(kind, openRouter) {
		return nil
	}
	switch kind {
	case domain.ProviderResponses, domain.ProviderCodex:
		return extra
	case domain.ProviderGemini:
		// Gemini replays opaque thought-signature state as ReasoningBlock.Extra
		// so a signature returned on a text part survives the round trip.
		return extra
	case domain.ProviderMessages:
		// Anthropic replays thinking signatures and redacted_thinking data
		// through the wire package's opaque Extra payload. Only an extra
		// carrying an actual Anthropic replay value may be forwarded —
		// anything else would become an unsigned thinking block the
		// provider rejects.
		if reasoningExtraHasAnthropicShape(extra) {
			return extra
		}
		return nil
	case domain.ProviderChat:
		if !openRouter {
			return nil
		}
		// OpenRouter Chat replays reasoning_details as a JSON array. Codex
		// encrypted items are objects and would fail openrouter putReasoning.
		var details []any
		if err := json.Unmarshal(extra, &details); err != nil {
			return nil
		}
		if openRouterReasoningVendorMismatch(details, req.Model) {
			return nil
		}
		return extra
	default:
		return nil
	}
}

// normalizeUpstreamRoute canonicalizes an upstream identity for comparison:
// routing slugs (provider.order values like "amazon-bedrock" or
// "deepinfra/fp4") and response-reported upstream names ("Amazon Bedrock")
// use different spellings for the same provider, so both sides reduce to
// lowercase alphanumeric (dropping a "/variant" suffix) before comparing.
func normalizeUpstreamRoute(value string) string {
	s := strings.ToLower(strings.TrimSpace(value))
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// openRouterReasoningFormatVendor maps a reasoning_details item's "format"
// tag to the OpenRouter vendor slug prefix whose upstreams produce — and can
// replay — that payload shape. Confirmed format values come from
// OpenRouter's reasoning docs; unknown or future format strings stay
// unmapped so they forward unchanged rather than guessing.
var openRouterReasoningFormatVendor = map[string]string{
	"openai-responses":          "openai",
	"openai-responses-v1":       "openai",
	"azure-openai-responses-v1": "openai",
	"xai-responses-v1":          "x-ai",
	"anthropic-claude-v1":       "anthropic",
	"google-gemini-v1":          "google",
	"meta-responses-v1":         "meta-llama",
}

// openRouterModelVendor derives the upstream vendor slug prefix from an
// OpenRouter model id ("anthropic/claude-sonnet-4" → "anthropic"). Variant
// suffixes (":free", ":extended") and a leading "models/" are stripped
// first. Routing aliases ("openrouter/auto", "openrouter/free") and bare
// model names have no derivable vendor and return "" — callers must then
// forward the extra unchanged rather than guess.
func openRouterModelVendor(model string) string {
	slug := strings.ToLower(strings.TrimSpace(model))
	slug = strings.TrimPrefix(slug, "models/")
	if i := strings.IndexByte(slug, ':'); i >= 0 {
		slug = slug[:i]
	}
	vendor, _, ok := strings.Cut(slug, "/")
	if !ok || vendor == "openrouter" {
		return ""
	}
	return vendor
}

// openRouterReasoningVendorMismatch reports whether a reasoning_details
// array carries an item whose declared format provably belongs to a
// different upstream vendor than the target model's slug prefix. Items
// with no format, "unknown", or an unmapped format are ignored; an
// underdetermined target vendor forwards the extra unchanged.
func openRouterReasoningVendorMismatch(details []any, model string) bool {
	target := openRouterModelVendor(model)
	if target == "" {
		return false
	}
	for _, item := range details {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		format, _ := entry["format"].(string)
		format = strings.ToLower(strings.TrimSpace(format))
		if format == "" || format == "unknown" {
			continue
		}
		vendor, known := openRouterReasoningFormatVendor[format]
		if known && vendor != target {
			return true
		}
	}
	return false
}

// reasoningExtraHasAnthropicShape reports whether an opaque reasoning extra
// carries a usable Anthropic replay value: a non-empty "signature" or
// "redacted" string. The payload shape is owned by the anthropic wire
// package; this check only inspects key presence so the application layer
// stays independent of its private decoder.
func reasoningExtraHasAnthropicShape(extra json.RawMessage) bool {
	var payload map[string]any
	if err := json.Unmarshal(extra, &payload); err != nil {
		return false
	}
	if signature, ok := payload["signature"].(string); ok && signature != "" {
		return true
	}
	if redacted, ok := payload["redacted"].(string); ok && redacted != "" {
		return true
	}
	return false
}

func chatMessageToCore(m ChatMessage, req ChatRequest, kind domain.ProviderKind, openRouter bool) core.Message {
	switch m.Role {
	case "user":
		return core.Message{Role: core.RoleUser, Blocks: userBlocks(m)}
	case "assistant":
		blocks := []core.Block{}
		// ReasoningBlock must come first: Anthropic requires thinking blocks
		// to be the first block in an assistant message ("If an assistant
		// message contains any thinking blocks, the first block must be
		// `thinking` or `redacted_thinking`"). OpenAI Responses and compat
		// providers don't care about block order — they route by type.
		extra := reasoningExtraForKind(m, req, kind, openRouter)
		hasExtra := len(extra) > 0
		if kind == domain.ProviderMessages {
			// Anthropic accepts only signed thinking blocks: replaying
			// plaintext-only or foreign reasoning produces an unsigned
			// thinking block the provider rejects with a 400. Emit a
			// ReasoningBlock only when the extra carries a usable
			// Anthropic signature or redacted payload — the wire adapter
			// decodes it back onto the block. A signed block is still
			// emitted when visible reasoning text is empty, which the
			// tool-loop protocol requires (an assistant message must start
			// with a thinking block).
			if hasExtra {
				blocks = append(blocks, core.ReasoningBlock{Text: m.Reasoning, Extra: append(json.RawMessage(nil), extra...)})
			}
		} else if m.Reasoning != "" || hasExtra {
			// Always send plaintext reasoning we have from the persisted
			// conversation. Testing against OpenRouter confirms non-reasoning
			// models safely ignore the reasoning field, and reasoning models
			// require it for context continuity. This uses the conversation
			// store as the source of truth — no catalog whitelist or
			// proactive learning needed.
			//
			// ReasoningExtra is attached only when the target kind can replay
			// it. Codex/Responses echo encrypted reasoning items; OpenRouter
			// Chat keeps array-shaped reasoning_details. OpenAI Chat and
			// Anthropic Messages reject or ignore foreign Extra, so a
			// Codex→Chat switch must strip it and keep plaintext only.
			rb := core.ReasoningBlock{Text: m.Reasoning}
			if hasExtra {
				rb.Extra = append(json.RawMessage(nil), extra...)
			}
			blocks = append(blocks, rb)
		} else if req.ReasoningReplay {
			// Models that require reasoning_content to be present on every
			// assistant message (e.g. DeepSeek, GLM with interleaved_field=
			// reasoning_content) get the placeholder sentinel when the prior
			// reasoning text is unavailable. This is gated by ReasoningReplay
			// because sending a placeholder to a model that doesn't require
			// it would inject noise.
			blocks = append(blocks, core.ReasoningBlock{Text: domain.ReasoningPlaceholder})
		}
		if m.Content != "" {
			blocks = append(blocks, core.TextBlock{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			toolUse := core.ToolUseBlock{
				ID:        tc.ID,
				Name:      domain.SanitizeToolName(tc.Name),
				Arguments: jsonRaw(tc.Args),
			}
			// Gemini thought signatures ride along with the tool call they
			// were returned on; a replayed call without its signature is
			// rejected by Gemini 3.
			if signature, ok := tc.Opaque[domain.ToolCallOpaqueThoughtSignature].(string); ok {
				toolUse.Signature = signature
			}
			blocks = append(blocks, toolUse)
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

// Context bundles an AIProvider with the kind and openRouter flag needed
// for request conversion and error mapping. It replaces the old AIProvider
// interface methods (Complete/Stream) with thin wrappers that call
// AIProvider.Chat/Stream + ToCoreRequest/FromCoreResponse/MapCoreError.
type Context struct {
	Provider         AIProvider
	ProviderID       string
	Kind             domain.ProviderKind
	Driver           domain.ProviderDriver
	OpenRouter       bool
	BaseURL          string
	ReasoningSummary string
}

func (pc Context) withProviderOptions(req ChatRequest) ChatRequest {
	if req.ReasoningSummary == "" {
		req.ReasoningSummary = pc.ReasoningSummary
	}
	// Every request leaving through a Context carries the adapter's
	// provider instance so the reasoning-extra instance gate cannot be
	// skipped by a request builder that forgot to stamp ProviderID.
	if req.ProviderID == "" {
		req.ProviderID = pc.ProviderID
	}
	return req
}

// Complete calls provider.Chat with the converted request and returns the
// converted response. Error mapping is applied so the retry loop keeps working.
func (pc Context) Complete(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	return CompleteViaCore(ctx, pc.Provider, pc.withProviderOptions(req), pc.Kind, pc.OpenRouter)
}

// Stream calls provider.Stream, sets up the idle watchdog, dispatches
// content/reasoning deltas, and returns the converted response.
func (pc Context) Stream(ctx context.Context, req ChatRequest, onDelta, onReasoning func(string)) (ChatResponse, error) {
	return StreamViaCoreWithToolActivity(ctx, pc.Provider, pc.withProviderOptions(req), pc.Kind, pc.OpenRouter, onDelta, onReasoning, nil, nil)
}

// ToolUseStart is a provider-side tool-call construction event. Re-exported
// so application/agent can subscribe without importing infrastructure/ai/core.
type ToolUseStart = core.ToolUseStart

// ToolUseDelta is a provider-side tool-call argument-progress event.
type ToolUseDelta = core.ToolUseDelta

// StreamWithToolActivity is the chat-stream variant used by the interactive
// agent. Tool construction callbacks fire while the provider is still
// assembling arguments, before the completed tool call is validated or
// executed. Other callers can keep using Stream when they only need answer
// and reasoning deltas.
func (pc Context) StreamWithToolActivity(ctx context.Context, req ChatRequest, onDelta, onReasoning func(string), onToolStart func(ToolUseStart), onToolDelta func(ToolUseDelta)) (ChatResponse, error) {
	return StreamViaCoreWithToolActivity(ctx, pc.Provider, pc.withProviderOptions(req), pc.Kind, pc.OpenRouter, onDelta, onReasoning, onToolStart, onToolDelta)
}

// NewProviderContext builds a Context from a domain.Provider and an
// AIProvider (typically returned by Factory). The second argument is
// AIProvider rather than core.Provider so root application code does not
// import core.
func NewProviderContext(p *domain.Provider, provider AIProvider) Context {
	return Context{
		Provider:         provider,
		ProviderID:       p.ID,
		Kind:             p.Kind,
		Driver:           p.EffectiveDriver(),
		OpenRouter:       domain.UsesOpenRouterWire(p.Kind, p.EffectiveDriver(), p.BaseURL),
		BaseURL:          p.BaseURL,
		ReasoningSummary: domain.NormalizeReasoningSummary(p.Kind, p.ReasoningSummary),
	}
}

// MapCoreError translates litellm/core errors into domain.ProviderError
// so the application retry loop keeps classifying by Kind/Temporary/RetryAfter.
func MapCoreError(err error, kind domain.ProviderKind) error {
	if err == nil {
		return nil
	}
	if core.IsStreamIdleError(err) {
		return &domain.ProviderError{Kind: domain.KindIdleTimeout, Temporary: true, Err: err}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &domain.ProviderError{Kind: domain.KindConnect, Temporary: false, Err: err}
	}
	var le *core.LiteLLMError
	if errors.As(err, &le) {
		up := &domain.ProviderError{Err: err}
		if le.StatusCode != 0 {
			up.Kind = domain.KindHTTPStatus
			up.StatusCode = le.StatusCode
			up.Err = fmt.Errorf("provider returned HTTP %d: %w", le.StatusCode, le)
			if le.RetryAfter > 0 {
				up.RetryAfter = time.Duration(le.RetryAfter) * time.Second
			}
		} else if core.IsNetworkError(err) || core.IsTimeoutError(err) {
			up.Kind = domain.KindConnect
		} else {
			up.Kind = domain.KindSSETransport
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
func CompleteViaCore(ctx context.Context, provider AIProvider, req ChatRequest, kind domain.ProviderKind, openRouter bool) (ChatResponse, error) {
	resp, err := provider.Chat(ctx, ToCoreRequest(req, kind, openRouter))
	if err != nil {
		return ChatResponse{}, MapCoreError(err, kind)
	}
	return fromCoreResponseForKind(resp, kind, openRouter), nil
}

// fromCoreResponseForKind is FromCoreResponse plus the wire-kind fallback
// for the reasoning-extra producer marker: a provider that does not stamp
// its family on the response (fakes in tests, custom wrappers) still gets
// the family that interprets the target wire recorded, so a captured
// ReasoningExtra always names its producer.
func fromCoreResponseForKind(resp *core.Response, kind domain.ProviderKind, openRouter bool) ChatResponse {
	out := FromCoreResponse(resp)
	if len(out.ReasoningExtra) > 0 && out.ReasoningExtraFor == "" {
		out.ReasoningExtraFor = reasoningFamilyForKind(kind, openRouter)
	}
	return out
}

// StreamViaCoreWithToolActivity calls provider.Stream, sets up the idle
// watchdog, dispatches content/reasoning deltas via core.HandleWith, and
// returns the converted response. Error mapping is applied. It additionally
// supports optional callbacks for
// provider-side tool construction. The callbacks never receive raw argument
// chunks from the application boundary; callers can use them as an activity
// signal while core continues to aggregate and validate the final tool call.
func StreamViaCoreWithToolActivity(ctx context.Context, provider AIProvider, req ChatRequest, kind domain.ProviderKind, openRouter bool, onDelta, onReasoning func(string), onToolStart func(ToolUseStart), onToolDelta func(ToolUseDelta)) (ChatResponse, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := provider.Stream(streamCtx, ToCoreRequest(req, kind, openRouter))
	if err != nil {
		return ChatResponse{}, MapCoreError(err, kind)
	}
	defer stream.Close()
	stream = core.WithStreamIdleWatchdog(stream, cancel, streamIdleTimeout(req, kind), string(kind))

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
		ToolStart: func(event core.ToolUseStart) error {
			if onToolStart != nil {
				onToolStart(event)
			}
			return nil
		},
		ToolDelta: func(event core.ToolUseDelta) error {
			if onToolDelta != nil {
				onToolDelta(event)
			}
			return nil
		},
	})
	if err != nil {
		return ChatResponse{}, MapCoreError(err, kind)
	}
	return fromCoreResponseForKind(lr, kind, openRouter), nil
}
