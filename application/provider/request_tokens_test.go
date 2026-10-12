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

func estimateBreakdownFixture() ChatRequest {
	return ChatRequest{
		Model:  "test-model",
		System: "You are a helpful assistant.",
		Messages: []ChatMessage{
			{Role: "user", Content: "hello there"},
			{Role: "assistant", Content: "hi", Reasoning: "thinking about it"},
			{Role: "user", Content: "another question"},
		},
		Tools: []ToolDef{{
			Name:        "read_file",
			Description: "Read a file from disk",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
	}
}

func TestEstimateRequestTokenBreakdownSumsToTotalAcrossKinds(t *testing.T) {
	req := estimateBreakdownFixture()
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
			breakdown := EstimateRequestTokenBreakdown(req, tc.kind, tc.openRouter)
			if want := EstimateRequestTokens(req, tc.kind, tc.openRouter); breakdown.Total != want {
				t.Fatalf("breakdown total = %d, EstimateRequestTokens = %d", breakdown.Total, want)
			}
			if sum := breakdown.System + breakdown.Messages + breakdown.Tools; sum != breakdown.Total {
				t.Fatalf("category sum = %d, total = %d; breakdown must partition the buffered total exactly", sum, breakdown.Total)
			}
			if breakdown.System <= 0 || breakdown.Messages <= 0 || breakdown.Tools <= 0 {
				t.Fatalf("breakdown = %+v, want positive system, messages, and tools", breakdown)
			}
		})
	}
}

func TestEstimateRequestTokenBreakdownCountsCompactionAsMessages(t *testing.T) {
	base := ChatRequest{
		Model:    "gpt-5",
		System:   "system",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	}
	withCompaction := base
	withCompaction.CompactionBlob = `[{"type":"compaction","encrypted_content":"` + strings.Repeat("x", 4000) + `"}]`

	for _, kind := range []domain.ProviderKind{domain.ProviderResponses, domain.ProviderCodex} {
		without := EstimateRequestTokenBreakdown(base, kind, false)
		with := EstimateRequestTokenBreakdown(withCompaction, kind, false)
		deltaTotal := with.Total - without.Total
		deltaMessages := with.Messages - without.Messages
		if deltaTotal <= 0 || deltaMessages <= 0 {
			t.Fatalf("kind %s: compaction blob must grow the messages category (total %+d, messages %+d)", kind, deltaTotal, deltaMessages)
		}
		// Apportioning may shift at most one rounding unit per unchanged
		// category; the blob itself is conversation input, never system/tools.
		if redistributed := deltaTotal - deltaMessages; redistributed > 2 || redistributed < -2 {
			t.Fatalf("kind %s: %d tokens of the compaction delta landed outside messages", kind, redistributed)
		}
	}

	for _, kind := range []domain.ProviderKind{domain.ProviderMessages, domain.ProviderChat} {
		without := EstimateRequestTokenBreakdown(base, kind, false)
		with := EstimateRequestTokenBreakdown(withCompaction, kind, false)
		if with != without {
			t.Fatalf("kind %s: non-replaying wire must ignore the compaction blob (without %+v, with %+v)", kind, without, with)
		}
	}
}

func TestEstimateRequestTokenBreakdownReflectsReasoningReplayVisibility(t *testing.T) {
	req := ChatRequest{
		Model:  "gpt-5",
		System: "system",
		Messages: []ChatMessage{
			{Role: "user", Content: "question"},
			{
				Role:           "assistant",
				Content:        "answer",
				Reasoning:      "visible chain of thought",
				ReasoningExtra: []byte(`{"encrypted_content":"` + strings.Repeat("e", 4000) + `"}`),
			},
			{Role: "user", Content: "follow up"},
		},
	}

	replaying := EstimateRequestTokenBreakdown(req, domain.ProviderResponses, false)
	stripped := EstimateRequestTokenBreakdown(req, domain.ProviderChat, false)
	if replaying.Messages <= stripped.Messages {
		t.Fatalf("responses messages = %d, chat messages = %d; opaque reasoning replay must stay in the conversation category", replaying.Messages, stripped.Messages)
	}
	// The system raw share is identical across kinds; only the apportioning
	// rounding unit may move as the conversation share changes.
	if diff := replaying.System - stripped.System; diff > 1 || diff < -1 {
		t.Fatalf("reasoning visibility moved the system category by %d (responses %d, chat %d)", diff, replaying.System, stripped.System)
	}
}

func TestEstimateRequestTokenBreakdownDoesNotCountInlineMediaAsText(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString(make([]byte, 1024*1024))
	req := ChatRequest{
		System: "system",
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "inspect this",
			Attachments: []domain.Attachment{
				{Type: "image", MediaType: "image/png", DataURL: "data:image/png;base64," + payload},
			},
		}},
	}
	breakdown := EstimateRequestTokenBreakdown(req, domain.ProviderResponses, false)
	if breakdown.Messages <= 0 || breakdown.Messages >= 10_000 {
		t.Fatalf("messages = %d, inline base64 payload must not be counted as text", breakdown.Messages)
	}
	if breakdown.Tools != 0 {
		t.Fatalf("tools = %d, want 0 for a request without tool definitions", breakdown.Tools)
	}
}

func TestEstimateRequestTokenBreakdownIgnoresApplicationOnlyFields(t *testing.T) {
	base := estimateBreakdownFixture()
	withInternalFields := base
	withInternalFields.Messages = append([]ChatMessage(nil), base.Messages...)
	withInternalFields.Messages[1].ReasoningExtra = []byte(`{"encrypted_content":"not sent to this wire"}`)

	for _, kind := range []domain.ProviderKind{domain.ProviderMessages, domain.ProviderChat} {
		gotBase := EstimateRequestTokenBreakdown(base, kind, false)
		gotInternal := EstimateRequestTokenBreakdown(withInternalFields, kind, false)
		if gotInternal != gotBase {
			t.Fatalf("kind %s: application-only fields changed breakdown %+v to %+v", kind, gotBase, gotInternal)
		}
	}
}

func TestEstimateRequestTokenBreakdownEmptyAndSingleCategory(t *testing.T) {
	empty := EstimateRequestTokenBreakdown(ChatRequest{}, domain.ProviderResponses, false)
	if empty != (RequestTokenBreakdown{}) {
		t.Fatalf("empty request breakdown = %+v, want zero", empty)
	}

	systemOnly := EstimateRequestTokenBreakdown(ChatRequest{System: "only a system prompt"}, domain.ProviderChat, false)
	if systemOnly.Total <= 0 || systemOnly.System != systemOnly.Total || systemOnly.Messages != 0 || systemOnly.Tools != 0 {
		t.Fatalf("system-only breakdown = %+v, want the full buffered total in system", systemOnly)
	}
}
