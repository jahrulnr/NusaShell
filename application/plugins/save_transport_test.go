package plugins

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

func testService(store Store, mcp Toolbox) *Service {
	return New(Deps{Store: store, MCP: mcp, Log: func(string, string, string, ...any) {}})
}

func TestHandleSaveDefaultsToStdio(t *testing.T) {
	store := &memStore{}
	mcp := &recordingMCP{}
	svc := testService(store, mcp)

	res, err := svc.handleSave(contracts.PluginSaveRequest{
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

func TestHandleSaveHTTPTransport(t *testing.T) {
	store := &memStore{}
	mcp := &recordingMCP{}
	svc := testService(store, mcp)

	res, err := svc.handleSave(contracts.PluginSaveRequest{
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

func TestHandleSaveSSETransport(t *testing.T) {
	store := &memStore{}
	mcp := &recordingMCP{}
	svc := testService(store, mcp)

	if _, err := svc.handleSave(contracts.PluginSaveRequest{
		Name: "legacy", Transport: "sse", URL: "http://localhost:10994/sse",
	}); err != nil {
		t.Fatal(err)
	}
	plugin := store.plugins[0]
	if plugin.Manifest.MCP.Transport != domain.PluginTransportSSE {
		t.Fatalf("transport = %q, want sse", plugin.Manifest.MCP.Transport)
	}
}

func TestHandleSaveRemoteRequiresURL(t *testing.T) {
	for _, transport := range []string{"sse", "http"} {
		store := &memStore{}
		svc := testService(store, &recordingMCP{})
		_, rpcErr := svc.handleSave(contracts.PluginSaveRequest{
			Name: "remote", Transport: transport,
		})
		if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
			t.Fatalf("transport %s without url: got %v, want validation error", transport, rpcErr)
		}
	}
}

func TestHandleSaveStdioRequiresCommand(t *testing.T) {
	store := &memStore{}
	svc := testService(store, &recordingMCP{})
	_, rpcErr := svc.handleSave(contracts.PluginSaveRequest{Name: "fs"})
	if rpcErr == nil || rpcErr.Code != contracts.CodeValidation {
		t.Fatalf("stdio without command: got %v, want validation error", rpcErr)
	}
}

func TestHandleSaveSwitchesTransportCleansStaleFields(t *testing.T) {
	store := &memStore{plugins: []*domain.Plugin{pluginWithAutostart("srv", "srv", false)}}
	svc := testService(store, &recordingMCP{})

	if _, err := svc.handleSave(contracts.PluginSaveRequest{
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

func TestHandleSaveOmittedTransportKeepsExistingRemote(t *testing.T) {
	store := &memStore{plugins: []*domain.Plugin{{
		Manifest: domain.PluginManifest{
			ID: "srv", Name: "srv", Version: "0.1.0", Icon: "🧩",
			MCP: domain.PluginMCPConfig{
				Transport: domain.PluginTransportHTTP,
				URL:       "https://mcp.example.com/mcp",
			},
		},
	}}}
	svc := testService(store, &recordingMCP{})

	if _, err := svc.handleSave(contracts.PluginSaveRequest{ID: "srv", Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	cfg := store.plugins[0].Manifest.MCP
	if cfg.Transport != domain.PluginTransportHTTP || cfg.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("remote config degraded: %+v", cfg)
	}
}
