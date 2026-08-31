package domain

import (
	"strings"
	"testing"
)

func TestRenderAgentPrompt_NoPlaceholders(t *testing.T) {
	tmpl := "lint all files in the workspace"
	if got := RenderAgentPrompt(tmpl, nil); got != tmpl {
		t.Fatalf("nil event: got %q, want %q", got, tmpl)
	}
	ev := &Event{Type: "x", Attributes: map[string]any{"text": "hi"}}
	if got := RenderAgentPrompt(tmpl, ev); got != tmpl {
		t.Fatalf("with event: got %q, want %q", got, tmpl)
	}
}

func TestRenderAgentPrompt_StandardAttributes(t *testing.T) {
	ev := &Event{
		Type:    "telegram.message",
		Subject: "Tuan",
		Attributes: map[string]any{
			"chat_id":    "12345",
			"message_id": "m_abc",
			"text":       "halo bos",
		},
	}
	cases := []struct {
		tmpl string
		want string
	}{
		{"reply to ${event.chat_id}", "reply to 12345"},
		{"msg=${event.message_id} text=${event.text}", "msg=m_abc text=halo bos"},
		{"subject=${event.subject}", "subject=Tuan"},
		{"type=${event.type}", "type=telegram.message"},
	}
	for _, c := range cases {
		got := RenderAgentPrompt(c.tmpl, ev)
		if got != c.want {
			t.Errorf("tmpl %q: got %q, want %q", c.tmpl, got, c.want)
		}
	}
}

func TestRenderAgentPrompt_GenericAttribute(t *testing.T) {
	ev := &Event{
		Attributes: map[string]any{
			"custom_key":  "custom_value",
			"nested.path": "deep",
		},
	}
	// Generic lookup (top-level only — dot-path uses lookupAttr's nested logic).
	if got := RenderAgentPrompt("v=${event.custom_key}", ev); got != "v=custom_value" {
		t.Errorf("custom_key: got %q", got)
	}
	// Dotted path inside Attributes map.
	if got := RenderAgentPrompt("v=${event.nested.path}", ev); got != "v=deep" {
		t.Errorf("nested.path: got %q", got)
	}
}

func TestRenderAgentPrompt_MissingAttributeIsEmpty(t *testing.T) {
	ev := &Event{Attributes: map[string]any{"chat_id": "123"}}
	got := RenderAgentPrompt("chat=${event.chat_id} text=${event.text}", ev)
	if got != "chat=123 text=" {
		t.Fatalf("missing attr: got %q, want %q", got, "chat=123 text=")
	}
}

func TestRenderAgentPrompt_NilEventAndMissingKeys(t *testing.T) {
	if got := RenderAgentPrompt("x=${event.chat_id}", nil); got != "x=" {
		t.Fatalf("nil event: got %q, want %q", got, "x=")
	}
	ev := &Event{}
	if got := RenderAgentPrompt("x=${event.chat_id}", ev); got != "x=" {
		t.Fatalf("zero event: got %q, want %q", got, "x=")
	}
}

func TestRenderAgentPrompt_NonStringValues(t *testing.T) {
	ev := &Event{
		Attributes: map[string]any{
			"count": 42,
			"flag":  true,
		},
	}
	if got := RenderAgentPrompt("n=${event.count}", ev); got != "n=42" {
		t.Errorf("int: got %q", got)
	}
	if got := RenderAgentPrompt("f=${event.flag}", ev); got != "f=true" {
		t.Errorf("bool: got %q", got)
	}
}

func TestRenderAgentPrompt_MultipleOccurrencesAndLiterals(t *testing.T) {
	ev := &Event{Attributes: map[string]any{"a": "1", "b": "2"}}
	tmpl := "a=${event.a}-${event.a}+b=${event.b} literal ${} ${event.} no-key=${event.x}"
	got := RenderAgentPrompt(tmpl, ev)
	want := "a=1-1+b=2 literal ${} ${event.} no-key="
	if got != want {
		t.Errorf("multi: got %q, want %q", got, want)
	}
	// Sanity: literal "${}" (not event.) and "${event.}" (empty key) stay literal
	// because the regex requires at least one char in the key.
	if !strings.Contains(got, "${}") {
		t.Errorf("literal ${} should be preserved: %q", got)
	}
	if !strings.Contains(got, "${event.}") {
		t.Errorf("literal ${event.} should be preserved: %q", got)
	}
}
