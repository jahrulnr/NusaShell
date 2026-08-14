package application

import "context"

type ctxKey string

// conversationIDKey carries the active conversation id through the tool
// execution context so conversation-scoped tools (todo) can read it without
// changing the ToolExecutor.Execute signature.
const conversationIDKey ctxKey = "conversation_id"

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
