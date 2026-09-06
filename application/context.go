package application

import (
	"context"

	"nusashell/application/tools"
)

// Turn-tool context lives in application/tools so the agent package can
// annotate Execute without importing this root package. These wrappers must
// keep using those helpers — Go context keys are compared by type, so a
// second ctxKey here would make todo and memory_project see an empty
// conversation/workspace (the agent writes tools keys; this package is what
// infrastructure/tools reads).

// WithConversationID returns a new context that carries the conversation id
// so conversation-scoped tools (todo) can access it.
func WithConversationID(ctx context.Context, conversationID string) context.Context {
	return tools.WithConversationID(ctx, conversationID)
}

// ConversationIDFromContext returns the conversation id stored in ctx, or ""
// when no id is present (e.g. ad-hoc tool calls outside a turn).
func ConversationIDFromContext(ctx context.Context) string {
	return tools.ConversationIDFromContext(ctx)
}

// WithRunID returns a new context that carries the turn run id so barrier
// tools (ask_question) can key their pending state by run.
func WithRunID(ctx context.Context, runID string) context.Context {
	return tools.WithRunID(ctx, runID)
}

// RunIDFromContext returns the run id stored in ctx, or "" when no id is
// present (e.g. ad-hoc tool calls outside a turn).
func RunIDFromContext(ctx context.Context) string {
	return tools.RunIDFromContext(ctx)
}

// WithToolCallID returns a new context that carries the tool call id so
// barrier tools (ask_question) can key their pending state by call.
func WithToolCallID(ctx context.Context, callID string) context.Context {
	return tools.WithToolCallID(ctx, callID)
}

// ToolCallIDFromContext returns the tool call id stored in ctx, or "".
func ToolCallIDFromContext(ctx context.Context) string {
	return tools.ToolCallIDFromContext(ctx)
}

// WithWorkspace returns a new context that carries the turn workspace path
// so project-memory tools can key entries without re-reading the conversation.
func WithWorkspace(ctx context.Context, workspace string) context.Context {
	return tools.WithWorkspace(ctx, workspace)
}

// WorkspaceFromContext returns the workspace path stored in ctx, or "".
func WorkspaceFromContext(ctx context.Context) string {
	return tools.WorkspaceFromContext(ctx)
}
