package aiutil

import (
	"encoding/json"
	"fmt"
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
