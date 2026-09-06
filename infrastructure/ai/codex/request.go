package codex

import "encoding/json"

// Reasoning mirrors codex-api/src/common.rs Reasoning.
type Reasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// TextControls mirrors codex-api/src/common.rs TextControls.
type TextControls struct {
	Verbosity string `json:"verbosity,omitempty"`
}

// ResponsesAPIRequest mirrors codex-api/src/common.rs ResponsesApiRequest.
// The Codex client always builds it with Store=false and Stream=true; tools
// are passed through as raw wire JSON because tool wire shapes are owned by
// the caller.
type ResponsesAPIRequest struct {
	Model             string            `json:"model"`
	Instructions      string            `json:"instructions,omitempty"`
	Input             []ResponseItem    `json:"input"`
	Tools             []json.RawMessage `json:"tools,omitempty"`
	ToolChoice        string            `json:"tool_choice,omitempty"`
	ParallelToolCalls bool              `json:"parallel_tool_calls,omitempty"`
	Reasoning         *Reasoning        `json:"reasoning,omitempty"`
	Store             bool              `json:"store"`
	Stream            bool              `json:"stream"`
	Include           []string          `json:"include,omitempty"`
	ServiceTier       string            `json:"service_tier,omitempty"`
	PromptCacheKey    string            `json:"prompt_cache_key,omitempty"`
	Text              *TextControls     `json:"text,omitempty"`
	ClientMetadata    map[string]string `json:"client_metadata,omitempty"`
}

// NewResponsesAPIRequest returns a request with the Codex client defaults:
// Store=false (stateless replay through opaque items) and Stream=true.
func NewResponsesAPIRequest(model string, input []ResponseItem) *ResponsesAPIRequest {
	return &ResponsesAPIRequest{
		Model:  model,
		Input:  CloneItems(input),
		Store:  false,
		Stream: true,
	}
}

// CompactionInput is the canonical request body of the legacy unary
// POST /responses/compact endpoint (codex-api/src/endpoint/compact.rs). The
// v2 flow does not use this DTO; it is modeled here for wire fidelity only.
type CompactionInput struct {
	Model             string            `json:"model"`
	Input             []ResponseItem    `json:"input"`
	Instructions      string            `json:"instructions,omitempty"`
	Tools             []json.RawMessage `json:"tools,omitempty"`
	ParallelToolCalls bool              `json:"parallel_tool_calls"`
	Reasoning         *Reasoning        `json:"reasoning,omitempty"`
	ServiceTier       string            `json:"service_tier,omitempty"`
	PromptCacheKey    string            `json:"prompt_cache_key,omitempty"`
	Text              *TextControls     `json:"text,omitempty"`
}

// LegacyCompactPath is the provider-relative path of the legacy unary
// compaction endpoint.
const LegacyCompactPath = "responses/compact"

// CompactResponse is the unary JSON body returned by the legacy compaction
// endpoint. The expected successful output is one or more compaction items
// carrying opaque encrypted_content.
type CompactResponse struct {
	Output []ResponseItem `json:"output"`
}
