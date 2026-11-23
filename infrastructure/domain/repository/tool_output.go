package repository

import (
	"context"
	"io"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ToolOutput stores conversation-owned tool output. Reserve checks the
// owner and allocates a reference for a text stream; Open creates the
// file only if output actually exceeds the in-memory budget. Keep stores
// media a tool produced (a screenshot, a generated image) and answers a
// sha256-pinned file:// attachment to it, named for its media type, so
// the model can be shown it at send time and the thread can display it.
type ToolOutput interface {
	Reserve(ctx context.Context, conversationID, callID string) (ToolOutputSpill, error)
	Keep(ctx context.Context, conversationID, callID, mediaType string, data []byte) (valueobject.Attachment, error)
}

type ToolOutputSpill interface {
	Reference() string
	Open() (io.WriteCloser, error)
}
