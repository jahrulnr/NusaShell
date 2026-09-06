package provider

import (
	"encoding/json"

	"nusashell/domain"
)

// ToolDef is one tool advertised to a chat provider.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ChatMessage is one turn in a ChatRequest.
type ChatMessage struct {
	Role        string // user | assistant | system | tool
	Content     string
	Reasoning   string              // assistant thinking text (persisted, not replayed)
	ToolCalls   []domain.ToolCall   // assistant
	ToolResult  *ToolResult         // tool
	Attachments []domain.Attachment // user only
}

// ToolResult is the model-visible output of one tool call.
type ToolResult struct {
	ToolCallID  string
	Name        string
	Content     string
	Attachments []domain.Attachment // optional image attachments (read_media tool)
}

// ChatRequest is the application-layer completion request, translated to
// core.Request by ToCoreRequest.
type ChatRequest struct {
	Model         string
	System        string
	Messages      []ChatMessage
	Tools         []ToolDef
	PromptCaching bool
	MaxTokens     int
	Effort        string // reasoning effort: "auto" (omit) or a level from the model's SupportedEfforts
	// ProviderRoute pins the upstream provider for this model on
	// aggregator gateways (OpenRouter). Empty means auto/load-balanced;
	// when set, the adapter sends provider.order=[route] with
	// allow_fallbacks=false (fail-closed, session cache stays warm).
	ProviderRoute string
	// Sampling parameters. nil = use provider default. Set from
	// domain.Settings at turn start.
	Temperature      *float64
	TopP             *float64
	TopK             *int
	FrequencyPenalty *float64
	PresencePenalty  *float64
	// PromptCache controls provider-side prompt caching. When non-nil and
	// PromptCaching is true, adapters translate the policy to their native
	// wire format (prompt_cache_key for OpenAI/OpenRouter Chat, cache_control
	// for Anthropic/OpenRouter block caching). OpenRouter also uses the key as
	// its session affinity/grouping identifier.
	PromptCache *PromptCachePolicy
	// ConversationID is the stable ID of the conversation this request
	// belongs to. It is included in the application-generated PromptCache key
	// so requests from separate conversations do not share a cache namespace.
	ConversationID string
	// ReasoningReplay is true when the target upstream requires
	// reasoning_content (Chat Completions) or reasoning items (Responses
	// API) to be echoed back on every assistant message in subsequent
	// turns. Resolved from the model's InterleavedField catalog signal
	// (preferred), a provider/model pattern fallback, an OpenCode host
	// (opencode.ai / Console Go), or upgraded at runtime by the 400
	// classifier. When false, the field is omitted — providers that
	// ignore it (OpenAI, Anthropic) are unaffected.
	ReasoningReplay bool
	// StripParams is the list of request fields the dynamic 400-learning
	// classifier has marked as unsupported for this provider+model. Each
	// entry names a ChatRequest field ("reasoning_effort", "temperature",
	// "top_p", "top_k", "frequency_penalty", "presence_penalty"). The
	// adapter omits the field from the wire request when listed here.
	StripParams []string
	// ToolChoice forces a specific tool when set (provider-native object).
	// Compaction uses this to require summary() instead of a free-text reply.
	ToolChoice any
	// CompactionBlob carries opaque server-side compaction items
	// (OpenAI Responses context_management encrypted_content) that must be
	// replayed as a prefix of the next request's input. Set by the
	// server-side compaction path; the OpenAI Responses adapter forwards it
	// via the "compaction_items" provider option. Empty for the client-side
	// path.
	CompactionBlob string
	// ContextManagement carries server-side context management directives
	// (OpenAI Responses context_management). When non-empty, the adapter
	// forwards it to the wire request so the server can compact context
	// automatically when the threshold is crossed.
	ContextManagement []map[string]any
}

// PromptCachePolicy is the provider-neutral cache intent. Adapters translate
// it to their native wire format. Mirrors the TS AgentPromptCachePolicy.
type PromptCachePolicy struct {
	// Mode: "auto" (default) or "off". "off" disables caching even when
	// the provider supports it.
	Mode string
	// TTL is the provider cache duration: "5m", "1h", or "30m".
	// Anthropic and genuine OpenRouter hosts use cache_control 5m/1h;
	// OpenAI Responses and vanilla OpenAI Chat (including OpenCode) send
	// 30m as prompt_cache_options.ttl. The stored driver is not enough:
	// custom providers default to openrouter but still speak Chat.
	TTL string
	// Key is a stable routing key sent as prompt_cache_key where the selected
	// wire supports it. NusaShell keeps it at 32 ASCII characters and uses a
	// visible agent namespace: "nusashell_cv_<digest>" for normal conversation
	// turns and "nusashell_bg_<digest>" for headless/background learning turns.
	Key string
}

// ChatUsage is token accounting for one provider round.
type ChatUsage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
}

// ContextTokens is the authoritative context fill for a single
// request/response round: the full prompt (uncached input plus any cached or
// cache-written input) plus the generated output. This is what actually
// occupies the model's context window after the round.
//
// Use the LAST round's ContextTokens as the conversation's context usage, not
// the sum of per-round usage: each tool round re-sends the growing history, so
// summing InputTokens across rounds double counts the prompt and can exceed
// the window.
//
// InputTokens is the UNCACHED input for all providers: each provider converter
// (anthropic, openai, compat/openrouter) normalizes at the boundary —
// OpenAI-style adapters subtract cached_tokens from prompt_tokens, Anthropic
// reports input_tokens as uncached already. ContextTokens therefore sums
// InputTokens + CacheRead + CacheWrite + OutputTokens uniformly — no
// per-provider branching needed.
func (u ChatUsage) ContextTokens() int {
	return u.InputTokens + u.CacheRead + u.CacheWrite + u.OutputTokens
}

// ChatResponse is the application-layer completion result.
type ChatResponse struct {
	Content    string
	Reasoning  string
	ToolCalls  []domain.ToolCall
	Usage      ChatUsage
	StopReason string
	// Warnings carries provider-level notices (dropped unsupported content
	// blocks, malformed tool arguments, strict-tool omissions) that would
	// otherwise be silently lost. Empty when the provider reported none.
	Warnings []string
	// CompactionItems carries opaque server-side compaction items (OpenAI
	// Responses context_management). When non-empty, the application layer
	// stores them on the conversation and replays them as a prefix on the
	// next turn. Each entry is the raw JSON of a compaction output item.
	CompactionItems []json.RawMessage
}
