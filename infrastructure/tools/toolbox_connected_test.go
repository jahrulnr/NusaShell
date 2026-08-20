package tools

import (
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// TestListToolsDoesNotExposeMCPDynamicTools verifies that MCP plugin tools
// are NOT advertised to the agent. The tool list must stay stable for the
// lifetime of a conversation so the provider prompt cache (OpenAI / Claude)
// is not invalidated. The agent can inspect MCP servers via mcp_list,
// tool_list, mcp_search, and tool_schema, but cannot call
// mcp__<server>__<tool> directly.
func TestListToolsDoesNotExposeMCPDynamicTools(t *testing.T) {
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
	for _, ti := range tb.ListTools() {
		if ti.Name == "mcp__Enabled__enabled_tool" {
			t.Fatalf("connected plugin tool should not be advertised to the agent")
		}
		if ti.Name == "mcp__Idle__whatever" {
			t.Fatalf("idle plugin should not be exposed")
		}
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
