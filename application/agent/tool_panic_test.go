package agent

import (
	"context"
	"strings"
	"testing"

	"nusashell/application/tools"
	"nusashell/domain"
)

type panicToolbox struct{}

func (panicToolbox) ListTools() []tools.ToolInfo { return nil }

func (panicToolbox) Execute(context.Context, string, []byte) (string, error) {
	panic("simulated Telegram send adapter panic")
}

func TestRunOneToolRecoversToolboxPanic(t *testing.T) {
	svc := &Service{Toolbox: panicToolbox{}}
	run := &TurnRun{
		ID:             "run-1",
		ConversationID: "conversation-1",
		Ctx:            context.Background(),
		Headless:       true,
	}

	var result ToolExecResult
	panicked := false
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicked = true
			}
		}()
		result = svc.RunOneTool(run, "message-1", domain.ToolCall{
			ID:   "call-1",
			Name: "telegram.send_message",
			Args: `{}`,
		}, ModelCapabilities{}, domain.Settings{}, 1)
	}()
	if panicked {
		t.Fatal("RunOneTool allowed a toolbox panic to escape")
	}
	if result.Status != domain.ToolFailed {
		t.Fatalf("tool status = %s, want fail", result.Status)
	}
	if !strings.Contains(result.Output, "panic") {
		t.Fatalf("tool output = %q, want panic diagnostic", result.Output)
	}
}
