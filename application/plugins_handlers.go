package application

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/pluginicon"
)

// pluginToDTO converts a domain.Plugin into its wire representation.
// The same DTO serves both catalog-installed plugins and manual MCP
// servers: a plugin is the single concept; "MCP server" is just a
// plugin whose manifest has an mcp block and no ui block.
func pluginToDTO(p *domain.Plugin) contracts.PluginDTO {
	dto := contracts.PluginDTO{
		ID:          p.Manifest.ID,
		Name:        p.Manifest.Name,
		Version:     p.Manifest.Version,
		Icon:        pluginicon.ResolveLocal(p.Manifest.Icon, p.InstallPath),
		Category:    p.Manifest.Category,
		HasUI:       p.HasUI,
		InstallPath: p.InstallPath,
		Autostart:   p.Manifest.MCP.Autostart,
		// Usage-contract declaration for the Plugins-view badge/drawer.
		ContractEntry: p.Manifest.ContractEntry(),
		AutoUpdate:    p.Manifest.AutoUpdate,
		// Baseline: anything exposing a UI is a plugin. handlePluginList
		// upgrades this (and Catalog) using catalog membership.
		Plugin: p.HasUI,
		Manifest: &contracts.PluginManifestDTO{
			ID:   p.Manifest.ID,
			Name: p.Manifest.Name,
			MCP: contracts.PluginMCPDTO{
				Transport: string(p.Manifest.MCP.Transport),
				Command:   p.Manifest.MCP.Command,
				URL:       p.Manifest.MCP.URL,
				Args:      p.Manifest.MCP.Args,
				Env:       p.Manifest.MCP.Env,
				Headers:   p.Manifest.MCP.Headers,
				Autostart: p.Manifest.MCP.Autostart,
				KeepAlive: p.Manifest.MCP.KeepAliveOnClose,
			},
		},
	}
	if p.HasUI {
		dto.Manifest.UI = &contracts.PluginUIDTO{
			Entry: p.Manifest.UI.Entry,
			Window: contracts.PluginWindowDTO{
				Mode:      string(p.Manifest.UI.Window.Mode),
				Resizable: p.Manifest.UI.Window.Resizable,
			},
		}
		dto.Manifest.UI.Window.DefaultSize.Width = p.Manifest.UI.Window.DefaultSize.Width
		dto.Manifest.UI.Window.DefaultSize.Height = p.Manifest.UI.Window.DefaultSize.Height
	}
	return dto
}

// pluginStatus returns the runtime state of a plugin's MCP server.
// Status is "connected" when tools are cached for its MCP server id,
// otherwise "idle". Manual MCP-server plugins and catalog-installed
// plugins are indistinguishable here.
func (a *App) pluginStatus(p *domain.Plugin) (string, []contracts.MCPToolDTO) {
	if a.MCPToolbox == nil {
		return "idle", nil
	}
	if tools, ok := a.MCPToolbox.ToolsFor(p.Manifest.MCPServerID()); ok {
		return "connected", tools
	}
	return "idle", nil
}

func (a *App) handlePluginCatalog() (any, *contracts.RPCError) {
	if a.PluginInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin installer not available"}
	}
	entries, err := a.PluginInstaller.Catalog(context.Background())
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	out := make([]contracts.PluginCatalogEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, contracts.PluginCatalogEntry{
			ID:          e.ID,
			PluginID:    e.PluginID,
			Name:        e.Name,
			Version:     e.Version,
			Description: e.Description,
			Icon:        e.Icon,
			Tag:         e.Tag,
			ReleasedAt:  e.ReleasedAt,
		})
	}
	return contracts.PluginCatalogResult{Plugins: out}, nil
}

func (a *App) handlePluginInstall(req contracts.PluginInstallRequest) (any, *contracts.RPCError) {
	if a.PluginInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin installer not available"}
	}
	source := domain.PluginInstallSource(req.Source)
	var data []byte
	if req.Data != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "invalid zip data"}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	plugin, err := a.PluginInstaller.Install(ctx, domain.PluginInstallRequest{
		Source: source,
		ID:     req.ID,
		URL:    req.URL,
		Subdir: req.Subdir,
		Ref:    req.Ref,
		Data:   data,
	})
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}

	dto := pluginToDTO(plugin)
	a.MCPToolbox.Drop(plugin.Manifest.MCPServerID())
	// Mount any skills bundled with the plugin (skills/ directory).
	if a.Skills != nil {
		skillsDir := filepath.Join(plugin.InstallPath, "skills")
		if err := a.Skills.MountPluginSkills(plugin.Manifest.ID, skillsDir); err != nil {
			a.log("warn", "plugin", "skill mount failed for %s: %v", plugin.Manifest.ID, err)
		}
	}
	a.log("info", "plugin", "plugin installed: %s v%s", plugin.Manifest.Name, plugin.Manifest.Version)
	return contracts.PluginInstallResult{Plugin: &dto}, nil
}

// handlePluginList returns every plugin — installed from the catalog or
// created manually as an MCP server — with runtime state. Idle and
// stopped plugins are included; the frontend renders their status.
func (a *App) handlePluginList() (any, *contracts.RPCError) {
	if a.Plugins == nil {
		return contracts.PluginListResult{Plugins: []contracts.PluginDTO{}}, nil
	}
	plugins, err := a.Plugins.List()
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	out := make([]contracts.PluginDTO, 0, len(plugins))
	// Check the catalog once so the UI can show which installed plugins are
	// catalog-managed (Catalog → auto-update/manual-update eligible) and
	// which have a newer version available (updateAvailable). The catalog
	// is cached, so CheckUpdates below reuses this fetch.
	catalogIDs := map[string]bool{}
	updatesByID := map[string]string{}
	if a.PluginInstaller != nil {
		ctxU, cancelU := context.WithTimeout(context.Background(), 30*time.Second)
		if entries, cerr := a.PluginInstaller.Catalog(ctxU); cerr == nil {
			for _, e := range entries {
				catalogIDs[e.PluginID] = true
			}
		}
		updates, uerr := a.PluginInstaller.CheckUpdates(ctxU, plugins)
		cancelU()
		if uerr == nil {
			for _, u := range updates {
				updatesByID[u.PluginID] = u.Version
			}
		}
	}
	for _, p := range plugins {
		dto := pluginToDTO(p)
		dto.Status, dto.Tools = a.pluginStatus(p)
		if catalogIDs[p.Manifest.ID] {
			dto.Catalog = true
			dto.Plugin = true
		}
		if v, ok := updatesByID[p.Manifest.ID]; ok {
			dto.UpdateAvailable = v
		}
		out = append(out, dto)
	}
	return contracts.PluginListResult{Plugins: out}, nil
}

// handlePluginSave creates or updates a manual MCP-server plugin (or a
// full plugin manifest when the request carries UI fields). The plugin is
// stored as <datadir>/plugins/<id>/manifest.json like any installed
// plugin, so manual MCP servers and catalog plugins live in one store.
func (a *App) handlePluginSave(req contracts.PluginSaveRequest) (any, *contracts.RPCError) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "plugin name is required"}
	}
	var existing *domain.Plugin
	if req.ID != "" {
		var err error
		existing, err = a.Plugins.Get(req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
	}
	mcpCfg, rpcErr := resolveMCPConfig(req, existing)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var p *domain.Plugin
	if existing != nil {
		p = existing
	} else {
		p = &domain.Plugin{Manifest: domain.PluginManifest{
			ID:      domain.NewID(domain.IDPrefixPlugin),
			Version: "0.1.0",
			Icon:    "🧩",
		}}
	}
	p.Manifest.Name = name
	p.Manifest.MCP = mcpCfg
	if err := a.Plugins.Save(p); err != nil {
		return nil, rpcInternal(err)
	}
	a.MCPToolbox.Drop(p.Manifest.MCPServerID())
	if p.Manifest.MCP.Autostart {
		if err := a.connectPluginMCP(context.Background(), p); err != nil {
			a.log("warn", "plugin", "autostart connect %s: %v", p.Manifest.ID, err)
		}
	}
	a.log("info", "plugin", "plugin saved: %s", p.Manifest.Name)
	dto := pluginToDTO(p)
	dto.Status, dto.Tools = a.pluginStatus(p)
	return contracts.PluginListResult{Plugins: []contracts.PluginDTO{dto}}, nil
}

// resolveMCPConfig builds the persisted MCP config from a save request.
// An omitted transport keeps the existing transport (so old callers can
// edit a remote server without degrading it back to stdio) and defaults
// to stdio for new servers. Stale fields from the other transport kind
// are cleared so a manifest never carries both command and url.
func resolveMCPConfig(req contracts.PluginSaveRequest, existing *domain.Plugin) (domain.PluginMCPConfig, *contracts.RPCError) {
	prev := domain.PluginMCPConfig{Transport: domain.PluginTransportStdio}
	if existing != nil {
		prev = existing.Manifest.MCP
	}
	transport := domain.PluginTransport(strings.TrimSpace(req.Transport))
	if transport == "" {
		transport = prev.Transport
		if transport == "" {
			transport = domain.PluginTransportStdio
		}
	}
	switch transport {
	case domain.PluginTransportStdio, domain.PluginTransportSSE, domain.PluginTransportHTTP:
	default:
		return domain.PluginMCPConfig{}, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unsupported mcp transport " + string(transport) + " (stdio, sse, http)"}
	}

	url := strings.TrimSpace(req.URL)
	if url == "" && transport != domain.PluginTransportStdio && transport == prev.Transport {
		url = prev.URL
	}
	headers := req.Headers
	if headers == nil && transport != domain.PluginTransportStdio && transport == prev.Transport {
		headers = prev.Headers
	}
	cfg := domain.PluginMCPConfig{
		Transport:        transport,
		URL:              url,
		Args:             req.Args,
		Env:              req.Env,
		Headers:          headers,
		Autostart:        req.Autostart,
		KeepAliveOnClose: prev.KeepAliveOnClose,
	}
	switch transport {
	case domain.PluginTransportStdio:
		cfg.Command = strings.TrimSpace(req.Command)
		if cfg.Command == "" {
			return domain.PluginMCPConfig{}, &contracts.RPCError{Code: contracts.CodeValidation, Message: "command is required for stdio transport"}
		}
		if cfg.URL != "" {
			cfg.URL = ""
			cfg.Headers = nil
		}
	case domain.PluginTransportSSE, domain.PluginTransportHTTP:
		if cfg.URL == "" {
			return domain.PluginMCPConfig{}, &contracts.RPCError{Code: contracts.CodeValidation, Message: "url is required for " + string(transport) + " transport"}
		}
		if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
			return domain.PluginMCPConfig{}, &contracts.RPCError{Code: contracts.CodeValidation, Message: "url must start with http:// or https://"}
		}
		cfg.Command = ""
		cfg.Args = nil
	}
	return cfg, nil
}

// handlePluginDelete removes a plugin. It works for both manual
// MCP-server plugins and catalog-installed plugins.
func (a *App) handlePluginDelete(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	if a.Plugins == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	if _, err := a.Plugins.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	// Unmount plugin skills before deleting the plugin directory.
	if a.Skills != nil {
		if err := a.Skills.UnmountPluginSkills(req.ID); err != nil {
			a.log("warn", "plugin", "skill unmount failed for %s: %v", req.ID, err)
		}
	}
	if err := a.Plugins.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	if a.CI != nil && a.CI.Caps != nil {
		deps, _ := a.CI.Caps.Dependents(context.Background(), req.ID)
		_ = a.CI.Caps.SetDisabled(context.Background(), req.ID, true)
		if len(deps) > 0 {
			a.log("info", "plugin", "plugin %s had %d dependent automation(s); they are now blocked", req.ID, len(deps))
		}
	}
	// Drop any cached MCP connection so the agent does not keep calling
	// a subprocess whose files were just removed.
	a.MCPToolbox.Drop("plugin:" + req.ID)
	a.log("info", "plugin", "plugin deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

// handlePluginTest connects a plugin's MCP server and returns its tools.
func (a *App) handlePluginTest(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	if a.Plugins == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	p, err := a.Plugins.Get(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tools, err := a.MCPToolbox.Connect(ctx, p)
	if err != nil {
		a.log("warn", "plugin", "plugin test failed: %s: %v", p.Manifest.Name, err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	return contracts.PluginTestResult{Tools: tools}, nil
}

// handlePluginStop drops the cached MCP connection for a plugin. The
// plugin definition remains installed; only the subprocess is stopped.
func (a *App) handlePluginStop(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	if a.Plugins == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	if _, err := a.Plugins.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	a.MCPToolbox.Drop("plugin:" + req.ID)
	a.log("info", "plugin", "plugin stopped: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

// handlePluginUninstall is an alias for handlePluginDelete kept for the
// install flow naming; uninstalling a catalog plugin removes its folder.
func (a *App) handlePluginUninstall(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	return a.handlePluginDelete(req)
}

// handlePluginCheckUpdates compares installed plugins against the catalog
// and returns entries with a newer version available.
func (a *App) handlePluginCheckUpdates() (any, *contracts.RPCError) {
	if a.Plugins == nil || a.PluginInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin runtime not available"}
	}
	installed, err := a.Plugins.List()
	if err != nil {
		return nil, rpcInternal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	updates, err := a.PluginInstaller.CheckUpdates(ctx, installed)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	out := make([]contracts.PluginCatalogEntry, 0, len(updates))
	for _, e := range updates {
		out = append(out, contracts.PluginCatalogEntry{
			ID: e.ID, PluginID: e.PluginID, Name: e.Name, Version: e.Version,
			Description: e.Description, Icon: e.Icon, Tag: e.Tag, ReleasedAt: e.ReleasedAt,
		})
	}
	return contracts.PluginCatalogResult{Plugins: out}, nil
}

// handlePluginSetAutoStart persists the auto-start preference (launch the
// plugin's MCP server when the app starts).
func (a *App) handlePluginSetAutoStart(req contracts.PluginSetFlagRequest) (any, *contracts.RPCError) {
	if a.Plugins == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	id := strings.TrimPrefix(req.ID, "plugin:")
	p, err := a.Plugins.Get(id)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	p.Manifest.MCP.Autostart = req.Enabled
	if err := a.Plugins.Save(p); err != nil {
		return nil, rpcInternal(err)
	}
	if req.Enabled {
		if err := a.connectPluginMCP(context.Background(), p); err != nil {
			a.log("warn", "plugin", "autostart connect %s: %v", id, err)
		}
	}
	a.log("info", "plugin", "autostart set: %s = %v", id, req.Enabled)
	return map[string]bool{"ok": true}, nil
}

// handlePluginSetAutoUpdate persists auto-update (same flag shape).
func (a *App) handlePluginSetAutoUpdate(req contracts.PluginSetFlagRequest) (any, *contracts.RPCError) {
	if a.Plugins == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	id := strings.TrimPrefix(req.ID, "plugin:")
	p, err := a.Plugins.Get(id)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	p.Manifest.AutoUpdate = req.Enabled
	if err := a.Plugins.Save(p); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "plugin", "autoupdate set: %s = %v", id, req.Enabled)
	return map[string]bool{"ok": true}, nil
}

// handlePluginUpdate updates a catalog plugin to its latest release.
func (a *App) handlePluginUpdate(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	if a.PluginInstaller == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin installer not available"}
	}
	id := strings.TrimSpace(req.ID)
	// Accept either the catalog key (e.g. "notes") or the manifest id
	// (e.g. "nusashell.notes") — normalize to the catalog key by looking up
	// the installed plugin's catalog entry.
	catalogID := id
	if strings.HasPrefix(id, "nusashell.") {
		catalogID = strings.TrimPrefix(id, "nusashell.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	updated, err := a.PluginInstaller.Update(ctx, catalogID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	a.MCPToolbox.Drop("plugin:" + updated.Manifest.ID)
	a.log("info", "plugin", "plugin updated: %s v%s", updated.Manifest.Name, updated.Manifest.Version)
	dto := pluginToDTO(updated)
	dto.Status, dto.Tools = a.pluginStatus(updated)
	// A plugin updated from the catalog is, by definition, catalog-managed.
	dto.Plugin = true
	dto.Catalog = true
	return contracts.PluginInstallResult{Plugin: &dto}, nil
}

// handlePluginToolsList returns tools from all connected plugins.
// Plugins that are not running contribute nothing; call handlePluginTest
// (Start) first to connect.
func (a *App) handlePluginToolsList() (any, *contracts.RPCError) {
	if a.Plugins == nil {
		return contracts.PluginToolsListResult{Tools: []contracts.MCPToolDTO{}}, nil
	}
	plugins, err := a.Plugins.List()
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	var out []contracts.MCPToolDTO
	for _, p := range plugins {
		if tools, ok := a.MCPToolbox.ToolsFor(p.Manifest.MCPServerID()); ok {
			out = append(out, tools...)
		}
	}
	if out == nil {
		out = []contracts.MCPToolDTO{}
	}
	return contracts.PluginToolsListResult{Tools: out}, nil
}
