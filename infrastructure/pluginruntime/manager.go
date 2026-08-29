// Package pluginruntime manages the lifecycle of installed plugins:
// starting/stopping their MCP servers and routing tool calls from
// plugin UIs to the MCP server.
//
// It wraps the existing mcpclient.Manager, which operates directly on a
// plugin's manifest MCP config, so the MCP connection logic stays in one
// place.
package pluginruntime

import (
	"context"
	"encoding/json"
	"fmt"

	"nusashell/contracts"
	"nusashell/infrastructure/mcpclient"
	"nusashell/infrastructure/pluginfs"

	"github.com/mark3labs/mcp-go/mcp"
)

// Manager owns the plugin store + MCP connection manager. It is safe
// for concurrent use.
type Manager struct {
	store *pluginfs.Store
	mcp   *mcpclient.Manager
}

// New creates a runtime manager backed by the given store and MCP
// manager.
func New(store *pluginfs.Store, mcp *mcpclient.Manager) *Manager {
	return &Manager{
		store: store,
		mcp:   mcp,
	}
}

// EnsureStarted starts the plugin's MCP server if it is not already
// running. Returns the available tools.
func (m *Manager) EnsureStarted(ctx context.Context, pluginID string) ([]contracts.MCPToolDTO, error) {
	plugin, err := m.store.Get(pluginID)
	if err != nil {
		return nil, err
	}
	tools, err := m.mcp.Connect(ctx, plugin)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", pluginID, err)
	}
	return tools, nil
}

// CallToolRaw routes a tool call from the plugin UI to the plugin's MCP
// server and returns the full MCP CallToolResult, including StructuredContent
// and IsError. The plugin is started if it is not already running. The
// caller is responsible for inspecting IsError and forwarding the result
// shape the UI expects (content + structuredContent).
func (m *Manager) CallToolRaw(ctx context.Context, pluginID, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
	plugin, err := m.store.Get(pluginID)
	if err != nil {
		return nil, err
	}
	if _, err := m.mcp.Connect(ctx, plugin); err != nil {
		return nil, fmt.Errorf("plugin %s: %w", pluginID, err)
	}
	return m.mcp.CallToolRaw(ctx, plugin.Manifest.MCPServerID(), toolName, args)
}

// CallTool is the application.PluginRuntimePort adapter: it calls
// CallToolRaw and converts the mcp-go result to contracts.PluginToolResult
// so transport does not need to import the MCP SDK. Text content parts are
// normalized to { type: "text", text: string }; non-text parts fall back
// to the SDK's JSON marshalling so no part is dropped.
func (m *Manager) CallTool(ctx context.Context, pluginID, toolName string, args map[string]any) (*contracts.PluginToolResult, error) {
	result, err := m.CallToolRaw(ctx, pluginID, toolName, args)
	if result == nil {
		return nil, err
	}
	out := &contracts.PluginToolResult{
		Content: make([]any, 0, len(result.Content)),
		IsError: result.IsError,
	}
	for _, c := range result.Content {
		if t, ok := c.(mcp.TextContent); ok {
			out.Content = append(out.Content, map[string]any{"type": "text", "text": t.Text})
			continue
		}
		if raw, jerr := json.Marshal(c); jerr == nil {
			var decoded any
			if json.Unmarshal(raw, &decoded) == nil {
				out.Content = append(out.Content, decoded)
			}
		}
	}
	if result.StructuredContent != nil {
		out.StructuredContent = result.StructuredContent
	}
	return out, err
}

// ListTools returns the tools advertised by a running plugin. Returns
// nil if the plugin is not running.
func (m *Manager) ListTools(pluginID string) []contracts.MCPToolDTO {
	serverID := "plugin:" + pluginID
	tools, ok := m.mcp.ToolsFor(serverID)
	if !ok {
		return nil
	}
	return tools
}

// Stop closes the MCP connection for a plugin.
func (m *Manager) Stop(pluginID string) {
	m.mcp.Drop("plugin:" + pluginID)
}
