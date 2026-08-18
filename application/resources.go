package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/pluginicon"
)

// ---- skills ----

// skillSlug normalizes a skill name into a filesystem-safe ID matching the
// skillfs pattern: lowercase ASCII letters, digits, and hyphens. Spaces and
// underscores become hyphens; other characters are dropped. A trailing
// suffix is appended when the result is empty or already taken to keep IDs
// unique and valid.
func skillSlug(name string) string {
	return domain.SkillSlug(name)
}

func skillDTO(s *domain.Skill) contracts.SkillDTO {
	dto := contracts.SkillDTO{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Category:    s.Category,
		State:       string(s.State),
		Origin:      string(s.Origin),
		OwnedBy:     s.EffectiveOwnedBy(),
		Pinned:      s.Pinned,
		UsageCount:  s.UsageCount,
		UpdatedAt:   s.UpdatedAt.Format(timeRFC3339),
	}
	if !s.LastUsedAt.IsZero() {
		dto.LastUsedAt = s.LastUsedAt.Format(timeRFC3339)
	}
	if s.State == "" {
		dto.State = string(domain.SkillStateActive)
	}
	if s.Origin == "" {
		dto.Origin = string(domain.SkillOriginUser)
	}
	return dto
}

// skillDTOsWithShadow marks skills that are shadowed by a higher-priority
// skill with the same ID. Priority: user > builtin > plugin.
func skillDTOsWithShadow(skills []*domain.Skill) []contracts.SkillDTO {
	// Group by ID to detect collisions.
	byID := make(map[string][]*domain.Skill)
	for _, s := range skills {
		byID[s.ID] = append(byID[s.ID], s)
	}
	out := make([]contracts.SkillDTO, 0, len(skills))
	for _, s := range skills {
		dto := skillDTO(s)
		// If there are multiple skills with this ID, mark lower-priority
		// ones as shadowed.
		if candidates := byID[s.ID]; len(candidates) > 1 {
			// Find the highest priority owner.
			bestPriority := domain.SkillOwnerPriority(s.EffectiveOwnedBy())
			for _, c := range candidates {
				if p := domain.SkillOwnerPriority(c.EffectiveOwnedBy()); p < bestPriority {
					bestPriority = p
				}
			}
			if domain.SkillOwnerPriority(s.EffectiveOwnedBy()) > bestPriority {
				dto.Shadowed = true
			}
		}
		out = append(out, dto)
	}
	return out
}

func (a *App) handleSkillsList() (any, *contracts.RPCError) {
	list := a.Skills.List()
	out := skillDTOsWithShadow(list)
	return contracts.SkillsListResult{Skills: out}, nil
}

func (a *App) handleSkillsRead(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	s, err := a.Skills.Get(req.ID, req.OwnedBy)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	full := contracts.SkillFull{SkillDTO: skillDTO(s), Content: s.Content}
	if files, ferr := a.Skills.Files(req.ID, req.OwnedBy); ferr == nil {
		for _, f := range files {
			full.Files = append(full.Files, contracts.SkillFileDTO{
				Path: f.Path, Type: f.Type, SizeBytes: f.SizeBytes, Editable: f.Editable,
			})
		}
	}
	return contracts.SkillReadResult{Skill: full}, nil
}

func (a *App) handleSkillsFileRead(req contracts.SkillFileReadRequest) (any, *contracts.RPCError) {
	if req.ID == "" || req.Path == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "id and path are required"}
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 200_000
	}
	f, err := a.Skills.ReadFile(req.ID, req.OwnedBy, req.Path, req.Offset, maxChars)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return contracts.SkillFileReadResult{
		Content:    f.Content,
		SizeBytes:  f.SizeBytes,
		Truncated:  f.Truncated,
		NextOffset: f.NextOffset,
	}, nil
}

func (a *App) handleSkillsInstall(req contracts.SkillInstallRequest) (any, *contracts.RPCError) {
	if req.Data == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "data is required"}
	}
	zipData, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "invalid base64 data"}
	}
	id, err := a.Skills.Install(zipData)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	skill, _ := a.Skills.Get(id, "")
	name := id
	if skill != nil {
		name = skill.Name
	}
	a.log("info", "skills", "skill installed: %s", id)
	return contracts.SkillInstallResult{ID: id, Name: name}, nil
}

func (a *App) handleSkillsSave(req contracts.SkillSaveRequest) (any, *contracts.RPCError) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill name is required"}
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill content is required"}
	}
	var s *domain.Skill
	if req.ID != "" {
		existing, err := a.Skills.Get(req.ID, "")
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		s = existing
	} else {
		s = &domain.Skill{
			ID:     skillSlug(name),
			State:  domain.SkillStateActive,
			Origin: domain.SkillOriginUser,
		}
	}
	s.Name = name
	s.Description = strings.TrimSpace(req.Description)
	s.Content = req.Content
	s.UpdatedAt = time.Now().UTC()
	if err := a.Skills.Save(s); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "skills", "skill saved: %s", s.Name)
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: skillDTO(s), Content: s.Content}}, nil
}

func (a *App) handleSkillsDelete(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	if _, err := a.Skills.Get(req.ID, req.OwnedBy); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err := a.Skills.Delete(req.ID, req.OwnedBy); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "skills", "skill deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

// ---- plugins (MCP servers + MCP+UI plugins) ----

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
		AutoUpdate:  p.Manifest.AutoUpdate,
		// Baseline: anything exposing a UI is a plugin. handlePluginList
		// upgrades this (and Catalog) using catalog membership.
		Plugin: p.HasUI,
		Manifest: &contracts.PluginManifestDTO{
			ID:   p.Manifest.ID,
			Name: p.Manifest.Name,
			MCP: contracts.PluginMCPDTO{
				Transport: string(p.Manifest.MCP.Transport),
				Command:   p.Manifest.MCP.Command,
				Args:      p.Manifest.MCP.Args,
				Env:       p.Manifest.MCP.Env,
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
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "command is required"}
	}
	var p *domain.Plugin
	if req.ID != "" {
		existing, err := a.Plugins.Get(req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		p = existing
	} else {
		p = &domain.Plugin{Manifest: domain.PluginManifest{
			ID:      domain.NewID("plugin"),
			Version: "0.1.0",
			Icon:    "🧩",
			MCP:     domain.PluginMCPConfig{Transport: domain.PluginTransportStdio},
		}}
	}
	p.Manifest.Name = name
	p.Manifest.MCP.Command = command
	p.Manifest.MCP.Args = req.Args
	p.Manifest.MCP.Env = req.Env
	p.Manifest.MCP.Autostart = req.Autostart
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
	if a.Automation != nil && a.Automation.Caps != nil {
		deps, _ := a.Automation.Caps.Dependents(context.Background(), req.ID)
		_ = a.Automation.Caps.SetDisabled(context.Background(), req.ID, true)
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

// ---- memory ----

func memDTO(e *domain.MemoryEntry) contracts.MemoryEntryDTO {
	dto := contracts.MemoryEntryDTO{
		ID:        e.ID,
		Content:   e.Content,
		Tags:      e.Tags,
		Source:    e.Source,
		CreatedAt: e.CreatedAt.Format(timeRFC3339),
	}
	if dto.Source == "" {
		dto.Source = "user"
	}
	return dto
}

func (a *App) handleMemoryList() (any, *contracts.RPCError) {
	list := a.Memory.List()
	out := make([]contracts.MemoryEntryDTO, 0, len(list))
	for _, e := range list {
		out = append(out, memDTO(e))
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemorySave(req contracts.MemorySaveRequest) (any, *contracts.RPCError) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory content is required"}
	}
	e := &domain.MemoryEntry{
		ID:        domain.NewULID("mem"),
		Content:   content,
		Tags:      req.Tags,
		Source:    "user",
		CreatedAt: time.Now().UTC(),
	}
	if err := a.Memory.Save(e); err != nil {
		return nil, rpcInternal(err)
	}
	return contracts.MemoryListResult{Entries: []contracts.MemoryEntryDTO{memDTO(e)}}, nil
}

func (a *App) handleMemorySearch(req contracts.MemorySearchRequest) (any, *contracts.RPCError) {
	query := strings.ToLower(strings.TrimSpace(req.Query))
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var out []contracts.MemoryEntryDTO
	for _, e := range a.Memory.List() {
		hay := strings.ToLower(e.Content + " " + strings.Join(e.Tags, " "))
		if query == "" || strings.Contains(hay, query) {
			out = append(out, memDTO(e))
			if len(out) >= limit {
				break
			}
		}
	}
	if out == nil {
		out = []contracts.MemoryEntryDTO{}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemoryDelete(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if err := a.Memory.Delete(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return map[string]bool{"ok": true}, nil
}

// ---- learning search ----

// handleLearningSearch runs hybrid BM25 + embedding search over skills and
// memory entries, fused via RRF. The kind filter ("skills" or "memory")
// restricts the search to one collection; empty searches both.
func (a *App) handleLearningSearch(req contracts.LearningSearchRequest) (any, *contracts.RPCError) {
	query := strings.TrimSpace(req.Query)
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))

	// Empty query: return an unfiltered listing (all skills and/or memories)
	// so the Learning view shows content immediately instead of an empty
	// "search to begin" state. Score is 0; items are sorted by name.
	if query == "" {
		items := make([]contracts.LearningSearchResultItem, 0, limit*2)
		if kind == "" || kind == "skills" {
			for _, sk := range a.Skills.List() {
				items = append(items, contracts.LearningSearchResultItem{
					ID:      sk.ID,
					Kind:    "skill",
					Name:    sk.Name,
					Content: sk.Content,
				})
			}
		}
		if kind == "" || kind == "memory" {
			for _, mem := range a.Memory.List() {
				items = append(items, contracts.LearningSearchResultItem{
					ID:      mem.ID,
					Kind:    "memory",
					Content: mem.Content,
				})
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		if len(items) > limit {
			items = items[:limit]
		}
		return contracts.LearningSearchResult{Items: items}, nil
	}

	searcher := a.learningSearch()
	ctx := context.Background()
	items := make([]contracts.LearningSearchResultItem, 0, limit*2)

	if kind == "" || kind == "skills" {
		results, err := searcher.SearchSkills(ctx, query, limit)
		if err == nil {
			for _, r := range results {
				s, err := a.Skills.Get(r.ID, "")
				if err != nil {
					continue
				}
				items = append(items, contracts.LearningSearchResultItem{
					ID:      s.ID,
					Kind:    "skill",
					Name:    s.Name,
					Content: s.Content,
					Score:   float32(r.Score),
				})
			}
		}
	}
	if kind == "" || kind == "memory" {
		results, err := searcher.SearchMemory(ctx, query, limit)
		if err == nil {
			for _, r := range results {
				var content string
				for _, e := range a.Memory.List() {
					if e.ID == r.ID {
						content = e.Content
						break
					}
				}
				if content == "" {
					continue
				}
				items = append(items, contracts.LearningSearchResultItem{
					ID:      r.ID,
					Kind:    "memory",
					Content: content,
					Score:   float32(r.Score),
				})
			}
		}
	}
	// Sort by score descending.
	sort.Slice(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	if len(items) > limit {
		items = items[:limit]
	}
	if a.Trajectory != nil {
		a.Trajectory.Record("search", map[string]interface{}{
			"query":  query,
			"kind":   kind,
			"limit":  limit,
			"result": len(items),
		})
	}
	return contracts.LearningSearchResult{Items: items}, nil
}

// handleLearningGraph returns the full learning graph (nodes + edges)
// for the frontend graph view. Nodes are skills + memory entries; edges
// are pre-computed by the EdgeBuilder (similarity + token overlap).
func (a *App) handleLearningGraph() (any, *contracts.RPCError) {
	// Build edges if edge builder is configured (idempotent — strengthens
	// existing edges, doesn't duplicate).
	if a.edgeBuilder != nil {
		// Resolve embedder lazily for embedding-based edges
		if a.edgeBuilder.embed == nil {
			if embedder, modelID := a.resolveEmbedder(); embedder != nil {
				a.edgeBuilder.embed = embedder
				a.edgeBuilder.modelID = modelID
			}
		}
		_ = a.edgeBuilder.Build(context.Background())
	}

	// Collect nodes
	var nodes []contracts.LearningGraphNode
	for _, s := range a.Skills.List() {
		nodes = append(nodes, contracts.LearningGraphNode{
			ID:   s.ID,
			Kind: "skill",
			Name: s.Name,
		})
	}
	for _, m := range a.Memory.List() {
		name := m.Content
		if len(name) > 40 {
			name = name[:40] + "…"
		}
		nodes = append(nodes, contracts.LearningGraphNode{
			ID:   m.ID,
			Kind: "memory",
			Name: name,
		})
	}

	// Collect edges from graph service. Only edges whose BOTH endpoints are
	// present in the node set are emitted: memory/skill entries that were
	// deleted after an edge was persisted would otherwise reference nodes
	// that do not exist, and vis-network silently drops them — making whole
	// clusters appear disconnected.
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		nodeIDs[n.ID] = struct{}{}
	}
	var edges []contracts.LearningGraphEdge
	if gs := a.graph(); gs != nil {
		for _, e := range gs.AllEdges() {
			if _, ok := nodeIDs[e.SourceID]; !ok {
				continue
			}
			if _, ok := nodeIDs[e.TargetID]; !ok {
				continue
			}
			edges = append(edges, contracts.LearningGraphEdge{
				From:   e.SourceID,
				To:     e.TargetID,
				Type:   string(e.Type),
				Weight: e.Weight,
			})
		}
	}

	if a.Trajectory != nil {
		a.Trajectory.Record("graph_load", map[string]interface{}{
			"nodes": len(nodes),
			"edges": len(edges),
		})
	}
	return contracts.LearningGraphResult{Nodes: nodes, Edges: edges}, nil
}

// ---- docs ----

func (a *App) handleDocsList() (any, *contracts.RPCError) {
	metas := a.Docs.List()
	out := make([]contracts.DocDTO, 0, len(metas))
	for _, m := range metas {
		out = append(out, contracts.DocDTO{ID: m.ID, Title: m.Title, Path: m.Path})
	}
	return contracts.DocsListResult{Docs: out}, nil
}

func (a *App) handleDocsSearch(req contracts.DocsSearchRequest) (any, *contracts.RPCError) {
	limit := req.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	hits := a.Docs.Search(req.Query, limit)
	out := make([]contracts.DocHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, contracts.DocHit{
			DocDTO:  contracts.DocDTO{ID: h.ID, Title: h.Title, Path: h.Path},
			Snippet: h.Snippet,
		})
	}
	return contracts.DocsSearchResult{Results: out}, nil
}

func (a *App) handleDocsRead(req contracts.DocReadRequest) (any, *contracts.RPCError) {
	doc, err := a.Docs.Read(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return contracts.DocReadResult{Doc: contracts.DocFull{
		DocDTO:  contracts.DocDTO{ID: doc.ID, Title: doc.Title, Path: doc.Path},
		Content: doc.Content,
	}}, nil
}

// ---- logs / settings ----

func (a *App) handleLogsList(req contracts.LogsListRequest) (any, *contracts.RPCError) {
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	entries := a.Logs.List(req.Level, limit)
	out := make([]contracts.LogEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, contracts.LogEntryDTO{
			ID: e.ID, Time: e.Time.Format(timeRFC3339), Level: e.Level, Source: e.Source, Message: e.Message,
		})
	}
	return contracts.LogsListResult{Entries: out}, nil
}

func (a *App) handleLogsClear() (any, *contracts.RPCError) {
	a.Logs.Clear()
	return map[string]bool{"ok": true}, nil
}

func (a *App) handleSettingsGet() (any, *contracts.RPCError) {
	return contracts.SettingsGetResult{Settings: settingsDTO(a.Settings.Get())}, nil
}

func (a *App) handleSettingsSet(req contracts.SettingsSetRequest) (any, *contracts.RPCError) {
	s := a.Settings.Get()
	if req.CompactionEnabled != nil {
		s.CompactionEnabled = *req.CompactionEnabled
	}
	if req.CompactionThreshold != nil {
		if *req.CompactionThreshold < 0 || *req.CompactionThreshold > 2000000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "compaction threshold must be between 0 and 2,000,000 (0 = auto)"}
		}
		s.CompactionThreshold = *req.CompactionThreshold
	}
	if req.PromptCaching != nil {
		s.PromptCaching = *req.PromptCaching
	}
	if req.MaxToolRounds != nil {
		if *req.MaxToolRounds < 1 || *req.MaxToolRounds > 10000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max tool rounds must be between 1 and 10000"}
		}
		s.MaxToolRounds = *req.MaxToolRounds
	}
	if req.MaxInputTokens != nil {
		if *req.MaxInputTokens < 1000 || *req.MaxInputTokens > 2000000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max input tokens must be between 1000 and 2000000"}
		}
		s.MaxInputTokens = *req.MaxInputTokens
	}
	if req.MaxOutputTokens != nil {
		if *req.MaxOutputTokens < 256 || *req.MaxOutputTokens > 1000000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max output tokens must be between 256 and 1000000"}
		}
		s.MaxOutputTokens = *req.MaxOutputTokens
	}
	// Sampling parameters use json.RawMessage to distinguish three states:
	// absent (don't change), null (clear to nil), value (set). A *float64
	// with omitempty cannot tell null from absent, so once set the parameter
	// could never be cleared.
	if err := applyOptionalFloat(req.Temperature, func(v float64) error {
		if v < 0 || v > 2 {
			return fmt.Errorf("temperature must be between 0 and 2")
		}
		return nil
	}, &s.Temperature); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := applyOptionalFloat(req.TopP, func(v float64) error {
		if v < 0 || v > 1 {
			return fmt.Errorf("top_p must be between 0 and 1")
		}
		return nil
	}, &s.TopP); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := applyOptionalInt(req.TopK, func(v int) error {
		if v < 1 {
			return fmt.Errorf("top_k must be at least 1")
		}
		return nil
	}, &s.TopK); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := applyOptionalFloat(req.FrequencyPenalty, func(v float64) error {
		if v < -2 || v > 2 {
			return fmt.Errorf("frequency_penalty must be between -2 and 2")
		}
		return nil
	}, &s.FrequencyPenalty); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := applyOptionalFloat(req.PresencePenalty, func(v float64) error {
		if v < -2 || v > 2 {
			return fmt.Errorf("presence_penalty must be between -2 and 2")
		}
		return nil
	}, &s.PresencePenalty); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if req.EmbeddingProviderID != nil {
		s.EmbeddingProviderID = strings.TrimSpace(*req.EmbeddingProviderID)
	}
	if req.EmbeddingModelID != nil {
		s.EmbeddingModelID = strings.TrimSpace(*req.EmbeddingModelID)
	}
	if req.VisionProviderID != nil {
		s.VisionProviderID = strings.TrimSpace(*req.VisionProviderID)
	}
	if req.VisionModelID != nil {
		s.VisionModelID = strings.TrimSpace(*req.VisionModelID)
	}
	if req.AudioProviderID != nil {
		s.AudioProviderID = strings.TrimSpace(*req.AudioProviderID)
	}
	if req.AudioModelID != nil {
		s.AudioModelID = strings.TrimSpace(*req.AudioModelID)
	}
	if req.VideoProviderID != nil {
		s.VideoProviderID = strings.TrimSpace(*req.VideoProviderID)
	}
	if req.VideoModelID != nil {
		s.VideoModelID = strings.TrimSpace(*req.VideoModelID)
	}
	if req.WebAnswerProvider != nil {
		s.WebAnswerProvider = strings.TrimSpace(*req.WebAnswerProvider)
	}
	if req.WebAnswerModel != nil {
		s.WebAnswerModel = strings.TrimSpace(*req.WebAnswerModel)
	}
	if req.WebAnswerAPIKey != nil {
		key := strings.TrimSpace(*req.WebAnswerAPIKey)
		if key == "" {
			_ = a.Credentials.Delete("web_answer")
		} else {
			if err := a.Credentials.Set("web_answer", key); err != nil {
				return nil, rpcInternal(err)
			}
		}
	}
	if req.LearningReviewThreshold != nil {
		v := *req.LearningReviewThreshold
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "learning_review_threshold must be >= 0 (0 disables turn-based review)"}
		}
		s.LearningReviewThreshold = v
	}
	if req.MaxAutoContinues != nil {
		v := *req.MaxAutoContinues
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max_auto_continues must be >= 0 (0 = unlimited)"}
		}
		s.MaxAutoContinues = v
	}
	if err := a.Settings.Set(s); err != nil {
		return nil, rpcInternal(err)
	}
	// Invalidate the learning searcher so the next search rebuilds it with
	// the new embedding settings (if the embedding model selection changed).
	a.InvalidateLearningSearcher()
	return contracts.SettingsGetResult{Settings: settingsDTO(s)}, nil
}

func settingsDTO(s domain.Settings) contracts.SettingsDTO {
	return contracts.SettingsDTO{
		CompactionEnabled:       s.CompactionEnabled,
		CompactionThreshold:     s.CompactionThreshold,
		PromptCaching:           s.PromptCaching,
		MaxToolRounds:           s.MaxToolRounds,
		MaxInputTokens:          s.MaxInputTokens,
		MaxOutputTokens:         s.MaxOutputTokens,
		EmbeddingProviderID:     s.EmbeddingProviderID,
		EmbeddingModelID:        s.EmbeddingModelID,
		VisionProviderID:        s.VisionProviderID,
		VisionModelID:           s.VisionModelID,
		AudioProviderID:         s.AudioProviderID,
		AudioModelID:            s.AudioModelID,
		VideoProviderID:         s.VideoProviderID,
		VideoModelID:            s.VideoModelID,
		WebAnswerProvider:       s.WebAnswerProvider,
		WebAnswerModel:          s.WebAnswerModel,
		Temperature:             s.Temperature,
		TopP:                    s.TopP,
		TopK:                    s.TopK,
		FrequencyPenalty:        s.FrequencyPenalty,
		PresencePenalty:         s.PresencePenalty,
		LearningReviewThreshold: s.LearningReviewThreshold,
		MaxAutoContinues:        s.MaxAutoContinues,
	}
}

// applyOptionalFloat parses a json.RawMessage sampling parameter in three
// states: nil (absent — don't change), "null" (clear to nil), or a float64
// value (validate then set). A *float64 with omitempty cannot distinguish
// null from absent, so json.RawMessage is used on the wire instead.
func applyOptionalFloat(raw json.RawMessage, validate func(float64) error, target **float64) error {
	return domain.ApplyOptionalFloat(raw, validate, target)
}

// applyOptionalInt is the integer variant of applyOptionalFloat for top_k.
func applyOptionalInt(raw json.RawMessage, validate func(int) error, target **int) error {
	return domain.ApplyOptionalInt(raw, validate, target)
}
