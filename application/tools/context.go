package tools

import "context"

// Canonical tool-execution context keys. application.WithConversationID and
// friends must wrap these helpers rather than declaring a second ctxKey;
// Go compares context keys by type, and a duplicate type makes todo /
// memory_project miss the conversation id and workspace the turn loop set.

type ctxKey string

const conversationIDKey ctxKey = "conversation_id"
const runIDKey ctxKey = "run_id"
const toolCallIDKey ctxKey = "tool_call_id"
const workspaceKey ctxKey = "workspace"

// WithConversationID returns a new context that carries the conversation id
// so conversation-scoped tools (todo) can access it.
func WithConversationID(ctx context.Context, conversationID string) context.Context {
	return context.WithValue(ctx, conversationIDKey, conversationID)
}

// ConversationIDFromContext returns the conversation id stored in ctx, or ""
// when no id is present (e.g. ad-hoc tool calls outside a turn).
func ConversationIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(conversationIDKey).(string); ok {
		return v
	}
	return ""
}

// WithRunID returns a new context that carries the turn run id so barrier
// tools (ask_question) can key their pending state by run.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey, runID)
}

// RunIDFromContext returns the run id stored in ctx, or "" when no id is
// present (e.g. ad-hoc tool calls outside a turn).
func RunIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(runIDKey).(string); ok {
		return v
	}
	return ""
}

// WithToolCallID returns a new context that carries the tool call id so
// barrier tools (ask_question) can key their pending state by call.
func WithToolCallID(ctx context.Context, callID string) context.Context {
	return context.WithValue(ctx, toolCallIDKey, callID)
}

// ToolCallIDFromContext returns the tool call id stored in ctx, or "".
func ToolCallIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(toolCallIDKey).(string); ok {
		return v
	}
	return ""
}

// WithWorkspace returns a new context that carries the turn workspace path
// so project-memory tools can key entries without re-reading the conversation.
func WithWorkspace(ctx context.Context, workspace string) context.Context {
	return context.WithValue(ctx, workspaceKey, workspace)
}

// WorkspaceFromContext returns the workspace path stored in ctx, or "".
func WorkspaceFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(workspaceKey).(string); ok {
		return v
	}
	return ""
}
