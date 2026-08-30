package mcpclient

import (
	"context"
	"os"
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
// TestManager_NotificationHandler with GO_WANT_HELPER=1, it runs a real
// mcp-go server over stdio that pushes a notifications/message when its
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
		mcp.WithDescription("push a message notification"),
	), func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		srv.SendNotificationToAllClients(NotificationMessage, map[string]any{
			"plugin":     "test.helper",
			"event":      "message",
			"chat_id":    "999",
			"message_id": "7",
			"chat_type":  "dm",
			"subject":    "Tester",
			"text":       "hello over stdio",
			"from_me":    false,
		})
		return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("ok")}}, nil
	})
	if err := server.ServeStdio(srv); err != nil {
		t.Fatalf("serve stdio: %v", err)
	}
}

func TestManager_NotificationHandler(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER") == "1" {
		t.Skip("helper process mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got := make(chan mcp.JSONRPCNotification, 1)
	mgr := NewManager()
	mgr.SetNotificationHandler(func(serverID string, n mcp.JSONRPCNotification) {
		if n.Method == NotificationMessage {
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
		if attrs["chat_id"] != "999" || attrs["chat_type"] != "dm" {
			t.Errorf("notification params = %#v", attrs)
		}
		if ev, ok := NotificationToEvent(serverID, n); !ok || ev.Type != "test.helper.message" {
			t.Errorf("translated event = %+v (ok=%v)", ev, ok)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("notification handler never fired")
	}
}
