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

// CodexSearchRequest is the application-facing query sent to the Codex
// standalone web-search backend.
type CodexSearchRequest struct {
	ProviderID     string
	AccountID      string
	ConversationID string
	Model          string
	Query          string
	Limit          int
}

// CodexSearchResult is a normalized structured result returned by Codex.
type CodexSearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// CodexSearchResponse is the provider-neutral subset needed by web_search.
type CodexSearchResponse struct {
	Summary string
	Results []CodexSearchResult
}

// CodexSearchBackend is a provider-bound Codex web-search client. The
// implementation owns Codex wire/auth details; callers only provide the
// active turn identity and query.
type CodexSearchBackend interface {
	Search(ctx context.Context, request CodexSearchRequest) (CodexSearchResponse, error)
}

// CodexSearchFactory creates a Codex search backend using the credential
// selected for the active turn. Implementations must not log the credential.
type CodexSearchFactory func(context.Context, *domain.Provider, string) (CodexSearchBackend, error)

// CodexSearchExecutor is the application boundary used by Toolbox to resolve
// the active Codex account and invoke its provider-bound search backend.
type CodexSearchExecutor interface {
	SearchCodexWeb(context.Context, CodexSearchRequest) (CodexSearchResponse, error)
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
