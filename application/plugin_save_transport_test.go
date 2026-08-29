package application

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// handlePluginSave must keep the legacy wire contract working: a request
// without transport stays stdio.
func TestHandlePluginSaveDefaultsToStdio(t *testing.T) {
	store := &autostartPluginStore{}
	mcp := &recordingMCP{}
	app := NewApp(Deps{Plugins: store, MCPToolbox: mcp, Logs: &fakeLogStore{}})

	res, err := app.handlePluginSave(contracts.PluginSaveRequest{
		Name: "fs", Command: "npx", Args: []string{"-y", "mcp-server-fs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := store.plugins[0]
	if plugin.Manifest.MCP.Transport != domain.PluginTransportStdio {
		t.Fatalf("transport = %q, want stdio", plugin.Manifest.MCP.Transport)
	}
	if plugin.Manifest.MCP.Command != "npx" {
		t.Fatalf("command = %q, want npx", plugin.Manifest.MCP.Command)
	}
	dto := res.(contracts.PluginListResult).Plugins[0]
	if dto.Manifest.MCP.Transport != "stdio" {
		t.Fatalf("dto transport = %q, want stdio", dto.Manifest.MCP.Transport)
	}
}

func TestHandlePluginSaveHTTPTransport(t *testing.T) {
	store := &autostartPluginStore{}
	mcp := &recordingMCP{}
	app := NewApp(Deps{Plugins: store, MCPToolbox: mcp, Logs: &fakeLogStore{}})

	res, err := app.handlePluginSave(contracts.PluginSaveRequest{
		Name:      "remote",
		Transport: "http",
		URL:       "https://mcp.example.com/mcp",
		Headers:   map[string]string{"Authorization": "Bearer tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := store.plugins[0]
	if plugin.Manifest.MCP.Transport != domain.PluginTransportHTTP {
		t.Fatalf("transport = %q, want http", plugin.Manifest.MCP.Transport)
	}
	if plugin.Manifest.MCP.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("url = %q", plugin.Manifest.MCP.URL)
	}
	if plugin.Manifest.MCP.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("headers not persisted: %v", plugin.Manifest.MCP.Headers)
	}
	dto := res.(contracts.PluginListResult).Plugins[0]
	if dto.Manifest.MCP.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("dto url = %q, want the stored url", dto.Manifest.MCP.URL)
	}
	if dto.Manifest.MCP.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("dto headers missing: %v", dto.Manifest.MCP.Headers)
	}
}

func TestHandlePluginSaveSSETransport(t *testing.T) {
	store := &autostartPluginStore{}
	mcp := &recordingMCP{}
	app := NewApp(Deps{Plugins: store, MCPToolbox: mcp, Logs: &fakeLogStore{}})

	if _, err := app.handlePluginSave(contracts.PluginSaveRequest{
		Name: "legacy", Transport: "sse", URL: "http://localhost:9999/sse",
	}); err != nil {
		t.Fatal(err)
	}
	plugin := store.plugins[0]
	if plugin.Manifest.MCP.Transport != domain.PluginTransportSSE {
		t.Fatalf("transport = %q, want sse", plugin.Manifest.MCP.Transport)
	}
}

func TestHandlePluginSaveRemoteRequiresURL(t *testing.T) {
	for _, transport := range []string{"sse", "http"} {
		store := &autostartPluginStore{}
		app := NewApp(Deps{Plugins: store, MCPToolbox: &recordingMCP{}, Logs: &fakeLogStore{}})
		_, rpcErr := app.handlePluginSave(contracts.PluginSaveRequest{
			Name: "remote", Transport: transport,
		})
		if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
			t.Fatalf("transport %s without url: got %v, want validation error", transport, rpcErr)
		}
	}
}

func TestHandlePluginSaveStdioRequiresCommand(t *testing.T) {
	store := &autostartPluginStore{}
	app := NewApp(Deps{Plugins: store, MCPToolbox: &recordingMCP{}, Logs: &fakeLogStore{}})
	_, rpcErr := app.handlePluginSave(contracts.PluginSaveRequest{Name: "fs"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("stdio without command: got %v, want validation error", rpcErr)
	}
}

// Saving a previously-stdio server as a remote one must clean the stale
// stdio fields instead of leaving both command and url in the manifest.
func TestHandlePluginSaveSwitchesTransportCleansStaleFields(t *testing.T) {
	store := &autostartPluginStore{plugins: []*domain.Plugin{pluginWithAutostart("srv", "srv", false)}}
	app := NewApp(Deps{Plugins: store, MCPToolbox: &recordingMCP{}, Logs: &fakeLogStore{}})

	if _, err := app.handlePluginSave(contracts.PluginSaveRequest{
		ID: "srv", Name: "srv", Transport: "http", URL: "https://mcp.example.com/mcp",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := store.plugins[0].Manifest.MCP
	if cfg.Command != "" || len(cfg.Args) != 0 {
		t.Fatalf("stale stdio fields kept: command=%q args=%v", cfg.Command, cfg.Args)
	}
	if cfg.URL == "" {
		t.Fatal("url missing after transport switch")
	}
}

// An update request that omits transport must not silently degrade a remote
// server back to stdio.
func TestHandlePluginSaveOmittedTransportKeepsExistingRemote(t *testing.T) {
	store := &autostartPluginStore{plugins: []*domain.Plugin{{
		Manifest: domain.PluginManifest{
			ID: "srv", Name: "srv", Version: "0.1.0", Icon: "🧩",
			MCP: domain.PluginMCPConfig{
				Transport: domain.PluginTransportHTTP,
				URL:       "https://mcp.example.com/mcp",
			},
		},
	}}}
	app := NewApp(Deps{Plugins: store, MCPToolbox: &recordingMCP{}, Logs: &fakeLogStore{}})

	if _, err := app.handlePluginSave(contracts.PluginSaveRequest{ID: "srv", Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	cfg := store.plugins[0].Manifest.MCP
	if cfg.Transport != domain.PluginTransportHTTP || cfg.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("remote config degraded: %+v", cfg)
	}
}
