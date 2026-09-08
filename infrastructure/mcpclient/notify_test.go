package mcpclient

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"nusashell/domain"
)

// --- unit: NotificationToEvent ---------------------------------------------

func TestNotificationToEvent_Message(t *testing.T) {
	n := mcp.JSONRPCNotification{
		JSONRPC: "2.0",
		Notification: mcp.Notification{
			Method: NotificationMessage,
			Params: mcp.NotificationParams{AdditionalFields: map[string]any{
				"plugin":     "nusashell.telegram",
				"event":      "message",
				"chat_id":    "520213916",
				"message_id": "42",
				"chat_type":  "dm",
				"subject":    "Jahrul",
				"text":       "halo",
				"from_me":    false,
			}},
		},
	}
	ev, ok := NotificationToEvent("nusashell.telegram", n)
	if !ok {
		t.Fatal("expected event, got none")
	}
	if ev.Type != "telegram.message" {
		t.Errorf("Type = %q, want telegram.message", ev.Type)
	}
	if ev.Source != "nusashell.telegram" {
		t.Errorf("Source = %q", ev.Source)
	}
	if ev.Subject != "Jahrul" {
		t.Errorf("Subject = %q", ev.Subject)
	}
	if ev.Attributes["chat_id"] != "520213916" || ev.Attributes["chat_type"] != "dm" {
		t.Errorf("Attributes = %#v", ev.Attributes)
	}
	wantID := "mcp:nusashell.telegram:message:520213916:42"
	if ev.ID != wantID {
		t.Errorf("ID = %q, want %q (deterministic for dedup)", ev.ID, wantID)
	}
	// from_me=true payloads never become events.
	n.Params.AdditionalFields["from_me"] = true
	if _, ok := NotificationToEvent("nusashell.telegram", n); ok {
		t.Error("from_me=true must not produce an event")
	}
}

func TestNotificationToEvent_LegacyMessageRequiresServerPluginMatch(t *testing.T) {
	n := mcp.JSONRPCNotification{Notification: mcp.Notification{
		Method: NotificationMessage,
		Params: mcp.NotificationParams{AdditionalFields: map[string]any{
			"plugin":     "nusashell.telegram",
			"event":      "message",
			"chat_id":    "520213916",
			"message_id": "42",
			"from_me":    false,
		}},
	}}
	if _, ok := NotificationToEvent("plugin:another-source", n); ok {
		t.Fatal("legacy message from another plugin must not be admitted")
	}
}

func TestNotificationToEventRejectsMissingIdentityOrProvenance(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"plugin":     "nusashell.telegram",
			"event":      "message",
			"chat_id":    "520213916",
			"message_id": "42",
			"from_me":    false,
		}
	}
	cases := map[string]func(map[string]any){
		"missing message id": func(p map[string]any) { delete(p, "message_id") },
		"empty message id":   func(p map[string]any) { p["message_id"] = "  " },
		"missing from_me":    func(p map[string]any) { delete(p, "from_me") },
		"wrong from_me type": func(p map[string]any) {
			p["from_me"] = "false"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := base()
			mutate(p)
			n := mcp.JSONRPCNotification{Notification: mcp.Notification{
				Method: NotificationMessage,
				Params: mcp.NotificationParams{AdditionalFields: p},
			}}
			if _, ok := NotificationToEvent("plugin:nusashell.telegram", n); ok {
				t.Fatal("malformed identity/provenance must not produce an event")
			}
		})
	}
}

func TestNotificationToEvent_UnknownMethodOrMalformed(t *testing.T) {
	if _, ok := NotificationToEvent("nusashell.telegram", mcp.JSONRPCNotification{Notification: mcp.Notification{Method: "notifications/whatever"}}); ok {
		t.Error("unknown method must be ignored")
	}
	n := mcp.JSONRPCNotification{
		Notification: mcp.Notification{
			Method: NotificationMessage,
			Params: mcp.NotificationParams{AdditionalFields: map[string]any{"plugin": "nusashell.telegram"}},
		},
	}
	if _, ok := NotificationToEvent("nusashell.telegram", n); ok {
		t.Error("missing chat_id must not produce an event")
	}
}

// --- integration: real stdio round-trip -------------------------------------

// TestManagerHelperProcess is the child process entry point. When spawned by
// TestManager_GenericNotificationHandler with GO_WANT_HELPER=1, it runs a real
// mcp-go server over stdio that pushes a NusaShell business event when its
// "push" tool is called — proving the Manager's stdio + OnNotification path.
func TestManagerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER") != "1" {
		t.Skip("helper process mode")
	}
	srv := server.NewMCPServer("helper", "1.0.0",
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)
	srv.AddTool(mcp.NewTool("push",
		mcp.WithDescription("push a business event notification"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		srv.SendNotificationToAllClients(NotificationEvent, map[string]any{
			"schema_version": 1,
			"event_id":       "helper-event-7",
			"type":           "test.helper.message",
			"subject":        "Tester",
			"attributes": map[string]any{
				"chat_id":   "999",
				"chat_type": "dm",
			},
			"data": map[string]any{
				"text": "hello over stdio",
			},
		})
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("ok")}}, nil
	})
	if err := server.ServeStdio(srv); err != nil {
		t.Fatalf("serve stdio: %v", err)
	}
}

func TestManager_GenericNotificationHandler(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER") == "1" {
		t.Skip("helper process mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got := make(chan mcp.JSONRPCNotification, 1)
	mgr := NewManager()
	mgr.SetNotificationHandler(func(serverID string, n mcp.JSONRPCNotification) {
		if n.Method == NotificationEvent {
			got <- n
		}
	})

	p := &domain.Plugin{
		Manifest: domain.PluginManifest{
			ID:      "test.helper",
			Name:    "Helper",
			Version: "1.0.0",
			Icon:    "icon.png",
			MCP: domain.PluginMCPConfig{
				Transport: domain.PluginTransportStdio,
				Command:   os.Args[0],
				Args:      []string{"-test.run", "^TestManagerHelperProcess$"},
				Env:       map[string]string{"GO_WANT_HELPER": "1"},
			},
		},
	}
	if _, err := mgr.Connect(ctx, p); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	serverID := p.Manifest.MCPServerID()
	t.Cleanup(func() { mgr.Drop(serverID) })

	if _, err := mgr.CallTool(ctx, serverID, "push", nil); err != nil {
		t.Fatalf("CallTool push: %v", err)
	}

	select {
	case n := <-got:
		attrs := notificationPayload(n)
		businessAttrs, ok := attrs["attributes"].(map[string]any)
		if !ok || businessAttrs["chat_id"] != "999" || businessAttrs["chat_type"] != "dm" {
			t.Errorf("notification params = %#v", attrs)
		}
		if ev, ok := NotificationToEvent(serverID, n); !ok || ev.Type != "test.helper.message" {
			t.Errorf("translated event = %+v (ok=%v)", ev, ok)
		} else if string(ev.Data) != `{"text":"hello over stdio"}` {
			t.Errorf("translated data = %s", ev.Data)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("notification handler never fired")
	}
}

func TestNotificationToEvent_GenericEvent(t *testing.T) {
	n := mcp.JSONRPCNotification{
		JSONRPC: "2.0",
		Notification: mcp.Notification{
			Method: NotificationEvent,
			Params: mcp.NotificationParams{AdditionalFields: map[string]any{
				"schema_version": 1,
				"event_id":       "delivery-123",
				"type":           "github.pull_request",
				"occurred_at":    "2026-09-08T10:00:00Z",
				"subject":        "owner/repo#42",
				"attributes": map[string]any{
					"action":     "opened",
					"repository": "owner/repo",
					"event_id":   "spoofed-id",
				},
				"data": map[string]any{
					"head_sha": "abc123",
				},
			}},
		},
	}
	got, ok := NotificationToEvent("plugin:github-events", n)
	if !ok {
		t.Fatal("expected generic event, got none")
	}
	if got.ID != "mcp:plugin:github-events:delivery-123" {
		t.Fatalf("ID = %q, want stable namespaced delivery ID", got.ID)
	}
	if got.Type != "github.pull_request" || got.Source != "plugin:github-events" {
		t.Fatalf("event identity = %+v", got)
	}
	if got.Subject != "owner/repo#42" || got.Attributes["action"] != "opened" {
		t.Fatalf("normalized event = %+v", got)
	}
	if got.Attributes["event_id"] != "delivery-123" {
		t.Fatalf("event_id attribute = %#v, want publisher identity", got.Attributes["event_id"])
	}
	if string(got.Data) != `{"head_sha":"abc123"}` {
		t.Fatalf("data = %s, want original JSON payload", got.Data)
	}
	wantTime, _ := time.Parse(time.RFC3339Nano, "2026-09-08T10:00:00Z")
	if !got.Time.Equal(wantTime) {
		t.Fatalf("time = %s, want %s", got.Time, wantTime)
	}
	duplicate, ok := NotificationToEvent("plugin:github-events", n)
	if !ok || duplicate.ID != got.ID {
		t.Fatalf("duplicate identity = %+v (ok=%v), want same ID %q", duplicate, ok, got.ID)
	}
	otherServer, ok := NotificationToEvent("plugin:other-source", n)
	if !ok || otherServer.ID == got.ID {
		t.Fatalf("server identity collision = %+v (ok=%v), want distinct ID", otherServer, ok)
	}
}

func TestNotificationToEvent_GenericAllowsOptionalPayloadFields(t *testing.T) {
	before := time.Now()
	n := mcp.JSONRPCNotification{Notification: mcp.Notification{
		Method: NotificationEvent,
		Params: mcp.NotificationParams{AdditionalFields: map[string]any{
			"schema_version": 1,
			"event_id":       "minimal-1",
			"type":           "trading.tick",
		}},
	}}
	got, ok := NotificationToEvent("plugin:trading", n)
	if !ok {
		t.Fatal("minimal generic event must be accepted")
	}
	if got.Subject != "" || string(got.Data) != `{}` {
		t.Fatalf("optional fields = subject %q data %s", got.Subject, got.Data)
	}
	if len(got.Attributes) != 1 || got.Attributes["event_id"] != "minimal-1" {
		t.Fatalf("default attributes = %#v", got.Attributes)
	}
	if got.Time.Before(before) || got.Time.After(time.Now()) {
		t.Fatalf("default event time = %s, outside ingestion window", got.Time)
	}
}

func TestNotificationToEvent_GenericRejectsInvalidEnvelope(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"schema_version": 1,
			"event_id":       "delivery-123",
			"type":           "trading.alert",
			"attributes":     map[string]any{"symbol": "BTCUSD"},
			"data":           map[string]any{"price": 64123.5},
		}
	}
	cases := map[string]func(map[string]any){
		"missing schema version":     func(p map[string]any) { delete(p, "schema_version") },
		"unsupported schema version": func(p map[string]any) { p["schema_version"] = 2 },
		"missing event id":           func(p map[string]any) { delete(p, "event_id") },
		"empty event id":             func(p map[string]any) { p["event_id"] = "  " },
		"wrong event id type":        func(p map[string]any) { p["event_id"] = 123 },
		"missing event type":         func(p map[string]any) { delete(p, "type") },
		"oversized event type":       func(p map[string]any) { p["type"] = strings.Repeat("x", maxMCPEventStringBytes+1) },
		"wrong attributes type":      func(p map[string]any) { p["attributes"] = "symbol=BTCUSD" },
		"oversized attributes": func(p map[string]any) {
			p["attributes"] = map[string]any{"blob": strings.Repeat("x", maxMCPEventAttributesBytes)}
		},
		"invalid data value":  func(p map[string]any) { p["data"] = func() {} },
		"oversized data":      func(p map[string]any) { p["data"] = strings.Repeat("x", maxMCPEventDataBytes) },
		"invalid occurred at": func(p map[string]any) { p["occurred_at"] = "not-a-time" },
		"unknown field":       func(p map[string]any) { p["plugin"] = "not-part-of-generic-contract" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := base()
			mutate(p)
			n := mcp.JSONRPCNotification{Notification: mcp.Notification{
				Method: NotificationEvent,
				Params: mcp.NotificationParams{AdditionalFields: p},
			}}
			if _, ok := NotificationToEvent("plugin:event-source", n); ok {
				t.Fatal("invalid generic envelope must not produce an event")
			}
		})
	}
}

func TestNotificationToEvent_IgnoresMCPLoggingNotification(t *testing.T) {
	n := mcp.JSONRPCNotification{Notification: mcp.Notification{
		Method: NotificationMessage,
		Params: mcp.NotificationParams{AdditionalFields: map[string]any{
			"level":      "info",
			"plugin":     "trading-provider",
			"event":      "message",
			"chat_id":    "520213916",
			"message_id": "42",
			"from_me":    false,
		}},
	}}
	if _, ok := NotificationToEvent("plugin:trading-provider", n); ok {
		t.Fatal("MCP logging notification must not become a business event")
	}
}
