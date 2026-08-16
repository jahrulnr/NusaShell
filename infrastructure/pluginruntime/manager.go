// Package pluginruntime manages the lifecycle of installed plugins:
// starting/stopping their MCP servers and routing tool calls from
// plugin UIs to the MCP server.
//
// It wraps the existing mcpclient.Manager, translating a plugin's
// manifest into a domain.MCPServer so the MCP connection logic stays
// in one place.
package pluginruntime

import (
	"context"
	"fmt"
	"sync"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/mcpclient"
	"nusashell/infrastructure/pluginfs"
)

// Manager owns the plugin store + MCP connection manager. It is safe
// for concurrent use.
type Manager struct {
	store   *pluginfs.Store
	mcp     *mcpclient.Manager
	mu      sync.Mutex
	running map[string]bool // pluginID → started
}

// New creates a runtime manager backed by the given store and MCP
// manager.
func New(store *pluginfs.Store, mcp *mcpclient.Manager) *Manager {
	return &Manager{
		store:   store,
		mcp:     mcp,
		running: map[string]bool{},
	}
}

// EnsureStarted starts the plugin's MCP server if it is not already
// running. Returns the available tools.
func (m *Manager) EnsureStarted(ctx context.Context, pluginID string) ([]contracts.MCPToolDTO, error) {
	plugin, err := m.store.Get(pluginID)
	if err != nil {
		return nil, err
	}
	server := pluginToMCPServer(plugin)
	tools, err := m.mcp.Connect(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", pluginID, err)
	}
	m.mu.Lock()
	m.running[pluginID] = true
	m.mu.Unlock()
	return tools, nil
}

// CallTool routes a tool call from the plugin UI to the plugin's MCP
// server. The plugin is started if it is not already running.
func (m *Manager) CallTool(ctx context.Context, pluginID, toolName string, args map[string]any) (string, error) {
	plugin, err := m.store.Get(pluginID)
	if err != nil {
		return "", err
	}
	server := pluginToMCPServer(plugin)
	// Ensure connected.
	if _, err := m.mcp.Connect(ctx, server); err != nil {
		return "", fmt.Errorf("plugin %s: %w", pluginID, err)
	}
	return m.mcp.CallTool(ctx, server.ID, toolName, args)
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
	m.mu.Lock()
	delete(m.running, pluginID)
	m.mu.Unlock()
}

// IsRunning returns true if the plugin's MCP server is connected.
func (m *Manager) IsRunning(pluginID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[pluginID]
}

// RunningPlugins returns the IDs of all currently running plugins.
func (m *Manager) RunningPlugins() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.running))
	for id := range m.running {
		out = append(out, id)
	}
	return out
}

// pluginToMCPServer converts a plugin's manifest into a domain.MCPServer
// so the mcpclient.Manager can dial it.
func pluginToMCPServer(p *domain.Plugin) *domain.MCPServer {
	m := p.Manifest.MCP
	return &domain.MCPServer{
		ID:         p.Manifest.MCPServerID(),
		Name:       p.Manifest.Name,
		Command:    m.Command,
		Args:       m.Args,
		Env:        m.Env,
		Enabled:    true,
		WorkingDir: p.InstallPath,
	}
}
