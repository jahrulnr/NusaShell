package domain

import (
	"encoding/json"
	"net/url"
	"strings"
)

// SanitizeToolName rewrites a tool name so it matches the OpenAI Responses
// API pattern ^[a-zA-Z0-9_-]+$. Models occasionally hallucinate tool names
// with characters the provider rejects (e.g. "terminal:exec", "fs.read",
// "mcp/server"). Without auto-heal the conversation becomes unreplayable:
// every subsequent request returns HTTP 400 "Invalid 'input[N].name': string
// does not match pattern" because the offending name is persisted in the
// assistant message history.
//
// Sanitization is safe for all three provider styles (Responses API,
// chat-completions, Codex) because they pair function_call ↔
// function_call_output by call_id, not by name. The rewritten name is only
// sent on the wire; the persisted ToolCall.Name is left untouched so the
// learning log and UI keep showing the original (hallucinated) name for
// debugging.
func SanitizeToolName(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// RepairToolCallArguments attempts to repair malformed JSON tool call
// arguments by stripping markdown fences and trailing commas. Returns "{}"
// for empty/whitespace input. If the result is not valid JSON, returns "{}".
func RepairToolCallArguments(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "{}"
	}
	if fence := extractMarkdownFence(trimmed); fence != "" {
		trimmed = strings.TrimSpace(fence)
	}
	repaired := stripTrailingCommas(trimmed)
	var obj map[string]any
	if err := json.Unmarshal([]byte(repaired), &obj); err == nil {
		return repaired
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		return trimmed
	}
	return "{}"
}

func extractMarkdownFence(value string) string {
	if !strings.HasPrefix(value, "```") {
		return ""
	}
	rest := value[3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		lang := strings.TrimSpace(rest[:nl])
		if lang == "" || lang == "json" {
			rest = rest[nl+1:]
		} else {
			return ""
		}
	} else {
		return ""
	}
	rest = strings.TrimRight(rest, "\n\r")
	if strings.HasSuffix(rest, "```") {
		return strings.TrimSuffix(rest, "```")
	}
	return ""
}

func stripTrailingCommas(value string) string {
	var out strings.Builder
	out.Grow(len(value))
	quoted := false
	escaped := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if quoted {
			out.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
			continue
		}
		if ch == '"' {
			quoted = true
			out.WriteByte(ch)
			continue
		}
		if ch == ',' {
			next := i + 1
			for next < len(value) && (value[next] == ' ' || value[next] == '\t' || value[next] == '\n' || value[next] == '\r') {
				next++
			}
			if next < len(value) && (value[next] == '}' || value[next] == ']') {
				continue
			}
		}
		out.WriteByte(ch)
	}
	return out.String()
}

// TextAttachmentContent renders a text attachment as a header + full content
// block, so models do not mistake the attachment for a dangling reference.
func TextAttachmentContent(attachment Attachment) string {
	return "\n[Attached text file: " + attachment.Name + " - full content included below]\n\n" + attachment.Content
}

// DocumentAttachmentContent renders a document attachment as a descriptive
// text marker (used by Chat Completions, which has no portable file part).
func DocumentAttachmentContent(attachment Attachment) string {
	return "[Attached document: " + attachment.Name + " (" + attachment.MediaType + ")]"
}

// DataURLBase64 extracts the base64 payload from a data: URL.
func DataURLBase64(dataURL string) string {
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return ""
	}
	return data
}

// IsOpenAIDirectURL reports whether baseURL points to a direct OpenAI host
// (api.openai.com), not an aggregator.
func IsOpenAIDirectURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.openai.com" || strings.HasSuffix(host, ".api.openai.com")
}

// IsOpenRouterHost reports whether a chat-kind provider with the given base
// URL should use the OpenRouter wire format (extra headers, reasoning_details,
// cache_retention). Returns true for all chat-kind hosts except direct OpenAI.
func IsOpenRouterHost(kind ProviderKind, baseURL string) bool {
	return kind == ProviderChat && !IsOpenAIDirectURL(baseURL)
}
