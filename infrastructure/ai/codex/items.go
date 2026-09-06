package codex

import (
	"encoding/json"
	"fmt"
)

// ResponseItem type tags on the wire (serde tag "type", snake_case), matching
// protocol/src/models.rs ResponseItem.
const (
	ItemTypeMessage            = "message"
	ItemTypeReasoning          = "reasoning"
	ItemTypeFunctionCall       = "function_call"
	ItemTypeFunctionCallOutput = "function_call_output"
	ItemTypeWebSearchCall      = "web_search_call"
	ItemTypeCompaction         = "compaction"
	// ItemTypeCompactionTrigger is a request control item, not a durable
	// response item. It is appended as the last input item of a remote v2
	// compaction request and never stored in history.
	ItemTypeCompactionTrigger = "compaction_trigger"
)

// Message roles used by the retained-history and accounting logic.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleDeveloper = "developer"
	RoleSystem    = "system"
)

// ContentItem type tags, matching protocol/src/models.rs ContentItem (plus
// the encrypted_content variant used inside function call outputs).
const (
	ContentInputText  = "input_text"
	ContentOutputText = "output_text"
	ContentInputImage = "input_image"
	ContentInputAudio = "input_audio"
	ContentEncrypted  = "encrypted_content"
)

// ReasoningSummary is one summary entry of a reasoning item
// ({type: "summary_text", text: "..."}).
type ReasoningSummary struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ContentItem is one tagged content entry of a message or function call
// output. Raw preserves the exact wire bytes on decode so items round-trip
// byte-for-byte; clear Raw before re-marshaling a mutated item.
type ContentItem struct {
	Type             string          `json:"type"`
	Text             string          `json:"text,omitempty"`
	ImageURL         string          `json:"image_url,omitempty"`
	Detail           string          `json:"detail,omitempty"` // auto, low, high, original
	AudioURL         string          `json:"audio_url,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
	Raw              json.RawMessage `json:"-"`
}

func (c ContentItem) MarshalJSON() ([]byte, error) {
	if len(c.Raw) > 0 {
		return append([]byte(nil), c.Raw...), nil
	}
	type alias ContentItem
	return json.Marshal(alias(c))
}

func (c *ContentItem) UnmarshalJSON(data []byte) error {
	type alias ContentItem
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = ContentItem(decoded)
	c.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// ResponseItem is the tagged conversation item union exchanged with the
// Responses API. Only the variants needed by context accounting, replay, and
// compaction are typed; anything else decodes with its exact bytes preserved
// in Raw and marshals back byte-for-byte.
//
// Field semantics by Type:
//   - message: Role + Content
//   - reasoning: ID + Summary + EncryptedContent (opaque, required for replay
//     even when Summary is empty)
//   - compaction: ID + EncryptedContent (opaque server-side compaction state)
//   - compaction_trigger: no fields (request-only control)
//   - function_call: ID + CallID + Name + Arguments
//   - function_call_output: ID + CallID + Name + Output (raw wire payload:
//     either a JSON string or an array of content items)
//   - web_search_call: ID + Status + Action (raw)
type ResponseItem struct {
	Type             string             `json:"type"`
	ID               string             `json:"id,omitempty"`
	Role             string             `json:"role,omitempty"`
	Content          []ContentItem      `json:"content,omitempty"`
	Summary          []ReasoningSummary `json:"summary,omitempty"`
	EncryptedContent string             `json:"encrypted_content,omitempty"`
	CallID           string             `json:"call_id,omitempty"`
	Name             string             `json:"name,omitempty"`
	Arguments        string             `json:"arguments,omitempty"`
	Output           json.RawMessage    `json:"output,omitempty"`
	Status           string             `json:"status,omitempty"`
	Action           json.RawMessage    `json:"action,omitempty"`
	Raw              json.RawMessage    `json:"-"`
}

func (i ResponseItem) MarshalJSON() ([]byte, error) {
	if len(i.Raw) > 0 {
		return append([]byte(nil), i.Raw...), nil
	}
	type alias ResponseItem
	if len(i.Content) > 0 {
		// Clearing the parent Raw marks the whole item as mutated. Do not let a
		// child content item's preserved wire bytes overwrite typed mutations.
		i.Content = append([]ContentItem(nil), i.Content...)
		for idx := range i.Content {
			i.Content[idx].Raw = nil
		}
	}
	return json.Marshal(alias(i))
}

func (i *ResponseItem) UnmarshalJSON(data []byte) error {
	type alias ResponseItem
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("codex: decode response item: %w", err)
	}
	*i = ResponseItem(decoded)
	i.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// Clone returns a deep copy of the item. Raw bytes are copied, never shared.
func (i ResponseItem) Clone() ResponseItem {
	out := i
	out.Raw = append(json.RawMessage(nil), i.Raw...)
	out.Action = append(json.RawMessage(nil), i.Action...)
	out.Output = append(json.RawMessage(nil), i.Output...)
	if i.Content != nil {
		out.Content = make([]ContentItem, len(i.Content))
		for idx, content := range i.Content {
			out.Content[idx] = content
			out.Content[idx].Raw = append(json.RawMessage(nil), content.Raw...)
		}
	}
	if i.Summary != nil {
		out.Summary = append([]ReasoningSummary(nil), i.Summary...)
	}
	return out
}

// CloneItems returns a deep copy of an item slice.
func CloneItems(items []ResponseItem) []ResponseItem {
	if items == nil {
		return nil
	}
	out := make([]ResponseItem, len(items))
	for idx, item := range items {
		out[idx] = item.Clone()
	}
	return out
}

// NewCompactionTrigger returns the request-only control item
// {"type":"compaction_trigger"}.
func NewCompactionTrigger() ResponseItem {
	return ResponseItem{Type: ItemTypeCompactionTrigger}
}

// NewTextMessage returns a message item with a single text content entry.
func NewTextMessage(role, contentType, text string) ResponseItem {
	return ResponseItem{
		Type:    ItemTypeMessage,
		Role:    role,
		Content: []ContentItem{{Type: contentType, Text: text}},
	}
}

// IsCompaction reports whether the item is an opaque server-side compaction
// output item (never a compaction_trigger).
func (i ResponseItem) IsCompaction() bool {
	return i.Type == ItemTypeCompaction
}

// IsCompactionTrigger reports whether the item is the request-only control.
func (i ResponseItem) IsCompactionTrigger() bool {
	return i.Type == ItemTypeCompactionTrigger
}

// IsUserMessage reports whether the item is an ordinary user-role message.
func (i ResponseItem) IsUserMessage() bool {
	return i.Type == ItemTypeMessage && i.Role == RoleUser
}

// HasTextContent reports whether the item is a message with at least one
// text content entry. It is the port's proxy for codex's
// parse_turn_item(UserMessage) check: it distinguishes real user turns from
// session-prefix or instruction-wrapper user items, which carry no
// input_text/output_text content.
func (i ResponseItem) HasTextContent() bool {
	if i.Type != ItemTypeMessage {
		return false
	}
	for _, content := range i.Content {
		if content.Type == ContentInputText || content.Type == ContentOutputText {
			return true
		}
	}
	return false
}
