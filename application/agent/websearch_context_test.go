package agent

import (
	"context"
	"testing"

	"nusashell/application/tools"
	"nusashell/domain"
)

type webSearchContextToolbox struct {
	providerID string
	model      string
}

func (b *webSearchContextToolbox) ListTools() []tools.ToolInfo { return nil }
func (b *webSearchContextToolbox) Execute(ctx context.Context, name string, _ []byte) (string, error) {
	if name != "web_search" {
		return "unexpected tool", nil
	}
	b.providerID = tools.ProviderIDFromContext(ctx)
	b.model = tools.ModelFromContext(ctx)
	return "ok", nil
}

func TestRunOneToolPropagatesProviderAndModelContext(t *testing.T) {
	box := &webSearchContextToolbox{}
	svc := &Service{Toolbox: box}
	run := &TurnRun{
		ID:             "run-1",
		ConversationID: "conversation-1",
		ProviderID:     "codex",
		Model:          "gpt-5-codex",
		Ctx:            context.Background(),
	}

	result := svc.RunOneTool(run, "message-1", domain.ToolCall{ID: "call-1", Name: "web_search", Args: `{"query":"test"}`}, ModelCapabilities{}, domain.Settings{}, 1)
	if result.Status != domain.ToolOK {
		t.Fatalf("status = %s, want %s; output = %s", result.Status, domain.ToolOK, result.Output)
	}
	if box.providerID != "codex" || box.model != "gpt-5-codex" {
		t.Fatalf("context = provider %q model %q", box.providerID, box.model)
	}
}
