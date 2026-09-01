// Notification translation: MCP server→client notifications pushed by
// plugins become domain events for the automation engine (when-triggers).
package mcpclient

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// Notification method names understood from plugins.
const (
	// NotificationMessage is the "a message arrived" push emitted by message
	// bridge plugins (telegram, whatsapp). Params (AdditionalFields):
	//
	//	plugin     string — host plugin id, e.g. "nusashell.telegram"
	//	event      string — event short name, e.g. "message"
	//	chat_id    string — chat identifier (int64-as-string / JID)
	//	message_id string — message identifier
	//	chat_type  string — dm | group | channel (or "" if unknown)
	//	subject    string — sender or chat display label
	//	text       string — truncated message text (full text lives in the store)
	//	from_me    bool   — false for inbound messages
	NotificationMessage = "notifications/message"
)

// NotificationToEvent converts an MCP notification into a domain.Event for
// automation when-triggers. It returns false for unknown methods or malformed
// payloads. Event IDs are deterministic from server+content so duplicate
// deliveries (e.g. reconnect re-ingest) collapse to one workflow run.
func NotificationToEvent(serverID string, n mcp.JSONRPCNotification) (domain.Event, bool) {
	switch n.Method {
	case NotificationMessage:
		return messageToEvent(serverID, notificationPayload(n))
	default:
		return domain.Event{}, false
	}
}

// notificationPayload returns the notification params as an attribute map.
func notificationPayload(n mcp.JSONRPCNotification) map[string]any {
	if n.Params.AdditionalFields == nil {
		return map[string]any{}
	}
	return n.Params.AdditionalFields
}

func messageToEvent(serverID string, p map[string]any) (domain.Event, bool) {
	plugin := strField(p, "plugin")
	event := strField(p, "event")
	chatID := strField(p, "chat_id")
	messageID := strField(p, "message_id")
	if plugin == "" || event == "" || chatID == "" {
		return domain.Event{}, false
	}
	if v, ok := p["from_me"].(bool); ok && v {
		// Outbound (bot) messages never trigger CI workflows.
		return domain.Event{}, false
	}

	short := strings.TrimPrefix(serverID, "plugin:")
	short = strings.TrimPrefix(short, "nusashell.")
	if short == "" {
		short = strings.TrimPrefix(plugin, "nusashell.")
	}
	evType := short + "." + event

	subject := strField(p, "subject")
	if subject == "" {
		subject = chatID
	}

	attrs := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"chat_type":  strField(p, "chat_type"),
		"subject":    subject,
		"from_me":    false,
	}
	if text := strField(p, "text"); text != "" {
		attrs["text"] = text
	}

	return domain.Event{
		ID:         fmt.Sprintf("mcp:%s:%s:%s:%s", serverID, event, chatID, messageID),
		Type:       evType,
		Source:     serverID,
		Subject:    subject,
		Time:       clock.NewTime().Time(),
		Attributes: attrs,
		Data:       json.RawMessage(`{}`),
	}, true
}

func strField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
