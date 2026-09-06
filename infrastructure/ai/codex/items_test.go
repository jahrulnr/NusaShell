package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseItemRoundTripsEncryptedContentByteForByte(t *testing.T) {
	// A wire item with extra provider fields must re-marshal to the exact
	// input bytes: encrypted_content is opaque state copied byte-for-byte.
	input := `{"id":"cmp_001","type":"compaction","encrypted_content":"ENC-XYZ","summary":[],"future_field":{"nested":true}}`
	var item ResponseItem
	if err := json.Unmarshal([]byte(input), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !item.IsCompaction() {
		t.Fatalf("IsCompaction = false, want true")
	}
	if item.ID != "cmp_001" || item.EncryptedContent != "ENC-XYZ" {
		t.Fatalf("decoded fields = %q/%q, want cmp_001/ENC-XYZ", item.ID, item.EncryptedContent)
	}
	out, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != input {
		t.Fatalf("round-trip mismatch:\n got %s\nwant %s", out, input)
	}
}

func TestResponseItemRoundTripsReasoningWithEmptySummary(t *testing.T) {
	// Reasoning items whose summary is empty still carry encrypted_content
	// required for replay; both must survive a round-trip untouched.
	input := `{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"ENC-REASONING"}`
	var item ResponseItem
	if err := json.Unmarshal([]byte(input), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(item.Summary) != 0 {
		t.Fatalf("summary len = %d, want 0", len(item.Summary))
	}
	out, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != input {
		t.Fatalf("round-trip mismatch:\n got %s\nwant %s", out, input)
	}
}

func TestCompactionTriggerMarshalsMinimal(t *testing.T) {
	out, err := json.Marshal(NewCompactionTrigger())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `{"type":"compaction_trigger"}` {
		t.Fatalf("trigger = %s, want {\"type\":\"compaction_trigger\"}", out)
	}
}

func TestCompactionTriggerDecodesWithoutRawMutation(t *testing.T) {
	var item ResponseItem
	if err := json.Unmarshal([]byte(`{"type":"compaction_trigger"}`), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !item.IsCompactionTrigger() || item.IsCompaction() {
		t.Fatalf("trigger flags wrong: trigger=%v compaction=%v", item.IsCompactionTrigger(), item.IsCompaction())
	}
}

func TestMessageItemDecodesTypedContent(t *testing.T) {
	input := `{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"low"}]}`
	var item ResponseItem
	if err := json.Unmarshal([]byte(input), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !item.IsUserMessage() {
		t.Fatalf("IsUserMessage = false, want true")
	}
	if !item.HasTextContent() {
		t.Fatalf("HasTextContent = false, want true")
	}
	if len(item.Content) != 2 || item.Content[0].Text != "hello" || item.Content[1].ImageURL == "" {
		t.Fatalf("content = %+v, want typed text+image", item.Content)
	}
	// Mutating a decoded message must clear Raw to re-marshal the change.
	item.Content[0].Text = "changed"
	item.Raw = nil
	out, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "changed") {
		t.Fatalf("marshal after mutation = %s, want changed text", out)
	}
}

func TestUserMessageWithoutTextIsNotRealUserContent(t *testing.T) {
	var item ResponseItem
	if err := json.Unmarshal([]byte(`{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://example.invalid/a.png"}]}`), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item.HasTextContent() {
		t.Fatalf("HasTextContent = true for image-only user wrapper, want false")
	}
}

func TestUnknownItemTypePassesThrough(t *testing.T) {
	input := `{"type":"local_shell_call","call_id":"lsh_1","status":"completed","action":{"type":"exec"}}`
	var item ResponseItem
	if err := json.Unmarshal([]byte(input), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if item.Type != "local_shell_call" {
		t.Fatalf("type = %q, want local_shell_call", item.Type)
	}
	out, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != input {
		t.Fatalf("passthrough mismatch:\n got %s\nwant %s", out, input)
	}
	if isModelGeneratedItem(item) {
		t.Fatalf("unknown passthrough item classified as model-generated")
	}
}

func TestCloneIsDeep(t *testing.T) {
	original := ResponseItem{
		Type: ItemTypeMessage,
		Role: RoleUser,
		Content: []ContentItem{
			{Type: ContentInputText, Text: "hi", Raw: json.RawMessage(`{"type":"input_text","text":"hi"}`)},
		},
	}
	clone := original.Clone()
	clone.Content[0].Text = "mutated"
	clone.Content[0].Raw[0] = 'x'
	if original.Content[0].Text != "hi" {
		t.Fatalf("clone mutation leaked text into original")
	}
	if original.Content[0].Raw[0] != '{' {
		t.Fatalf("clone mutation leaked raw bytes into original")
	}

	items := []ResponseItem{original}
	cloned := CloneItems(items)
	cloned[0].Content = nil
	if len(items[0].Content) != 1 {
		t.Fatalf("CloneItems mutation leaked into original slice")
	}
}

func TestFunctionCallOutputDecodesRawOutput(t *testing.T) {
	input := `{"type":"function_call_output","call_id":"call_1","output":[{"type":"encrypted_content","encrypted_content":"ENC-OUT"}]}`
	var item ResponseItem
	if err := json.Unmarshal([]byte(input), &item); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var contents []ContentItem
	if err := json.Unmarshal(item.Output, &contents); err != nil {
		t.Fatalf("output decode: %v", err)
	}
	if len(contents) != 1 || contents[0].Type != ContentEncrypted || contents[0].EncryptedContent != "ENC-OUT" {
		t.Fatalf("output contents = %+v, want one encrypted_content entry", contents)
	}
}
