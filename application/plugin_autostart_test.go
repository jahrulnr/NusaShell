package application

import (
	"context"
	"fmt"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type recordingMCP struct {
	connected []string
	tools     map[string][]contracts.MCPToolDTO
	failID    string
}

func (m *recordingMCP) Connect(_ context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	if m.failID != "" && p.Manifest.ID == m.failID {
		return nil, fmt.Errorf("connect failed")
	}
	if m.tools == nil {
		m.tools = map[string][]contracts.MCPToolDTO{}
	}
	m.connected = append(m.connected, p.Manifest.ID)
	tools := []contracts.MCPToolDTO{{Name: "ping"}}
	m.tools[p.Manifest.MCPServerID()] = tools
	return tools, nil
}

func (m *recordingMCP) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) {
	t, ok := m.tools[serverID]
	return t, ok
}

func (m *recordingMCP) Drop(serverID string) {
	delete(m.tools, serverID)
}

type autostartPluginStore struct{ plugins []*domain.Plugin }

func (s *autostartPluginStore) List() ([]*domain.Plugin, error) { return s.plugins, nil }
func (s *autostartPluginStore) Get(id string) (*domain.Plugin, error) {
	for _, p := range s.plugins {
		if p.Manifest.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *autostartPluginStore) Install(string) (*domain.Plugin, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *autostartPluginStore) Uninstall(string) error { return nil }
func (s *autostartPluginStore) Save(p *domain.Plugin) error {
	for i, existing := range s.plugins {
		if existing.Manifest.ID == p.Manifest.ID {
			s.plugins[i] = p
			return nil
		}
	}
	s.plugins = append(s.plugins, p)
	return nil
}
func (s *autostartPluginStore) Delete(string) error { return nil }

func pluginWithAutostart(id, name string, autostart bool) *domain.Plugin {
	return &domain.Plugin{
		Manifest: domain.PluginManifest{
			ID:      id,
			Name:    name,
			Version: "1.0.0",
			Icon:    "🧩",
			MCP: domain.PluginMCPConfig{
				Transport: domain.PluginTransportStdio,
				Command:   "node",
				Autostart: autostart,
			},
		},
	}
}

func TestStartMCPAutostartConnectsOnlyFlaggedPlugins(t *testing.T) {
	mcp := &recordingMCP{}
	app := NewApp(Deps{
		Plugins: &autostartPluginStore{plugins: []*domain.Plugin{
			pluginWithAutostart("mail", "mail", true),
			pluginWithAutostart("notes", "notes", false),
		}},
		MCPToolbox: mcp,
		Logs:       &fakeLogStore{},
	})

	app.StartMCPAutostart(context.Background())

	if len(mcp.connected) != 1 || mcp.connected[0] != "mail" {
		t.Fatalf("connected = %v, want [mail]", mcp.connected)
	}
	if _, ok := mcp.ToolsFor("plugin:mail"); !ok {
		t.Fatal("autostart plugin must be connected after boot")
	}
	if _, ok := mcp.ToolsFor("plugin:notes"); ok {
		t.Fatal("plugins with autostart off must stay lazy")
	}
}

func TestStartMCPAutostartContinuesAfterConnectError(t *testing.T) {
	mcp := &recordingMCP{failID: "broken"}
	app := NewApp(Deps{
		Plugins: &autostartPluginStore{plugins: []*domain.Plugin{
			pluginWithAutostart("broken", "broken", true),
			pluginWithAutostart("mail", "mail", true),
		}},
		MCPToolbox: mcp,
		Logs:       &fakeLogStore{},
	})

	app.StartMCPAutostart(context.Background())

	if len(mcp.connected) != 1 || mcp.connected[0] != "mail" {
		t.Fatalf("connected = %v, want [mail] after skipping the failed plugin", mcp.connected)
	}
}

func TestStartMCPAutostartNoOpWithoutToolbox(t *testing.T) {
	app := NewApp(Deps{
		Plugins: &autostartPluginStore{plugins: []*domain.Plugin{
			pluginWithAutostart("mail", "mail", true),
		}},
	})
	app.StartMCPAutostart(context.Background())
}

func TestHandlePluginSetAutoStartConnectsWhenEnabled(t *testing.T) {
	p := pluginWithAutostart("mail", "mail", false)
	store := &autostartPluginStore{plugins: []*domain.Plugin{p}}
	mcp := &recordingMCP{}
	app := NewApp(Deps{Plugins: store, MCPToolbox: mcp, Logs: &fakeLogStore{}})

	if _, err := app.handlePluginSetAutoStart(contracts.PluginSetFlagRequest{ID: "mail", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("mail")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Manifest.MCP.Autostart {
		t.Fatal("autostart flag must persist")
	}
	if len(mcp.connected) != 1 || mcp.connected[0] != "mail" {
		t.Fatalf("enabling autostart must connect immediately, connected=%v", mcp.connected)
	}
}

func TestHandlePluginSaveReconnectsWhenAutostart(t *testing.T) {
	p := pluginWithAutostart("mail", "mail", true)
	store := &autostartPluginStore{plugins: []*domain.Plugin{p}}
	mcp := &recordingMCP{}
	app := NewApp(Deps{Plugins: store, MCPToolbox: mcp, Logs: &fakeLogStore{}})

	if _, err := app.handlePluginSave(contracts.PluginSaveRequest{
		ID: "mail", Name: "mail", Command: "node", Autostart: true,
	}); err != nil {
		t.Fatal(err)
	}
	if len(mcp.connected) != 1 || mcp.connected[0] != "mail" {
		t.Fatalf("saving with autostart must reconnect, connected=%v", mcp.connected)
	}
	if _, ok := mcp.ToolsFor("plugin:mail"); !ok {
		t.Fatal("autostart plugin must be connected after save")
	}
}
