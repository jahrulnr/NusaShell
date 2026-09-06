package plugins

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/pluginicon"
	"nusashell/pkg/rpcdispatch"
)

const mcpConnectTimeout = 20 * time.Second

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
		// Baseline: anything exposing a UI is a plugin. handleList
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

func (s *Service) pluginStatus(p *domain.Plugin) (string, []contracts.MCPToolDTO) {
	if s.mcp == nil {
		return "idle", nil
	}
	if tools, ok := s.mcp.ToolsFor(p.Manifest.MCPServerID()); ok {
		return "connected", tools
	}
	return "idle", nil
}

func (s *Service) dropMCP(p *domain.Plugin) {
	if s.mcp == nil || p == nil {
		return
	}
	s.mcp.Drop(p.Manifest.MCPServerID())
}

func (s *Service) dropMCPID(id string) {
	if s.mcp == nil {
		return
	}
	s.mcp.Drop("plugin:" + id)
}

func (s *Service) connect(ctx context.Context, p *domain.Plugin) error {
	if s.mcp == nil || p == nil {
		return nil
	}
	connectCtx, cancel := context.WithTimeout(ctx, mcpConnectTimeout)
	defer cancel()
	_, err := s.mcp.Connect(connectCtx, p)
	return err
}

func (s *Service) handleCatalog() (any, *contracts.RPCError) {
	if s.installer == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin installer not available"}
	}
	entries, err := s.installer.Catalog(context.Background())
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

func (s *Service) handleInstall(req contracts.PluginInstallRequest) (any, *contracts.RPCError) {
	if s.installer == nil {
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

	plugin, err := s.installer.Install(ctx, domain.PluginInstallRequest{
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
	s.dropMCP(plugin)
	if s.skills != nil {
		skillsDir := filepath.Join(plugin.InstallPath, "skills")
		if err := s.skills.MountPluginSkills(plugin.Manifest.ID, skillsDir); err != nil {
			s.warn("skill mount failed for %s: %v", plugin.Manifest.ID, err)
		}
	}
	s.info("plugin installed: %s v%s", plugin.Manifest.Name, plugin.Manifest.Version)
	return contracts.PluginInstallResult{Plugin: &dto}, nil
}

// handleList returns every plugin — installed from the catalog or
// created manually as an MCP server — with runtime state. Idle and
// stopped plugins are included; the frontend renders their status.
func (s *Service) handleList() (any, *contracts.RPCError) {
	if s.store == nil {
		return contracts.PluginListResult{Plugins: []contracts.PluginDTO{}}, nil
	}
	list, err := s.store.List()
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	out := make([]contracts.PluginDTO, 0, len(list))
	catalogIDs := map[string]bool{}
	updatesByID := map[string]string{}
	if s.installer != nil {
		ctxU, cancelU := context.WithTimeout(context.Background(), 30*time.Second)
		if entries, cerr := s.installer.Catalog(ctxU); cerr == nil {
			for _, e := range entries {
				catalogIDs[e.PluginID] = true
			}
		}
		updates, uerr := s.installer.CheckUpdates(ctxU, list)
		cancelU()
		if uerr == nil {
			for _, u := range updates {
				updatesByID[u.PluginID] = u.Version
			}
		}
	}
	for _, p := range list {
		dto := pluginToDTO(p)
		dto.Status, dto.Tools = s.pluginStatus(p)
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

// handleSave creates or updates a manual MCP-server plugin (or a
// full plugin manifest when the request carries UI fields). The plugin is
// stored as <datadir>/plugins/<id>/manifest.json like any installed
// plugin, so manual MCP servers and catalog plugins live in one store.
func (s *Service) handleSave(req contracts.PluginSaveRequest) (any, *contracts.RPCError) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "plugin name is required"}
	}
	var existing *domain.Plugin
	if req.ID != "" {
		var err error
		existing, err = s.store.Get(req.ID)
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
	if err := s.store.Save(p); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	s.dropMCP(p)
	if p.Manifest.MCP.Autostart {
		if err := s.connect(context.Background(), p); err != nil {
			s.warn("autostart connect %s: %v", p.Manifest.ID, err)
		}
	}
	s.info("plugin saved: %s", p.Manifest.Name)
	dto := pluginToDTO(p)
	dto.Status, dto.Tools = s.pluginStatus(p)
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

func (s *Service) handleDelete(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	if s.store == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	if _, err := s.store.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if s.skills != nil {
		if err := s.skills.UnmountPluginSkills(req.ID); err != nil {
			s.warn("skill unmount failed for %s: %v", req.ID, err)
		}
	}
	if err := s.store.Delete(req.ID); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	if s.caps != nil {
		deps, _ := s.caps.Dependents(context.Background(), req.ID)
		_ = s.caps.SetDisabled(context.Background(), req.ID, true)
		if len(deps) > 0 {
			s.info("plugin %s had %d dependent automation(s); they are now blocked", req.ID, len(deps))
		}
	}
	s.dropMCPID(req.ID)
	s.info("plugin deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

func (s *Service) handleTest(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	if s.store == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	p, err := s.store.Get(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpConnectTimeout)
	defer cancel()
	tools, err := s.mcp.Connect(ctx, p)
	if err != nil {
		s.warn("plugin test failed: %s: %v", p.Manifest.Name, err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	return contracts.PluginTestResult{Tools: tools}, nil
}

func (s *Service) handleStop(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	if s.store == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	if _, err := s.store.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	s.dropMCPID(req.ID)
	s.info("plugin stopped: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

func (s *Service) handleUninstall(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	return s.handleDelete(req)
}

func (s *Service) handleCheckUpdates() (any, *contracts.RPCError) {
	if s.store == nil || s.installer == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin runtime not available"}
	}
	installed, err := s.store.List()
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	updates, err := s.installer.CheckUpdates(ctx, installed)
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

func (s *Service) handleSetAutoStart(req contracts.PluginSetFlagRequest) (any, *contracts.RPCError) {
	if s.store == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	id := strings.TrimPrefix(req.ID, "plugin:")
	p, err := s.store.Get(id)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	p.Manifest.MCP.Autostart = req.Enabled
	if err := s.store.Save(p); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	if req.Enabled {
		if err := s.connect(context.Background(), p); err != nil {
			s.warn("autostart connect %s: %v", id, err)
		}
	}
	s.info("autostart set: %s = %v", id, req.Enabled)
	return map[string]bool{"ok": true}, nil
}

func (s *Service) handleSetAutoUpdate(req contracts.PluginSetFlagRequest) (any, *contracts.RPCError) {
	if s.store == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin store not available"}
	}
	id := strings.TrimPrefix(req.ID, "plugin:")
	p, err := s.store.Get(id)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	p.Manifest.AutoUpdate = req.Enabled
	if err := s.store.Save(p); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	s.info("autoupdate set: %s = %v", id, req.Enabled)
	return map[string]bool{"ok": true}, nil
}

func (s *Service) handleUpdate(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	if s.installer == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "plugin installer not available"}
	}
	id := strings.TrimSpace(req.ID)
	catalogID := id
	if strings.HasPrefix(id, "nusashell.") {
		catalogID = strings.TrimPrefix(id, "nusashell.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	updated, err := s.installer.Update(ctx, catalogID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	s.dropMCPID(updated.Manifest.ID)
	s.info("plugin updated: %s v%s", updated.Manifest.Name, updated.Manifest.Version)
	dto := pluginToDTO(updated)
	dto.Status, dto.Tools = s.pluginStatus(updated)
	dto.Plugin = true
	dto.Catalog = true
	return contracts.PluginInstallResult{Plugin: &dto}, nil
}

func (s *Service) handleToolsList() (any, *contracts.RPCError) {
	if s.store == nil {
		return contracts.PluginToolsListResult{Tools: []contracts.MCPToolDTO{}}, nil
	}
	list, err := s.store.List()
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	var out []contracts.MCPToolDTO
	if s.mcp != nil {
		for _, p := range list {
			if tools, ok := s.mcp.ToolsFor(p.Manifest.MCPServerID()); ok {
				out = append(out, tools...)
			}
		}
	}
	if out == nil {
		out = []contracts.MCPToolDTO{}
	}
	return contracts.PluginToolsListResult{Tools: out}, nil
}
