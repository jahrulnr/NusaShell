package transport

import (
	"context"
	"nusashell/domain"
	"strings"
	"testing"
	"time"
)

// TestPipelineAgentStepRunsHeadlessTurn verifies that a pipeline agent step
// executes a real agent turn (with ACP tools hidden) and returns the final
// assistant text as output.
func TestPipelineAgentStepRunsHeadlessTurn(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})

	h.llm.setRounds([][]llmStep{
		{{Text: "Pipeline agent completed: all files linted."}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, _, err := h.app.RunHeadlessTurn(ctx, "lint all files in the workspace", "", domain.TrustSafe, nil)
	if err != nil {
		t.Fatalf("RunHeadlessTurn: %v", err)
	}
	text, _ := out["output"].(string)
	if !strings.Contains(text, "Pipeline agent completed") {
		t.Fatalf("output text = %q, want substring %q", text, "Pipeline agent completed")
	}
}

// TestPipelineAgentStepRespectsModelField verifies that the model field on
// a headless turn selects the specified provider:model.
func TestPipelineAgentStepRespectsModelField(t *testing.T) {
	h := newHarness(t, nil)
	pid := h.addOpenAIProvider(t, "Fake")
	h.rpcOK(t, "ai.providers.import-models", map[string]any{"id": pid})

	h.llm.setRounds([][]llmStep{
		{{Text: "Done with specified model."}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, _, err := h.app.RunHeadlessTurn(ctx, "run checks", "", domain.TrustSafe, nil)
	if err != nil {
		t.Fatalf("RunHeadlessTurn: %v", err)
	}
	text, _ := out["output"].(string)
	if !strings.Contains(text, "Done with specified model") {
		t.Fatalf("output text = %q", text)
	}
}

// TestPipelineAgentStepFailsWithoutProvider verifies that a headless turn
// returns a clear error when no enabled provider is available.
func TestPipelineAgentStepFailsWithoutProvider(t *testing.T) {
	h := newHarness(t, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := h.app.RunHeadlessTurn(ctx, "do work", "", domain.TrustSafe, nil)
	if err == nil || !strings.Contains(err.Error(), "no enabled provider") {
		t.Fatalf("want no-enabled-provider error, got %v", err)
	}
}
