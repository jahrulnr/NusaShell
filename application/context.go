package application

import "context"

type ctxKey string

// conversationIDKey carries the active conversation id through the tool
// execution context so conversation-scoped tools (todo) can read it without
// changing the ToolExecutor.Execute signature.
const conversationIDKey ctxKey = "conversation_id"

// runIDKey carries the active turn run id through the tool execution context
// so barrier tools (ask_question) can key their pending state.
const runIDKey ctxKey = "run_id"

// toolCallIDKey carries the current tool call id through the tool execution
// context so barrier tools (ask_question) can key their pending state by call.
const toolCallIDKey ctxKey = "tool_call_id"

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
