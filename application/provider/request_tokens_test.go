package provider

import (
	"encoding/base64"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestEstimateRequestTokensDoesNotCountInlineMediaPayloadAsText(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(make([]byte, 1024*1024))
	req := ChatRequest{
		System: "You are a helpful assistant.",
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "Inspect these media files.",
			Attachments: []domain.Attachment{
				{Type: "image", MediaType: "image/png", DataURL: "data:image/png;base64," + payload},
				{Type: "audio", MediaType: "audio/wav", DataURL: "data:audio/wav;base64," + payload},
				{Type: "video", MediaType: "video/mp4", DataURL: "data:video/mp4;base64," + payload},
			},
		}},
	}

	for _, tc := range []struct {
		name       string
		kind       domain.ProviderKind
		openRouter bool
	}{
		{name: "messages", kind: domain.ProviderMessages},
		{name: "responses", kind: domain.ProviderResponses},
		{name: "chat", kind: domain.ProviderChat},
		{name: "openrouter-chat", kind: domain.ProviderChat, openRouter: true},
		{name: "codex", kind: domain.ProviderCodex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EstimateRequestTokens(req, tc.kind, tc.openRouter)
			if got <= 0 {
				t.Fatalf("estimate = %d, want positive", got)
			}
			if got >= 10_000 {
				t.Fatalf("estimate = %d, large inline media was counted as text/base64", got)
			}
		})
	}
}

func TestEstimateRequestTokensIgnoresApplicationOnlyMessageFields(t *testing.T) {
	base := ChatRequest{
		Model:  "test-model",
		System: "system",
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "hello",
		}},
	}
	withInternalFields := base
	withInternalFields.Messages = []ChatMessage{{
		Role:           "user",
		Content:        "hello",
		Reasoning:      strings.Repeat("not sent for a user message", 1000),
		ReasoningExtra: []byte(`{"encrypted_content":"not sent"}`),
	}}

	for _, kind := range []domain.ProviderKind{
		domain.ProviderMessages,
		domain.ProviderResponses,
		domain.ProviderChat,
		domain.ProviderCodex,
	} {
		gotBase := EstimateRequestTokens(base, kind, false)
		gotWithInternalFields := EstimateRequestTokens(withInternalFields, kind, false)
		if gotWithInternalFields != gotBase {
			t.Fatalf("kind %s: application-only fields changed estimate from %d to %d", kind, gotBase, gotWithInternalFields)
		}
	}
}

func TestEstimateRequestTokensIncludesProviderVisibleCompactionItems(t *testing.T) {
	base := ChatRequest{
		Model:    "gpt-5",
		System:   "system",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	}
	withCompaction := base
	withCompaction.CompactionBlob = `[{"type":"reasoning","encrypted_content":"opaque-provider-item"}]`

	without := EstimateRequestTokens(base, domain.ProviderResponses, false)
	with := EstimateRequestTokens(withCompaction, domain.ProviderResponses, false)
	if with <= without {
		t.Fatalf("estimate without compaction items = %d, with = %d; provider-visible items must be counted", without, with)
	}
}
