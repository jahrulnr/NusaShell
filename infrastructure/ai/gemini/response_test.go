package gemini

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/infrastructure/ai/core"
)

func TestConvertResponseTextThoughtTool(t *testing.T) {
	resp := &generateContentResponse{
		Candidates: []candidate{{
			FinishReason: "STOP",
			Content: &content{Parts: []part{
				{Text: "I will compute it.", Thought: true, ThoughtSignature: "sig-think"},
				{Text: "The answer is 42."},
			}},
		}},
		UsageMetadata: &usageMetadata{PromptTokenCount: 11, CandidatesTokenCount: 9, TotalTokenCount: 20},
	}
	out, err := convertResponse(resp, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("convertResponse: %v", err)
	}
	if len(out.Blocks) != 2 {
		t.Fatalf("blocks = %+v", out.Blocks)
	}
	reasoning, ok := out.Blocks[0].(core.ReasoningBlock)
	if !ok || reasoning.Text != "I will compute it." || reasoning.Signature != "sig-think" {
		t.Fatalf("reasoning block = %+v", out.Blocks[0])
	}
	text, ok := out.Blocks[1].(core.TextBlock)
	if !ok || text.Text != "The answer is 42." {
		t.Fatalf("text block = %+v", out.Blocks[1])
	}
	if out.FinishReason != core.FinishReasonStop || out.FinishReasonRaw != "STOP" {
		t.Fatalf("finish = %s (%s)", out.FinishReason, out.FinishReasonRaw)
	}
	if out.Usage.InputTokens != 11 || out.Usage.OutputTokens != 9 || out.Usage.TotalTokens != 20 {
		t.Fatalf("usage = %+v", out.Usage)
	}
	if out.Text() != "The answer is 42." || out.Reasoning() != "I will compute it." {
		t.Fatalf("text = %q reasoning = %q", out.Text(), out.Reasoning())
	}
}

func TestConvertResponseToolCallWithSignature(t *testing.T) {
	resp := &generateContentResponse{
		Candidates: []candidate{{
			FinishReason: "STOP",
			Content: &content{Parts: []part{
				{Text: "Calling now."},
				{FunctionCall: &functionCall{ID: "fc-1", Name: "lookup", Args: rawJSON(`{"q":"x"}`)}, ThoughtSignature: "sig-call"},
			}},
		}},
	}
	out, err := convertResponse(resp, "gemini-3-pro-preview")
	if err != nil {
		t.Fatalf("convertResponse: %v", err)
	}
	calls := out.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls = %+v", calls)
	}
	if calls[0].ID != "fc-1" || calls[0].Name != "lookup" || calls[0].Signature != "sig-call" {
		t.Fatalf("call = %+v", calls[0])
	}
	if out.FinishReason != core.FinishReasonToolCall {
		t.Fatalf("finish = %s, want tool_calls even when Gemini says STOP", out.FinishReason)
	}
}

func TestConvertResponseTextPartSignature(t *testing.T) {
	resp := &generateContentResponse{
		Candidates: []candidate{{
			FinishReason: "STOP",
			Content: &content{Parts: []part{
				{Text: "plain answer", ThoughtSignature: "sig-text"},
			}},
		}},
	}
	out, err := convertResponse(resp, "gemini-3-pro-preview")
	if err != nil {
		t.Fatalf("convertResponse: %v", err)
	}
	if len(out.Blocks) != 2 {
		t.Fatalf("blocks = %+v", out.Blocks)
	}
	carrier, ok := out.Blocks[0].(core.ReasoningBlock)
	if !ok || carrier.Text != "" || carrier.Signature != "sig-text" {
		t.Fatalf("signature carrier = %+v", out.Blocks[0])
	}
	if out.Reasoning() != "" {
		t.Fatalf("reasoning must stay empty for a text-part signature: %q", out.Reasoning())
	}
	if extra := carrier.Extra; len(extra) == 0 || !strings.Contains(string(extra), "sig-text") {
		t.Fatalf("carrier extra = %s, want replaysable thought_signature", extra)
	}
}

func TestConvertResponsePromptBlocked(t *testing.T) {
	resp := &generateContentResponse{
		PromptFeedback: &promptFeedback{BlockReason: "SAFETY"},
		UsageMetadata:  &usageMetadata{PromptTokenCount: 5, TotalTokenCount: 5},
	}
	out, err := convertResponse(resp, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("convertResponse: %v", err)
	}
	if out.FinishReason != core.FinishReasonSafety || out.FinishReasonRaw != "SAFETY" {
		t.Fatalf("finish = %s (%s)", out.FinishReason, out.FinishReasonRaw)
	}
	if len(out.Warnings) != 1 || out.Warnings[0].Code != "gemini.prompt_blocked" {
		t.Fatalf("warnings = %+v", out.Warnings)
	}
	if out.Usage.InputTokens != 5 {
		t.Fatalf("usage = %+v", out.Usage)
	}
}

func TestConvertResponseUsageAccounting(t *testing.T) {
	t.Run("thought tokens outside candidate count are added", func(t *testing.T) {
		meta := &usageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 8,
			TotalTokenCount:      30,
			ThoughtsTokenCount:   12,
		}
		got := convertUsage(meta)
		if got.OutputTokens != 20 {
			t.Fatalf("OutputTokens = %d, want 8+12", got.OutputTokens)
		}
		if got.ReasoningTokens != 12 {
			t.Fatalf("ReasoningTokens = %d", got.ReasoningTokens)
		}
	})
	t.Run("thought tokens inside candidate count are not double counted", func(t *testing.T) {
		meta := &usageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 20,
			TotalTokenCount:      30,
			ThoughtsTokenCount:   12,
		}
		got := convertUsage(meta)
		if got.OutputTokens != 20 {
			t.Fatalf("OutputTokens = %d, want 20", got.OutputTokens)
		}
	})
	t.Run("cached and tool-use tokens", func(t *testing.T) {
		meta := &usageMetadata{
			PromptTokenCount:        100,
			CandidatesTokenCount:    25,
			TotalTokenCount:         130,
			CachedContentTokenCount: 60,
			ToolUsePromptTokenCount: 5,
		}
		got := convertUsage(meta)
		if got.InputTokens != 105 || got.CacheReadTokens != 60 {
			t.Fatalf("usage = %+v", got)
		}
	})
}

func TestConvertResponseNoCandidates(t *testing.T) {
	_, err := convertResponse(&generateContentResponse{}, "gemini-2.5-flash")
	if err == nil || !strings.Contains(err.Error(), "no candidates") {
		t.Fatalf("err = %v, want no-candidates error", err)
	}
}

func TestConvertResponseUsesModelVersion(t *testing.T) {
	resp := &generateContentResponse{
		ModelVersion: "gemini-3.5-flash",
		Candidates: []candidate{{
			FinishReason: "STOP",
			Content:      &content{Parts: []part{{Text: "hi"}}},
		}},
	}
	out, err := convertResponse(resp, "gemini-3-flash")
	if err != nil {
		t.Fatalf("convertResponse: %v", err)
	}
	if out.Model != "gemini-3.5-flash" {
		t.Fatalf("Model = %q, want gemini-3.5-flash from ModelVersion", out.Model)
	}
}

func TestConvertResponseFallsBackToRequestModel(t *testing.T) {
	resp := &generateContentResponse{
		Candidates: []candidate{{
			FinishReason: "STOP",
			Content:      &content{Parts: []part{{Text: "hi"}}},
		}},
	}
	out, err := convertResponse(resp, "gemini-3-flash")
	if err != nil {
		t.Fatalf("convertResponse: %v", err)
	}
	if out.Model != "gemini-3-flash" {
		t.Fatalf("Model = %q, want request model when ModelVersion is empty", out.Model)
	}
}

func TestConvertResponseInlineImageOutput(t *testing.T) {
	resp := &generateContentResponse{
		Candidates: []candidate{{
			FinishReason: "STOP",
			Content: &content{Parts: []part{
				{InlineData: &blob{MimeType: "image/png", Data: "UE5H"}},
			}},
		}},
	}
	out, err := convertResponse(resp, "gemini-2.5-flash")
	if err != nil {
		t.Fatalf("convertResponse: %v", err)
	}
	image, ok := out.Blocks[0].(core.ImageBlock)
	if !ok || string(image.Data) != "PNG" || image.MIME != "image/png" {
		t.Fatalf("image block = %+v", out.Blocks[0])
	}
}

func rawJSON(value string) json.RawMessage {
	return json.RawMessage(value)
}
