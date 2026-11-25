package toolfile

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ReadMediaToolName is the catalog name of the media reader. It is the name
// the encoder's omitted-attachment placeholder points at for images, so the
// tool and the placeholder cannot drift apart.
const ReadMediaToolName = "read_media"

// ReadMediaTool loads a file the model should inspect rather than read as
// text — under the turn's workspace, or an absolute host path via
// Workspace.OpenHost. What it does with the payload depends on the turn's
// model, never on the caller's words: a model that accepts the modality gets
// the file attached (a file:// reference adapters resolve at send time, pinned
// by sha256 to the bytes hashed here), and a model that cannot take it gets a
// description instead — an image through the vision role — as text, so the
// description never counts toward the image cap. Text payloads answer like
// read_file: the model never has to know which name fits before reading.
type ReadMediaTool struct {
	route  mediaRoute
	schema valueobject.ToolSchema
}

// NewReadMediaTool binds the tool's default root: every requested path
// resolves inside it. A turn-scoped workspace dir overrides it per call; the
// repository creates attachment URIs for provider-time resolution.
func NewReadMediaTool(store drepo.Workspace, root string, media MediaDeps) (service.Tool, error) {
	abs, err := checkDir(store, root)
	if err != nil {
		return nil, err
	}
	schema, err := valueobject.NewToolSchema(
		ReadMediaToolName,
		"Load a file so it can be inspected directly: an image, audio, video, or other non-text media — and, when the current model cannot take that kind, a description (images) or a transcript (audio) instead. Text files come back as read_file renders them. For files you want to read as text, read_file is the same tool with paging.",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the workspace root, an absolute host path, or ~/… for the owner's home"}},"required":["path"]}`),
		true,
		enum.ToolPermissionAgent,
	)
	if err != nil {
		return nil, err
	}
	return &ReadMediaTool{route: mediaRoute{workspace: store, root: abs, media: media}, schema: schema}, nil
}

func (tool *ReadMediaTool) Schema() valueobject.ToolSchema {
	return tool.schema
}

func (tool *ReadMediaTool) Execute(ctx context.Context, call entity.ToolCall, _ service.ToolEmitter) (service.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return service.ToolResult{}, err
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &input); err != nil {
		return service.ToolResult{}, fmt.Errorf("invalid %s input: %w", ReadMediaToolName, err)
	}
	return tool.route.attachOrRead(ctx, call, input.Path)
}
