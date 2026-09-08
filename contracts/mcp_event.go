package contracts

import "encoding/json"

const (
	// MCPEventNotificationMethod carries a NusaShell business event from an
	// MCP plugin to the host. It is intentionally namespaced instead of
	// overloading an MCP standard notification method.
	MCPEventNotificationMethod = "notifications/nusashell/event"

	// MCPMessageNotificationMethod is the legacy messaging bridge method.
	// It is retained only for compatibility with older message plugins and
	// must not be used as the generic business-event envelope.
	MCPMessageNotificationMethod = "notifications/message"

	// MCPEventSchemaVersion is the current version of the generic event
	// notification envelope.
	MCPEventSchemaVersion = 1
)

// MCPEventNotification is the wire shape for a business event pushed by an
// MCP plugin. Source is assigned by the host from the connected server ID and
// is therefore deliberately absent from this plugin-supplied payload.
type MCPEventNotification struct {
	SchemaVersion int             `json:"schema_version"`
	EventID       string          `json:"event_id"`
	Type          string          `json:"type"`
	OccurredAt    string          `json:"occurred_at,omitempty"`
	Subject       string          `json:"subject,omitempty"`
	Attributes    map[string]any  `json:"attributes,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
}
