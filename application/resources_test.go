package application

import (
	"testing"

	"nusashell/domain"
)

// pluginToDTO must surface the auto-update and auto-start preferences and a
// baseline plugin flag so the Plugins drawer can render the toggles. Before
// this was fixed the DTO never emitted AutoUpdate, so the UI toggle was
// stuck OFF regardless of the stored manifest value.
func TestPluginToDTOSurfacesPreferences(t *testing.T) {
	p := &domain.Plugin{
		Manifest: domain.PluginManifest{
			ID:         "nusashell.notes",
			Name:       "Notes",
			Version:    "1.2.0",
			Icon:       "📝",
			AutoUpdate: true,
			MCP: domain.PluginMCPConfig{
				Transport: domain.PluginTransportStdio,
				Command:   "node",
				Autostart: true,
			},
		},
	}

	dto := pluginToDTO(p)

	if !dto.AutoUpdate {
		t.Error("AutoUpdate must reflect manifest.AutoUpdate")
	}
	if !dto.Autostart {
		t.Error("Autostart must reflect manifest.MCP.Autostart")
	}
	if dto.Version != "1.2.0" {
		t.Errorf("Version = %q, want 1.2.0", dto.Version)
	}
}

// pluginToDTO must surface the declared usage-contract entry so the Plugins
// view can badge contract-declaring plugins and show the entry in the drawer.
func TestPluginToDTOSurfacesContractEntry(t *testing.T) {
	with := &domain.Plugin{Manifest: domain.PluginManifest{
		ID: "nusashell.files", Name: "Files", Version: "2.1.3", Icon: "📂",
		MCP:      domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "mcp/server"},
		Contract: &domain.PluginContractConfig{Entry: "CONTRACT.md"},
	}}
	dto := pluginToDTO(with)
	if dto.ContractEntry != "CONTRACT.md" {
		t.Errorf("ContractEntry = %q, want CONTRACT.md", dto.ContractEntry)
	}

	without := &domain.Plugin{Manifest: domain.PluginManifest{
		ID: "plugin_x", Name: "X", Version: "0.1.0", Icon: "🧩",
		MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"},
	}}
	if got := pluginToDTO(without).ContractEntry; got != "" {
		t.Errorf("ContractEntry = %q, want empty for contract-less plugin", got)
	}
}

// A manual stdio MCP server (no UI) is not a plugin by default; the list
// handler upgrades Plugin/Catalog only when the id is in the catalog.
func TestPluginToDTOManualServerIsNotPluginByDefault(t *testing.T) {
	p := &domain.Plugin{
		Manifest: domain.PluginManifest{
			ID:      "plugin_abc123",
			Name:    "filesystem",
			Version: "0.1.0",
			Icon:    "🧩",
			MCP:     domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"},
		},
	}

	dto := pluginToDTO(p)

	if dto.Plugin {
		t.Error("a headless manual MCP server must not be marked Plugin by pluginToDTO")
	}
	if dto.Catalog {
		t.Error("catalog membership is decided by the list handler, not pluginToDTO")
	}
}

// A plugin that exposes a UI is a plugin regardless of catalog membership.
func TestPluginToDTOUIPluginIsPlugin(t *testing.T) {
	p := &domain.Plugin{
		HasUI: true,
		Manifest: domain.PluginManifest{
			ID:      "nusashell.notes",
			Name:    "Notes",
			Version: "1.0.0",
			Icon:    "📝",
			MCP:     domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "node"},
			UI:      &domain.PluginUIConfig{Entry: "ui/index.html"},
		},
	}

	dto := pluginToDTO(p)

	if !dto.Plugin {
		t.Error("a UI plugin must be marked Plugin")
	}
	if !dto.HasUI {
		t.Error("HasUI must be true for a UI plugin")
	}
}
