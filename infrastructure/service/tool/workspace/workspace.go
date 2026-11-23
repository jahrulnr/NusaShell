// Package workspace provides the harness's workspace-binding record tool.
// Its schema rides the conversation catalog with ToolPermissionSystem:
// the turn writes completed injection calls under this name to pin the
// working directory into history, while the agent-facing permission gate
// keeps the name off the provider's tool list — a model imitating the
// call it observed is rejected as not permitted.
package workspace

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// WorkspaceTool advertises the workspace-binding record. The harness is
// its only caller: Execute exists to satisfy the tool contract (and to
// answer truthfully if a system-side path ever invokes it) — the
// recorded pin output and a live Execute describe the same binding.
type WorkspaceTool struct {
	schema valueobject.ToolSchema
}

// NewWorkspaceTool validates the advertised schema.
func NewWorkspaceTool() (dservice.Tool, error) {
	schema, err := valueobject.NewToolSchema(
		"workspace",
		"Reports the working directory a turn is bound to. Written by the harness as an injection record; never callable by the agent.",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"absolute working directory"},"folders":{"type":"array","items":{"type":"string"},"description":"further absolute folders the file tools reach"}},"required":["path"]}`),
		true,
		enum.ToolPermissionSystem,
	)
	if err != nil {
		return nil, err
	}
	return &WorkspaceTool{schema: schema}, nil
}

func (tool *WorkspaceTool) Schema() valueobject.ToolSchema {
	return tool.schema
}

// Execute reports the turn-scoped binding — the same line a pin records.
func (tool *WorkspaceTool) Execute(ctx context.Context, _ entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return dservice.ToolResult{}, err
	}
	dir := dservice.WorkspaceDir(ctx)
	if dir == "" {
		dir = "(unset)"
	}
	text := "workspace changed to " + dir
	if extra := dservice.ExtraFolders(ctx); len(extra) > 0 {
		text += "; also reachable by absolute path: " + strings.Join(extra, ", ")
	}
	return dservice.ToolResult{Text: text}, nil
}
