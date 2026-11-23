// Package memory provides the memory index hydration record: before each
// provider call the harness renders the memories the thread sees, and the
// turn re-records it only when it changed. The schema is
// ToolPermissionSystem — the model reads and writes memory through the
// memory_* tools of the built-in "memory" MCP server, never this name.
package memory

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

type IndexTool struct {
	index  dservice.MemoryIndex
	schema valueobject.ToolSchema
}

func NewIndexTool(index dservice.MemoryIndex) (dservice.Tool, error) {
	if index == nil {
		return nil, errors.New("memory index is required")
	}
	schema, err := valueobject.NewToolSchema("memory",
		"The memories this thread sees, one line each. Written by the harness before provider calls; never callable by the agent.",
		json.RawMessage(`{"type":"object","properties":{}}`), true, enum.ToolPermissionSystem)
	if err != nil {
		return nil, err
	}
	return &IndexTool{index: index, schema: schema}, nil
}

func (t *IndexTool) Schema() valueobject.ToolSchema { return t.schema }

func (t *IndexTool) Execute(ctx context.Context, _ entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	text, err := t.index.Index(ctx, dservice.ConversationID(ctx))
	if err != nil {
		return dservice.ToolResult{}, err
	}
	if text == "" {
		text = "Memory is turned off by the owner: nothing is remembered across threads, and the memory tools refuse."
	}
	return dservice.ToolResult{Text: text}, nil
}
