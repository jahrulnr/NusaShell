package mcpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"nusashell/domain"
)

// newTestServer builds an MCP server exposing a single "ping" tool and
// serves it over the given transport kind ("http" or "sse").
func newTestServer(t *testing.T, transport string, guard func(*http.Request) bool) (*httptest.Server, *domain.Plugin) {
	t.Helper()
	srv := mcpserver.NewMCPServer("test", "1.0.0",
		mcpserver.WithToolCapabilities(true),
	)
	ping := mcp.NewTool("ping", mcp.WithDescription("health check"))
	srv.AddTool(ping, func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})

	var handler http.Handler
	switch transport {
	case "http":
		handler = mcpserver.NewStreamableHTTPServer(srv)
	case "sse":
		handler = mcpserver.NewSSEServer(srv)
	default:
		t.Fatalf("unsupported test transport %q", transport)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if guard != nil && !guard(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)

	path := "/mcp"
	if transport == "sse" {
		path = "/sse"
	}
	plugin := &domain.Plugin{Manifest: domain.PluginManifest{
		ID:   "test-" + transport,
		Name: "test",
		MCP: domain.PluginMCPConfig{
			Transport: domain.PluginTransport(transport),
			URL:       ts.URL + path,
		},
	}}
	return ts, plugin
}

func testPluginWithHeaders(p *domain.Plugin, headers map[string]string) *domain.Plugin {
	p.Manifest.MCP.Headers = headers
	return p
}

func assertPingTool(t *testing.T, m *Manager, p *domain.Plugin) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := m.Connect(ctx, p)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	found := false
	for _, tool := range tools {
		if tool.Name == "ping" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tools = %v, want ping", tools)
	}
	m.Drop(p.Manifest.MCPServerID())
}

func TestConnectStreamableHTTP(t *testing.T) {
	_, plugin := newTestServer(t, "http", nil)
	assertPingTool(t, NewManager(), plugin)
}

func TestConnectSSE(t *testing.T) {
	_, plugin := newTestServer(t, "sse", nil)
	assertPingTool(t, NewManager(), plugin)
}

// Headers from the manifest must be sent with every HTTP request. The
// guard rejects unauthenticated requests, so a successful Connect proves
// the header was propagated.
func TestConnectSendsHeaders(t *testing.T) {
	guard := func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer tok"
	}
	_, plugin := newTestServer(t, "http", guard)
	m := NewManager()
	if _, err := m.Connect(context.Background(), plugin); err == nil {
		t.Fatal("connect without headers must fail the auth guard")
	}
	m.Drop(plugin.Manifest.MCPServerID())

	assertPingTool(t, m, testPluginWithHeaders(plugin, map[string]string{"Authorization": "Bearer tok"}))
}

func TestConnectRejectsUnsupportedTransport(t *testing.T) {
	plugin := &domain.Plugin{Manifest: domain.PluginManifest{
		ID: "bad", Name: "bad",
		MCP: domain.PluginMCPConfig{Transport: "websocket", URL: "http://localhost/x"},
	}}
	m := NewManager()
	_, err := m.Connect(context.Background(), plugin)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err = %v, want unsupported transport", err)
	}
}
