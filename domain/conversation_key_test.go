package domain

import (
	"strings"
	"testing"
)

func TestRenderConversationKey(t *testing.T) {
	ev := &Event{
		ID:   "e1",
		Type: "telegram.message",
		Attributes: map[string]any{
			"chat_id": "520213916",
		},
	}
	cases := []struct {
		name string
		tmpl string
		want string
	}{
		{"empty template", "", ""},
		{"static key passthrough", "kanban-main", "kanban-main"},
		{"event interpolation", "tg-${event.chat_id}", "tg-520213916"},
		{"nil event renders empty", "tg-${event.chat_id}", ""},
		{"unsafe chars collapse", "repo:owner/repo", "repo:owner-repo"},
		{"dashes collapse", "a---b--c", "a-b-c"},
		{"long value trimmed", "x-" + strings.Repeat("y", 200), "x-" + strings.Repeat("y", 118)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := ev
			if tc.name == "nil event renders empty" {
				e = nil
			}
			if got := RenderConversationKey(tc.tmpl, e); got != tc.want {
				t.Fatalf("RenderConversationKey(%q) = %q, want %q", tc.tmpl, got, tc.want)
			}
		})
	}
}

func TestAgentStepConversationFields(t *testing.T) {
	s := AgentStep{Prompt: "p", Model: "prov:m", Reuse: true, Conversation: "tg-${event.chat_id}"}
	if !s.Reuse || s.Conversation != "tg-${event.chat_id}" {
		t.Fatalf("AgentStep conversation fields not preserved: %+v", s)
	}
	var zero AgentStep
	if zero.Reuse || zero.Conversation != "" {
		t.Fatalf("AgentStep zero value must default to fresh conversations: %+v", zero)
	}
}

func TestResolveConcurrencyKey(t *testing.T) {
	ev := &Event{Attributes: map[string]any{"chat_id": "A"}}
	if got := ResolveConcurrencyKey("tg-${event.chat_id}", "wf", ev); got != "tg-A" {
		t.Fatalf("rendered key = %q, want tg-A", got)
	}
	if got := ResolveConcurrencyKey("", "wf", ev); got != "wf" {
		t.Fatalf("empty key fallback = %q, want wf", got)
	}
	if got := ResolveConcurrencyKey("tg-${event.missing}", "wf", ev); got != "wf" {
		t.Fatalf("empty render fallback = %q, want wf", got)
	}
	if got := ResolveConcurrencyKey("static-mon", "wf", nil); got != "static-mon" {
		t.Fatalf("static key = %q, want static-mon", got)
	}
}

func TestConversationTemplateIsPerResource(t *testing.T) {
	if !ConversationTemplateIsPerResource("tg-${event.chat_id}") {
		t.Fatal("event template must be per-resource")
	}
	if ConversationTemplateIsPerResource("shared") || ConversationTemplateIsPerResource("") {
		t.Fatal("static/empty templates are workflow-scoped")
	}
}
