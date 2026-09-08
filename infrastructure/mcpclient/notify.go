// Notification translation: MCP server→client notifications pushed by
// plugins become domain events for the automation engine (when-triggers).
package mcpclient

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

const (
	maxMCPEventStringBytes     = 4 << 10
	maxMCPEventAttributesBytes = 256 << 10
	maxMCPEventDataBytes       = 8 << 20

	// NotificationEvent is the NusaShell business-event push contract. Its
	// params are a contracts.MCPEventNotification envelope.
	NotificationEvent = contracts.MCPEventNotificationMethod

	// NotificationMessage is the deprecated messaging bridge contract. It is
	// retained for older message plugins only; it is not a generic event
	// envelope and must not be used for trading, GitHub, or other business
	// events.
	//
	// Deprecated: use NotificationEvent for business events.
	NotificationMessage = contracts.MCPMessageNotificationMethod
)

// NotificationToEvent converts a business-event or legacy messaging MCP
// notification into a domain.Event for automation when-triggers. It returns
// false for unknown methods or malformed payloads. Generic event IDs are
// namespaced by the connected server so duplicate deliveries collapse to one
// workflow delivery without trusting a plugin-supplied source.
func NotificationToEvent(serverID string, n mcp.JSONRPCNotification) (domain.Event, bool) {
	switch n.Method {
	case NotificationEvent:
		return genericEventToEvent(serverID, notificationPayload(n))
	case NotificationMessage:
		return legacyMessageToEvent(serverID, notificationPayload(n))
	default:
		return domain.Event{}, false
	}
}

func genericEventToEvent(serverID string, p map[string]any) (domain.Event, bool) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" || !knownGenericEventFields(p) {
		return domain.Event{}, false
	}
	version, ok := intField(p, "schema_version")
	if !ok || version != contracts.MCPEventSchemaVersion {
		return domain.Event{}, false
	}
	eventID, ok := optionalStringField(p, "event_id", maxMCPEventStringBytes)
	if !ok {
		return domain.Event{}, false
	}
	eventType, ok := optionalStringField(p, "type", maxMCPEventStringBytes)
	if !ok {
		return domain.Event{}, false
	}
	if eventID == "" || eventType == "" {
		return domain.Event{}, false
	}

	attrs, ok := objectField(p, "attributes")
	if !ok {
		return domain.Event{}, false
	}
	// The host-owned identity is exposed for prompt/filter use, and cannot be
	// overridden by a publisher attribute with the same name.
	attrs["event_id"] = eventID

	data := json.RawMessage(`{}`)
	if _, present := p["data"]; present {
		var valid bool
		data, valid = rawJSONField(p, "data")
		if !valid {
			return domain.Event{}, false
		}
	}
	occurredAt := clock.NewTime().Time()
	occurredAtText, ok := optionalStringField(p, "occurred_at", maxMCPEventStringBytes)
	if !ok {
		return domain.Event{}, false
	}
	if occurredAtText != "" {
		parsed, err := time.Parse(time.RFC3339Nano, occurredAtText)
		if err != nil {
			return domain.Event{}, false
		}
		occurredAt = parsed
	}

	subject, ok := optionalStringField(p, "subject", maxMCPEventStringBytes)
	if !ok {
		return domain.Event{}, false
	}

	return domain.Event{
		ID:         fmt.Sprintf("mcp:%s:%s", serverID, eventID),
		Type:       eventType,
		Source:     serverID,
		Time:       clock.NewTime(occurredAt).Time(),
		Subject:    subject,
		Attributes: attrs,
		Data:       data,
	}, true
}

func knownGenericEventFields(p map[string]any) bool {
	for key := range p {
		switch key {
		case "schema_version", "event_id", "type", "occurred_at", "subject", "attributes", "data":
		default:
			return false
		}
	}
	return true
}

// notificationPayload returns the notification params as an attribute map.
func notificationPayload(n mcp.JSONRPCNotification) map[string]any {
	if n.Params.AdditionalFields == nil {
		return map[string]any{}
	}
	return n.Params.AdditionalFields
}

func legacyMessageToEvent(serverID string, p map[string]any) (domain.Event, bool) {
	// notifications/message is an MCP logging channel. A logging-shaped
	// payload must never be admitted as a legacy business message merely
	// because it also contains chat fields.
	if _, loggingNotification := p["level"]; loggingNotification {
		return domain.Event{}, false
	}
	serverID = strings.TrimSpace(serverID)
	plugin := strField(p, "plugin")
	event := strField(p, "event")
	chatID := strField(p, "chat_id")
	messageID := strField(p, "message_id")
	if serverID == "" || plugin == "" || event == "" || chatID == "" || messageID == "" || normalizePluginID(serverID) != normalizePluginID(plugin) {
		return domain.Event{}, false
	}
	fromMe, ok := p["from_me"].(bool)
	if !ok {
		// Provenance is security-sensitive. Do not guess when an adapter omits
		// it or serializes it with the wrong type, otherwise an outbound bot
		// message could be treated as an inbound automation event.
		return domain.Event{}, false
	}
	if fromMe {
		// Outbound (bot) messages never trigger Automation workflows.
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

func normalizePluginID(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "plugin:")
}

func strField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func intField(m map[string]any, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), uint64(n) <= uint64(^uint(0)>>1)
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), uint64(n) <= uint64(^uint(0)>>1)
	case uint64:
		return int(n), n <= uint64(^uint(0)>>1)
	case float64:
		i := int(n)
		return i, n >= 0 && n == float64(i)
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil && int64(int(i)) == i && i >= 0
	default:
		return 0, false
	}
}

func optionalStringField(m map[string]any, key string, maxBytes int) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", true
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	return s, len(s) <= maxBytes
}

func objectField(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok {
		return map[string]any{}, true
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal(b, &object); err != nil || object == nil {
		return nil, false
	}
	if len(b) > maxMCPEventAttributesBytes {
		return nil, false
	}
	return object, true
}

func rawJSONField(m map[string]any, key string) (json.RawMessage, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) > maxMCPEventDataBytes || !json.Valid(b) {
		return nil, false
	}
	return json.RawMessage(append([]byte(nil), b...)), true
}
