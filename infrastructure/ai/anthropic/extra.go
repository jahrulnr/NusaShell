package anthropic

import "encoding/json"

// thinkingReplayExtra is the opaque JSON payload stored in
// core.ReasoningBlock.Extra and persisted by the application layer as
// Message.ReasoningExtra. It carries the replay state Anthropic requires
// back verbatim: the signature proving a thinking block is unmodified, and
// the redacted_thinking data blob. The shape is private to this wire
// package; the application layer only checks that one of these keys is
// present before replaying the payload to this provider.
type thinkingReplayExtra struct {
	Signature string `json:"signature,omitempty"`
	Redacted  string `json:"redacted,omitempty"`
}

// thinkingExtra encodes Anthropic replay state for ReasoningBlock.Extra.
// It returns nil when neither signature nor redacted data is present so an
// empty payload is never attached to a reasoning delta.
func thinkingExtra(signature, redacted string) json.RawMessage {
	if signature == "" && redacted == "" {
		return nil
	}
	data, err := json.Marshal(thinkingReplayExtra{Signature: signature, Redacted: redacted})
	if err != nil {
		return nil
	}
	return data
}

// thinkingFromExtra decodes replay state captured by thinkingExtra. Fields
// the decoder does not know (a foreign provider's extra shape) are ignored,
// so a mismatched payload decodes to empty values instead of an error.
func thinkingFromExtra(extra json.RawMessage) (signature, redacted string) {
	if len(extra) == 0 {
		return "", ""
	}
	var payload thinkingReplayExtra
	if err := json.Unmarshal(extra, &payload); err != nil {
		return "", ""
	}
	return payload.Signature, payload.Redacted
}

// firstNonEmpty returns the first non-empty value; the typed
// ReasoningBlock fields take precedence over the opaque Extra fallback.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
