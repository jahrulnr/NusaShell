package tools

import (
	"context"

	"nusashell/domain"
)

// ToolInfo is one advertised tool definition (name, description, JSON schema).
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ToolExecutor is the execution surface for advertised tools.
type ToolExecutor interface {
	ListTools() []ToolInfo
	Execute(ctx context.Context, name string, argsJSON []byte) (string, error)
}

// DocsSource is the documentation corpus the docs.* RPC and docs dispatcher
// tool read. Infrastructure/docs.Source implements this.
type DocsSource interface {
	List() []DocMeta
	Search(query string, limit int) []DocHit
	Read(id string) (DocFull, error)
}

// DocMeta is a documentation page listing row.
type DocMeta struct {
	ID    string
	Title string
	Path  string
}

// DocHit is a documentation search hit.
type DocHit struct {
	DocMeta
	Snippet string
}

// DocFull is a documentation page with content.
type DocFull struct {
	DocMeta
	Content string
}

// HeadlessTurnRunner is the consumer-side port PipelineAgentRunner uses to
// run an unattended agent step. Narrower than automation.HeadlessTurnRunner
// (no Steer): tools must not import automation. *App still satisfies both.
type HeadlessTurnRunner interface {
	RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error)
}
