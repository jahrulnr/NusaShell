package aiutil

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MustJSON marshals v to a json.RawMessage. On error it returns an empty
// JSON array, matching the original behaviour where a failed marshal of
// content blocks should never panic the caller.
func MustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

// ParseFloat parses a pricing string like "3.0" (USD per MTok) into a
// float64.
func ParseFloat(s string) (float64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	return v, err
}

// Deref safely dereferences a *string, returning "" for nil.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// StrJSON encodes a plain string as a JSON string RawMessage (message
// content in the Responses API is a string, not a block array).
func StrJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

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
