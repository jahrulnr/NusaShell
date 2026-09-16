package agent

import (
	"context"
	"testing"

	"nusashell/application/tools"
)

type hydrationSkillContextToolbox struct {
	workspace string
}

func (b *hydrationSkillContextToolbox) ListTools() []tools.ToolInfo { return nil }

func (b *hydrationSkillContextToolbox) Execute(ctx context.Context, name string, _ []byte) (string, error) {
	if name == "skill" {
		b.workspace = tools.WorkspaceFromContext(ctx)
		return `{"id":"workspace-skill"}`, nil
	}
	return "", nil
}

func TestHydrationSkillUsesRuntimeWorkspaceContext(t *testing.T) {
	box := &hydrationSkillContextToolbox{}
	workspace := "/tmp/project"
	result := NewHydrationBuilder(HydrationSource{
		Executor:       box,
		RuntimeContext: RuntimeContextSnapshot{Workspace: workspace},
	}).Build()

	if box.workspace != workspace {
		t.Fatalf("skill hydration workspace = %q, want %q", box.workspace, workspace)
	}
	var found bool
	for _, message := range result.Messages {
		for _, call := range message.ToolCalls {
			if call.Name == "skill" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("hydration did not include the real skill tool result")
	}
}
