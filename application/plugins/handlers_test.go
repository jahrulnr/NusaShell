package plugins

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

type memStore struct{ plugins []*domain.Plugin }

func (s *memStore) List() ([]*domain.Plugin, error) { return s.plugins, nil }
func (s *memStore) Get(id string) (*domain.Plugin, error) {
	for _, p := range s.plugins {
		if p.Manifest.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *memStore) Save(p *domain.Plugin) error {
	for i, existing := range s.plugins {
		if existing.Manifest.ID == p.Manifest.ID {
			s.plugins[i] = p
			return nil
		}
	}
	s.plugins = append(s.plugins, p)
	return nil
}
func (s *memStore) Delete(string) error { return nil }

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

func TestHandleSetAutoStartConnectsWhenEnabled(t *testing.T) {
	p := pluginWithAutostart("mail", "mail", false)
	store := &memStore{plugins: []*domain.Plugin{p}}
	mcp := &recordingMCP{}
	svc := testService(store, mcp)

	if _, err := svc.handleSetAutoStart(contracts.PluginSetFlagRequest{ID: "mail", Enabled: true}); err != nil {
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

func TestHandleSaveReconnectsWhenAutostart(t *testing.T) {
	p := pluginWithAutostart("mail", "mail", true)
	store := &memStore{plugins: []*domain.Plugin{p}}
	mcp := &recordingMCP{}
	svc := testService(store, mcp)

	if _, err := svc.handleSave(contracts.PluginSaveRequest{
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
