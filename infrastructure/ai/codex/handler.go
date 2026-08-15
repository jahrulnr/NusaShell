// Package codex implements the application.AIProvider port for the
// OpenAI Codex backend (chatgpt.com/backend-api/codex/responses).
//
// The Codex backend speaks the Responses API wire format but requires
// request normalization, mandatory headers, and OAuth-based authentication
// that differ from the standard OpenAI Responses API. This adapter wraps
// the responses.Adapter pattern with Codex-specific transformations.
//
// Request normalization is ported from 9router's codex.js executor
// (MIT licensed, https://github.com/decolua/9router).
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	aiutil "nusashell/infrastructure/ai/internal"
)

// DefaultInstructions is injected when the caller provides no system prompt.
// Codex backend rejects empty instructions. Ported from 9router
// codexInstructions.js.
const DefaultInstructions = "You are a helpful coding assistant running inside NusaShell."

// DefaultBaseURL is the Codex backend Responses API endpoint.
const DefaultBaseURL = "https://chatgpt.com/backend-api/codex"

// DefaultOriginator identifies the client to the Codex backend. Matches
// the official Codex CLI.
const DefaultOriginator = "codex_cli_rs"

// serverIDPattern matches server-generated item IDs (rs_, fc_, resp_, msg_)
// that Codex /responses cannot resolve when store=false.
var serverIDPattern = regexp.MustCompile(`^(rs|fc|resp|msg)_`)

// hostedToolTypes are tool types that Codex executes server-side and must
// pass through the allowlist.
var hostedToolTypes = map[string]bool{
	"image_generation": true, "web_search": true, "web_search_preview": true,
	"file_search": true, "computer": true, "computer_use_preview": true,
	"code_interpreter": true, "mcp": true, "local_shell": true, "tool_search": true,
}

// Adapter talks to the OpenAI Codex backend with SSE streaming, function
// calling, and OAuth-based authentication. It wraps the Responses API wire
// format with Codex-specific request normalization and headers.
type Adapter struct {
	// BaseURL defaults to DefaultBaseURL if empty.
	BaseURL string
	// AccessToken is the OAuth access_token from the Codex OAuth flow.
	AccessToken string
	// AccountID is the ChatGPT account ID extracted from the id_token JWT.
	// Sent as the ChatGPT-Account-ID header.
	AccountID string
	// Originator identifies the client type. Defaults to DefaultOriginator.
	Originator string
	// Client is the HTTP client for API calls.
	Client *http.Client
}

func (a *Adapter) Kind() domain.ProviderKind { return domain.ProviderCodex }

func (a *Adapter) baseURL() string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (a *Adapter) responsesURL() string {
	return aiutil.JoinEndpoint(a.baseURL(), "/responses")
}

func (a *Adapter) originator() string {
	if a.Originator != "" {
		return a.Originator
	}
	return DefaultOriginator
}

func (a *Adapter) headers() map[string]string {
	h := map[string]string{
		"Authorization": "Bearer " + a.AccessToken,
		"originator":    a.originator(),
	}
	if a.AccountID != "" {
		h["ChatGPT-Account-ID"] = a.AccountID
	}
	return h
}

// ---- wire types (Codex uses Responses API shape) ----

type codexInputItem struct {
	Role    string          `json:"role,omitempty"`
	Type    string          `json:"type,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Args    string          `json:"arguments,omitempty"`
	Output  *string         `json:"output,omitempty"`
}

type codexToolDef struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type codexRequest struct {
	Model          string           `json:"model"`
	Instructions   string           `json:"instructions,omitempty"`
	Input          []codexInputItem `json:"input,omitempty"`
	Tools          []codexToolDef   `json:"tools,omitempty"`
	Stream         bool             `json:"stream"`
	Store          bool             `json:"store"`
	Reasoning      *codexReasoning  `json:"reasoning,omitempty"`
	Include        []string         `json:"include,omitempty"`
	PromptCacheKey string           `json:"prompt_cache_key,omitempty"`
}

type codexUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

type codexNonStreamResponse struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"summary"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
		Args   string `json:"arguments"`
	} `json:"output"`
	Usage codexUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ---- request building + normalization (ATM from 9router codex.js) ----

func toCodexInput(msgs []application.ChatMessage) []codexInputItem {
	var out []codexInputItem
	for _, m := range msgs {
		switch m.Role {
		case "user":
			content := aiutil.StrJSON(m.Content)
			if len(m.Attachments) > 0 {
				content = aiutil.MustJSON(codexUserContent(m))
			}
			out = append(out, codexInputItem{Role: "user", Content: content})
		case "system":
			// Codex uses role=developer for system prompts (keeps content
			// in cacheable prefix). Ported from 9router convertSystemToDeveloperRole.
			out = append(out, codexInputItem{Role: "developer", Content: aiutil.StrJSON(m.Content)})
		case "assistant":
			if m.Content != "" {
				out = append(out, codexInputItem{Role: "assistant", Content: aiutil.StrJSON(m.Content)})
			}
			for _, tc := range m.ToolCalls {
				out = append(out, codexInputItem{
					Type: "function_call", CallID: tc.ID, Name: tc.Name, Args: tc.Args,
				})
			}
		case "tool":
			output := m.ToolResult.Content
			out = append(out, codexInputItem{
				Type:   "function_call_output",
				CallID: m.ToolResult.ToolCallID,
				Output: &output,
			})
		}
	}
	// Strip server-generated item IDs (rs_/fc_/resp_/msg_) — Codex
	// /responses can't resolve them when store=false. Ported from
	// 9router stripStoredItemReferences.
	out = stripStoredItemReferences(out)
	return out
}

func codexUserContent(message application.ChatMessage) []map[string]any {
	blocks := make([]map[string]any, 0, 1+len(message.Attachments))
	if message.Content != "" {
		blocks = append(blocks, map[string]any{"type": "input_text", "text": message.Content})
	}
	for _, attachment := range message.Attachments {
		switch attachment.Type {
		case "text":
			blocks = append(blocks, map[string]any{"type": "input_text", "text": aiutil.TextAttachmentContent(attachment)})
		case "image":
			blocks = append(blocks, map[string]any{"type": "input_image", "image_url": attachment.DataURL})
		case "file":
			blocks = append(blocks, map[string]any{
				"type": "input_file", "file_data": attachment.DataURL, "filename": attachment.Name,
			})
		}
	}
	return blocks
}

// stripStoredItemReferences removes server-generated item IDs from the
// input array. Codex backend with store=false cannot resolve references
// to items from previous responses. ATM from 9router stripStoredItemReferences.
func stripStoredItemReferences(items []codexInputItem) []codexInputItem {
	var out []codexInputItem
	for _, item := range items {
		// Drop item_reference types entirely
		if item.Type == "item_reference" {
			continue
		}
		// We don't carry server IDs in our wire types, but if any leak
		// through (e.g. from a stored conversation), the pattern check
		// would catch them. Our codexInputItem doesn't have an ID field,
		// so this is a no-op for now — kept for parity with 9router.
		out = append(out, item)
	}
	return out
}

// normalizeCodexTools flattens Chat-Completions tool shape into Responses
// flat format and filters unsupported tool types. ATM from 9router
// normalizeCodexTools.
func normalizeCodexTools(tools []application.ToolDef) []codexToolDef {
	var out []codexToolDef
	for _, t := range tools {
		// NusaShell tools are always function type; pass through directly.
		// Hosted tool types (web_search, code_interpreter, etc.) would
		// need separate handling if NusaShell ever exposes them.
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if len(name) > 128 {
			name = name[:128]
		}
		params := t.InputSchema
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, codexToolDef{
			Type:        "function",
			Name:        name,
			Description: t.Description,
			Parameters:  params,
		})
	}
	return out
}

func buildCodexRequest(req application.ChatRequest) codexRequest {
	input := toCodexInput(req.Messages)
	// Ensure input is non-empty (Codex API rejects empty input)
	if len(input) == 0 {
		placeholder := "..."
		input = []codexInputItem{{
			Role: "user", Content: aiutil.StrJSON(placeholder),
		}}
	}

	// If the conversation carries a server-side compaction blob from a
	// previous CompactServer call, prepend it as a Compaction input item.
	// The Codex backend uses this encrypted blob to restore compacted
	// context without re-sending the full history. The blob is passed via
	// ChatRequest.System as a JSON-encoded prefix (see agent_round.go
	// buildSystemPrompt) — but since it's opaque, we extract it from a
	// dedicated field if the caller sets one.
	// Note: the blob injection is handled in CompactServer's buildCompactInput
	// for the compact endpoint itself. For regular /responses requests,
	// the blob would need to be injected here if we want subsequent turns
	// to benefit from it. That requires plumbing CompactionBlob through
	// ChatRequest, which is a larger change. For now, the blob is only
	// used within the compact endpoint round-trip.

	instructions := req.System
	if strings.TrimSpace(instructions) == "" {
		instructions = DefaultInstructions
	}

	out := codexRequest{
		Model:        req.Model,
		Instructions: instructions,
		Input:        input,
		Tools:        normalizeCodexTools(req.Tools),
		Stream:       true,  // Codex requires streaming
		Store:        false, // Codex requirement
	}

	// Reasoning: Codex requires reasoning with summary. Default to "low"
	// when not specified (matches 9router default).
	effort := req.Effort
	if effort == "" || effort == "auto" {
		effort = "low"
	}
	out.Reasoning = &codexReasoning{Effort: effort, Summary: "auto"}

	// Include reasoning encrypted content (required by Codex backend for
	// reasoning models). ATM from 9router.
	if effort != "none" {
		out.Include = []string{"reasoning.encrypted_content"}
	}

	// Prompt cache key — Codex only supports prompt_cache_key, NOT
	// prompt_cache_options or prompt_cache_retention. Verified from
	// Codex Rust source (ResponsesApiRequest struct).
	if req.PromptCache != nil && req.PromptCache.Mode != "off" && req.PromptCache.Key != "" {
		out.PromptCacheKey = req.PromptCache.Key
	}

	return out
}

// ---- AIProvider implementation ----

func (a *Adapter) Complete(ctx context.Context, req application.ChatRequest) (application.ChatResponse, error) {
	// Codex requires streaming; non-streaming Complete is emulated by
	// consuming the stream internally.
	return a.Stream(ctx, req, nil, nil)
}

func (a *Adapter) Stream(ctx context.Context, req application.ChatRequest, onDelta, onReasoning func(string)) (application.ChatResponse, error) {
	resp, err := aiutil.OpenSSE(ctx, a.Client, a.responsesURL(), a.headers(), buildCodexRequest(req))
	if err != nil {
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
	streamErr := error(nil)
	completed := false
	readErr := aiutil.ReadSSE(ctx, resp.Body, aiutil.DefaultIdleTimeout, func(ev aiutil.Event) error {
		if ev.Data == "[DONE]" {
			completed = true
			return nil
		}
		var frame struct {
			Type        string `json:"type"`
			OutputIndex int    `json:"output_index"`
			Delta       string `json:"delta"`
			Item        *struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			} `json:"item"`
			Response *struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
				Usage codexUsage `json:"usage"`
			} `json:"response"`
		}
		if err := aiutil.DecodeData(ev, &frame); err != nil {
			return err
		}
		switch frame.Type {
		case "response.output_text.delta":
			result.Content += frame.Delta
			if onDelta != nil {
				onDelta(frame.Delta)
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			result.Reasoning += frame.Delta
			if onReasoning != nil {
				onReasoning(frame.Delta)
			}
		case "response.output_item.added":
			if frame.Item != nil && frame.Item.Type == "function_call" {
				toolByIndex[frame.OutputIndex] = &domain.ToolCall{ID: frame.Item.CallID, Name: frame.Item.Name}
			}
		case "response.function_call_arguments.delta":
			if acc := toolByIndex[frame.OutputIndex]; acc != nil {
				acc.Args += frame.Delta
			}
		case "response.completed":
			completed = true
			if frame.Response != nil {
				result.Usage = application.ChatUsage{
					InputTokens:  frame.Response.Usage.InputTokens,
					OutputTokens: frame.Response.Usage.OutputTokens,
					CacheRead:    frame.Response.Usage.InputTokensDetails.CachedTokens,
					CacheWrite:   frame.Response.Usage.InputTokensDetails.CacheWriteTokens,
				}
			}
		case "response.failed":
			msg := "provider stream failed"
			if frame.Response != nil && frame.Response.Error != nil {
				msg = frame.Response.Error.Message
			}
			streamErr = fmt.Errorf("%s", msg)
			return streamErr
		}
		return nil
	})
	if readErr != nil {
		return result, aiutil.RetryableSSEReadError(readErr)
	}
	if streamErr != nil {
		return result, streamErr
	}
	// Finalize accumulated tool calls
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
		return result, aiutil.IncompleteSSEError()
	}
	return result, nil
}

// ListModels fetches the Codex model catalog by spawning a Codex app-server
// subprocess and calling the model/list JSON-RPC method. The catalog is
// account-aware: some models may be gated by the user's ChatGPT subscription
// tier (Plus, Pro, etc.). The subprocess uses the user's Codex credentials
// from ~/.codex/auth.json.
func (a *Adapter) ListModels(ctx context.Context, _ string) ([]domain.Model, error) {
	return listModelsViaSubprocess(ctx)
}

// ---- ServerCompactor implementation ----

// CompactServer implements application.ServerCompactor. It compacts the
// conversation by spawning a Codex app-server subprocess and sending
// thread/compact/start via JSON-RPC.
//
// This is the only working way to trigger Codex's server-side compaction.
// The HTTP /responses/compact endpoint is internal to the Codex CLI and
// returns 404 to direct HTTP clients. The app-server JSON-RPC protocol
// is the public interface.
//
// The encrypted compaction blob is stored internally by Codex and is NOT
// exposed to the client. Since NusaShell uses its own HTTP adapter for
// regular requests (not the app-server subprocess), the compaction blob
// is not portable across the two approaches. This method returns a
// summary string only.
//
// On any error (subprocess failure, timeout, rate limit), the caller
// falls back to client-side compaction.
func (a *Adapter) CompactServer(ctx context.Context, c *domain.Conversation, model string, _ int) (string, error) {
	if len(c.Messages) <= 1 {
		return "", nil
	}
	return compactViaSubprocess(ctx, c, model)
}
