package application

import (
	"context"
	"testing"

	"nusashell/application/tools"
	"nusashell/domain"
)

// TestToolContextKeysRoundTripThroughToolsWriter pins the production contract:
// the agent package annotates Execute with application/tools helpers, and
// infrastructure/tools reads the same values via this package. Go context
// keys are compared by type, so a second ctxKey here makes todo and
// memory_project see an empty conversation/workspace.
func TestToolContextKeysRoundTripThroughToolsWriter(t *testing.T) {
	ctx := tools.WithConversationID(context.Background(), "conv_1")
	ctx = tools.WithWorkspace(ctx, "/ws/proj")
	ctx = tools.WithRunID(ctx, "run_1")
	ctx = tools.WithToolCallID(ctx, "call_1")

	if got := ConversationIDFromContext(ctx); got != "conv_1" {
		t.Fatalf("conversation id = %q, want conv_1", got)
	}
	if got := WorkspaceFromContext(ctx); got != "/ws/proj" {
		t.Fatalf("workspace = %q, want /ws/proj", got)
	}
	if got := RunIDFromContext(ctx); got != "run_1" {
		t.Fatalf("run id = %q, want run_1", got)
	}
	if got := ToolCallIDFromContext(ctx); got != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", got)
	}
}

type contextProbeToolbox struct {
	conversationID string
	workspace      string
	runID          string
	toolCallID     string
}

func (b *contextProbeToolbox) ListTools() []ToolInfo { return nil }

func (b *contextProbeToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	b.conversationID = ConversationIDFromContext(ctx)
	b.workspace = WorkspaceFromContext(ctx)
	b.runID = RunIDFromContext(ctx)
	b.toolCallID = ToolCallIDFromContext(ctx)
	return "ok", nil
}

// TestRunOneToolAnnotatesConversationAndWorkspace matches HandleTurnsStart:
// run.Ctx is a bare cancel context; conversation id and workspace live on
// the TurnRun fields and must be copied onto the tool context with keys
// the real toolbox can read.
func TestRunOneToolAnnotatesConversationAndWorkspace(t *testing.T) {
	box := &contextProbeToolbox{}
	app := &App{Bus: NewBus(), Toolbox: box, Logs: &fakeLogStore{}}
	run := &TurnRun{
		ID:             "run_1",
		ConversationID: "conv_1",
		Workspace:      "/ws/proj",
		Ctx:            context.Background(),
	}
	got := app.runOneTool(run, "msg_1", domain.ToolCall{ID: "call_1", Name: "todo", Args: `{}`}, ModelCapabilities{}, domain.Settings{}, 1)
	if got.Status != domain.ToolOK {
		t.Fatalf("status = %s output = %q, want ok", got.Status, got.Output)
	}
	if box.conversationID != "conv_1" {
		t.Fatalf("conversation id = %q, want conv_1", box.conversationID)
	}
	if box.workspace != "/ws/proj" {
		t.Fatalf("workspace = %q, want /ws/proj", box.workspace)
	}
	if box.runID != "run_1" {
		t.Fatalf("run id = %q, want run_1", box.runID)
	}
	if box.toolCallID != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", box.toolCallID)
	}
}
