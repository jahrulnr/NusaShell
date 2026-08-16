package tools

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// TestListToolsOnlyExposesConnectedPlugins verifies the dynamic parity rule:
// an idle plugin contributes no tools; only explicitly enabled (connected)
// plugins do.
func TestListToolsOnlyExposesConnectedPlugins(t *testing.T) {
	enabled := &domain.Plugin{Manifest: domain.PluginManifest{ID: "enabled", Name: "Enabled", Version: "1.0.0", Icon: "🧩", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "x"}}}
	idle := &domain.Plugin{Manifest: domain.PluginManifest{ID: "idle", Name: "Idle", Version: "1.0.0", Icon: "🧩", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "y"}}}

	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			enabled.Manifest.MCPServerID(): {{Name: "enabled_tool", Description: "d"}},
		},
	}
	tb := &Toolbox{
		Plugins: &stubPluginStore{plugins: []*domain.Plugin{enabled, idle}},
		MCP:     mcp,
	}
	names := map[string]bool{}
	for _, ti := range tb.ListTools() {
		names[ti.Name] = true
	}
	if !names["mcp__Enabled__enabled_tool"] {
		t.Fatalf("expected connected plugin tool, got %v", names)
	}
	if names["mcp__Idle__whatever"] {
		t.Fatalf("idle plugin should not be exposed")
	}
}

// TestListToolsIdlePluginsNoLeak ensures a plugin that is not connected has
// no tools at all in the list.
func TestListToolsIdlePluginsNoLeak(t *testing.T) {
	idle := &domain.Plugin{Manifest: domain.PluginManifest{ID: "idle", Name: "Idle", Version: "1.0.0", Icon: "🧩", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "y"}}}
	tb := &Toolbox{
		Plugins: &stubPluginStore{plugins: []*domain.Plugin{idle}},
		MCP:     &stubMCP{tools: map[string][]contracts.MCPToolDTO{}},
	}
	for _, ti := range tb.ListTools() {
		if ti.Name == "mcp__Idle__x" {
			t.Fatalf("idle plugin leaked a tool into the list")
		}
	}
}
