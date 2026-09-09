package learn

import (
	"context"
	"testing"

	"nusashell/domain"
)

type recordingHeadlessRunner struct {
	sawDeadline bool
}

func (r *recordingHeadlessRunner) RunHeadlessTurn(ctx context.Context, _ string, _ string, _ domain.TrustLevel, _ map[string]any) (map[string]any, string, error) {
	_, r.sawDeadline = ctx.Deadline()
	return map[string]any{"output": "ok"}, "conv-test", nil
}

func TestRunLearningTurnDoesNotImposeWallClockDeadline(t *testing.T) {
	runner := &recordingHeadlessRunner{}
	service := New(Deps{Headless: runner})

	text, convID, err := service.runLearningTurn(context.Background(), "", "prompt")
	if err != nil {
		t.Fatalf("runLearningTurn error = %v", err)
	}
	if text != "ok" {
		t.Fatalf("text = %q, want %q", text, "ok")
	}
	if convID != "conv-test" {
		t.Fatalf("conversation id = %q, want %q", convID, "conv-test")
	}
	if runner.sawDeadline {
		t.Fatal("runLearningTurn imposed a wall-clock deadline on the provider context")
	}
}
