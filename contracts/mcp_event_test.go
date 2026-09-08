package contracts

import (
	"encoding/json"
	"testing"
)

func TestMCPEventNotificationGolden(t *testing.T) {
	assertGolden(t, "mcp-event.json", MCPEventNotification{
		SchemaVersion: MCPEventSchemaVersion,
		EventID:       "delivery-123",
		Type:          "github.pull_request",
		OccurredAt:    "2026-09-08T10:00:00Z",
		Subject:       "owner/repo#42",
		Attributes:    map[string]any{"action": "opened"},
		Data:          json.RawMessage(`{"head_sha":"abc123"}`),
	})
}

func TestMCPEventNotificationOmitsOptionalFields(t *testing.T) {
	got, err := json.Marshal(MCPEventNotification{
		SchemaVersion: MCPEventSchemaVersion,
		EventID:       "event-1",
		Type:          "trading.price_alert",
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	want := `{"schema_version":1,"event_id":"event-1","type":"trading.price_alert"}`
	if string(got) != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
