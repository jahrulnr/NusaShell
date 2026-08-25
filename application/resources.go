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
	// When path is set, write a support file inside an existing skill.
	if path := strings.TrimSpace(req.Path); path != "" {
		if err := a.Skills.WriteFile(name, "", path, req.Content); err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		a.log("info", "skills", "skill file saved: %s/%s", name, path)
		return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: contracts.SkillDTO{ID: name, Name: name}}}, nil
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

// fragmentDTO converts a MemoryFragment to a MemoryEntryDTO for the UI.
func fragmentDTO(f *domain.MemoryFragment) contracts.MemoryEntryDTO {
	dto := contracts.MemoryEntryDTO{
		ID:        f.ID,
		Content:   f.Content,
		Tags:      f.Tags,
		Source:    f.Source,
		CreatedAt: f.CreatedAt.Format(timeRFC3339),
		Category:  f.Category,
		Project:   f.Project,
		Task:      f.Task,
		Tier:      "fragment",
	}
	if dto.Source == "" {
		dto.Source = "user"
	}
	return dto
}

// primaryDTO converts a PrimaryEntry to a MemoryEntryDTO for the UI.
func primaryDTO(e *domain.PrimaryEntry) contracts.MemoryEntryDTO {
	dto := contracts.MemoryEntryDTO{
		ID:        e.ID,
		Content:   e.Content,
		Source:    e.Source,
		CreatedAt: e.UpdatedAt.Format(timeRFC3339),
		Tier:      "primary",
	}
	if dto.Source == "" {
		dto.Source = "user"
	}
	return dto
}

// emitMemoryUpdated publishes a memory.updated event so the Learning UI
// can refresh its memory list, search results, and graph without polling.
func (a *App) emitMemoryUpdated() {
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventMemoryUpdated, map[string]any{"source": "rpc"})
	}
}

func (a *App) handleMemoryList() (any, *contracts.RPCError) {
	out := make([]contracts.MemoryEntryDTO, 0)
	// Primary memory entries (always-injected working set).
	if a.Primary != nil {
		mem := a.Primary.Load()
		for i := range mem.Entries {
			out = append(out, primaryDTO(&mem.Entries[i]))
		}
	}
	// Fragments (searchable archive).
	if a.Fragments != nil {
		for _, f := range a.Fragments.List(domain.FragmentSearchFilter{Limit: 500}) {
			out = append(out, fragmentDTO(f))
		}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemorySave(req contracts.MemorySaveRequest) (any, *contracts.RPCError) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory content is required"}
	}
	if a.Fragments == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "fragment store not configured"}
	}
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = domain.FragmentCategoryGeneral
	}
	frag := &domain.MemoryFragment{
		Category: category,
		Project:  strings.TrimSpace(req.Project),
		Task:     strings.TrimSpace(req.Task),
		Tags:     req.Tags,
		Content:  content,
		Source:   "user",
	}
	if err := a.Fragments.Save(frag); err != nil {
		return nil, rpcInternal(err)
	}
	a.emitMemoryUpdated()
	return contracts.MemoryListResult{Entries: []contracts.MemoryEntryDTO{fragmentDTO(frag)}}, nil
}

func (a *App) handleMemorySearch(req contracts.MemorySearchRequest) (any, *contracts.RPCError) {
	query := strings.TrimSpace(req.Query)
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var out []contracts.MemoryEntryDTO
	// Search fragments via BM25 + metadata filters.
	if a.Fragments != nil {
		hits := a.Fragments.Search(domain.FragmentSearchFilter{
			Query:    query,
			Category: strings.TrimSpace(req.Category),
			Project:  strings.TrimSpace(req.Project),
			Task:     strings.TrimSpace(req.Task),
			Tags:     req.Tags,
			Limit:    limit,
		})
		for _, h := range hits {
			out = append(out, fragmentDTO(h.Fragment))
		}
	}
	// Also include primary memory entries that match the query (substring).
	if a.Primary != nil {
		mem := a.Primary.Load()
		q := strings.ToLower(query)
		for i := range mem.Entries {
			if q == "" || strings.Contains(strings.ToLower(mem.Entries[i].Content), q) {
				out = append(out, primaryDTO(&mem.Entries[i]))
			}
		}
	}
	if out == nil {
		out = []contracts.MemoryEntryDTO{}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemoryDelete(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if a.Fragments == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "fragment store not configured"}
	}
	if err := a.Fragments.Delete(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	a.emitMemoryUpdated()
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
			// Primary memory entries.
			if a.Primary != nil {
				mem := a.Primary.Load()
				for i := range mem.Entries {
					content := mem.Entries[i].Content
					name := content
					if len(name) > 40 {
						name = name[:40] + "…"
					}
					items = append(items, contracts.LearningSearchResultItem{
						ID:      mem.Entries[i].ID,
						Kind:    "memory",
						Tier:    "primary",
						Name:    name,
						Content: content,
					})
				}
			}
			// Fragments.
			if a.Fragments != nil {
				for _, f := range a.Fragments.List(domain.FragmentSearchFilter{Limit: 200}) {
					name := f.Content
					if len(name) > 40 {
						name = name[:40] + "…"
					}
					items = append(items, contracts.LearningSearchResultItem{
						ID:      f.ID,
						Kind:    "memory",
						Tier:    "fragment",
						Name:    name,
						Content: f.Content,
					})
				}
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
		// Search fragments via BM25.
		if a.Fragments != nil {
			hits := a.Fragments.Search(domain.FragmentSearchFilter{
				Query: query,
				Limit: limit,
			})
			for _, h := range hits {
				name := h.Fragment.Content
				if len(name) > 40 {
					name = name[:40] + "…"
				}
				items = append(items, contracts.LearningSearchResultItem{
					ID:      h.Fragment.ID,
					Kind:    "memory",
					Tier:    "fragment",
					Name:    name,
					Content: h.Fragment.Content,
					Score:   float32(h.Score),
				})
			}
		}
		// Also search primary memory via substring.
		if a.Primary != nil {
			mem := a.Primary.Load()
			q := strings.ToLower(query)
			for i := range mem.Entries {
				if strings.Contains(strings.ToLower(mem.Entries[i].Content), q) {
					name := mem.Entries[i].Content
					if len(name) > 40 {
						name = name[:40] + "…"
					}
					items = append(items, contracts.LearningSearchResultItem{
						ID:      mem.Entries[i].ID,
						Kind:    "memory",
						Tier:    "primary",
						Name:    name,
						Content: mem.Entries[i].Content,
					})
				}
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
	// Primary memory nodes. Primary is a single prose document (one entry
	// per whole document), not per-fact entries — the node label is the
	// first line so it reads as the document's subject, and the tier marks
	// it as primary in the UI (distinct shape/color from fragments).
	if a.Primary != nil {
		mem := a.Primary.Load()
		for i := range mem.Entries {
			nodes = append(nodes, contracts.LearningGraphNode{
				ID:   mem.Entries[i].ID,
				Kind: "memory",
				Tier: "primary",
				Name: primaryNodeLabel(mem.Entries[i].Content),
			})
		}
	}
	// Fragment nodes (one node per fact).
	if a.Fragments != nil {
		for _, f := range a.Fragments.List(domain.FragmentSearchFilter{Limit: 500}) {
			nodes = append(nodes, contracts.LearningGraphNode{
				ID:   f.ID,
				Kind: "memory",
				Tier: "fragment",
				Name: memoryNodeLabel(f.Content),
			})
		}
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

// memoryNodeLabel shortens a fragment's content to a single-line node
// label (max 40 chars), collapsing whitespace so multi-line content does
// not break the graph label.
func memoryNodeLabel(content string) string {
	oneLine := strings.Join(strings.Fields(content), " ")
	if len(oneLine) > 40 {
		return oneLine[:40] + "…"
	}
	return oneLine
}

// primaryNodeLabel labels the single primary-memory document node with
// its first line (the document's subject), capped at 60 chars. The full
// document stays in the node tooltip via the frontend's title fallback.
func primaryNodeLabel(content string) string {
	first := content
	if idx := strings.IndexByte(first, '\n'); idx >= 0 {
		first = first[:idx]
	}
	first = strings.TrimSpace(first)
	if len(first) > 60 {
		return first[:60] + "…"
	}
	return first
}

// handleLearningLog returns the autolearn activity feed: learning-layer
// events from the trajectory log (review runs, extraction, edge building,
// consolidation, decay, prune), newest first. Review events are enriched
// with the source conversation title and their mutations (kind, tool,
// snippet). Events that are pure UI query noise (search, graph_load) are
// excluded.
func (a *App) handleLearningLog(req contracts.LearningLogRequest) (any, *contracts.RPCError) {
	events := ReadTrajectory(a.DataDir, req.Limit)

	// Conversation title lookup for review events. Build once from the
	// store (conversation counts are small) rather than per event.
	titles := map[string]string{}
	if a.Conversations != nil {
		for _, c := range a.Conversations.List() {
			if c.Title != "" {
				titles[c.ID] = c.Title
			}
		}
	}

	out := make([]contracts.LearningLogEntryDTO, 0, len(events))
	for _, e := range events {
		entry := contracts.LearningLogEntryDTO{
			TS:   e.Timestamp.UTC().Format(time.RFC3339),
			Type: e.Type,
		}
		if convID, ok := e.Detail["conversation"].(string); ok && convID != "" {
			entry.ConversationID = convID
			entry.ConversationTitle = titles[convID]
		}
		if reviewID, ok := e.Detail["review_id"].(string); ok {
			entry.ReviewID = reviewID
		}
		if status, ok := e.Detail["status"].(string); ok {
			entry.Status = status
		}
		if errMsg, ok := e.Detail["error"].(string); ok {
			entry.Error = errMsg
		}
		if raw, ok := e.Detail["mutations"]; ok {
			if list, ok := raw.([]interface{}); ok {
				for _, m := range list {
					if mm, ok := m.(map[string]interface{}); ok {
						// Structured mutation: kind + tool + snippet.
						mut := contracts.LearningLogMutationDTO{}
						if kind, ok := mm["kind"].(string); ok {
							mut.Kind = kind
						}
						if tool, ok := mm["tool"].(string); ok {
							mut.Tool = tool
						}
						if snippet, ok := mm["snippet"].(string); ok {
							mut.Snippet = snippet
						}
						entry.Mutations = append(entry.Mutations, mut)
						continue
					}
					if s, ok := m.(string); ok {
						// Legacy entries recorded mutations as a list of
						// kind strings (e.g. ["memory"]).
						entry.Mutations = append(entry.Mutations, contracts.LearningLogMutationDTO{Kind: s})
					}
				}
			}
		}
		// Pass through the remaining detail fields as raw JSON so the UI
		// can show per-type extras (e.g. decay/prune counts). The
		// conversation and mutations fields are structured columns and
		// must not be duplicated here.
		if len(e.Detail) > 0 {
			detail := make(map[string]json.RawMessage, len(e.Detail))
			for k, v := range e.Detail {
				if k == "conversation" || k == "mutations" || k == "review_id" || k == "status" || k == "error" {
					continue
				}
				b, err := json.Marshal(v)
				if err == nil {
					detail[k] = b
				}
			}
			if len(detail) > 0 {
				entry.Detail = detail
			}
		}
		out = append(out, entry)
	}
	return contracts.LearningLogResult{Entries: out}, nil
}

// handleLearningReviewTranscript returns the review agent's own
// conversation (LLM exchanges + tool calls + tool results) for a given
// review ID. This is the "background agent conversation" the user opens
// from the learning log — not the source conversation that was reviewed.
func (a *App) handleLearningReviewTranscript(req contracts.LearningReviewTranscriptRequest) (any, *contracts.RPCError) {
	if req.ID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "review transcript id is required"}
	}
	t := ReadReviewTranscript(a.DataDir, req.ID)
	if t == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "review transcript not found"}
	}
	msgs := make([]contracts.LearningReviewTranscriptMessageDTO, 0, len(t.Messages))
	for _, m := range t.Messages {
		dto := contracts.LearningReviewTranscriptMessageDTO{
			Role:      m.Role,
			Content:   m.Content,
			Reasoning: m.Reasoning,
		}
		for _, tc := range m.ToolCalls {
			dto.ToolCalls = append(dto.ToolCalls, contracts.ToolCallDTO{
				ID:     tc.ID,
				Name:   tc.Name,
				Args:   json.RawMessage(tc.Args),
				Status: string(tc.Status),
				Output: tc.Output,
				Opaque: tc.Opaque,
			})
		}
		if m.ToolResult != nil {
			dto.ToolResult = &contracts.ToolResultDTO{
				ToolCallID: m.ToolResult.ToolCallID,
				Name:       m.ToolResult.Name,
				Content:    m.ToolResult.Content,
			}
		}
		msgs = append(msgs, dto)
	}
	return contracts.LearningReviewTranscriptResult{
		ID:             t.ID,
		ConversationID: t.ConversationID,
		Model:          t.Model,
		CreatedAt:      t.CreatedAt.Format(time.RFC3339),
		Messages:       msgs,
	}, nil
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
	if req.CompactionModel != nil {
		s.CompactionModel = strings.TrimSpace(*req.CompactionModel)
	}
	if req.CompactionSummaryMaxTokens != nil {
		if *req.CompactionSummaryMaxTokens < 0 || *req.CompactionSummaryMaxTokens > 100000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "compaction summary max tokens must be between 0 and 100000 (0 = default)"}
		}
		s.CompactionSummaryMaxTokens = *req.CompactionSummaryMaxTokens
	}
	if req.CompactionSummaryMinChars != nil {
		if *req.CompactionSummaryMinChars < 0 || *req.CompactionSummaryMinChars > 100000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "compaction summary min chars must be between 0 and 100000 (0 = default)"}
		}
		s.CompactionSummaryMinChars = *req.CompactionSummaryMinChars
	}
	if req.ReviewModel != nil {
		s.ReviewModel = strings.TrimSpace(*req.ReviewModel)
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
	if req.RepeatedToolLimit != nil {
		if *req.RepeatedToolLimit < 0 || *req.RepeatedToolLimit > 100 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "repeated tool limit must be between 0 and 100 (0 = disabled)"}
		}
		s.RepeatedToolLimit = *req.RepeatedToolLimit
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
	if req.MaxParallelTools != nil {
		if *req.MaxParallelTools < 1 || *req.MaxParallelTools > 64 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max parallel tools must be between 1 and 64"}
		}
		s.MaxParallelTools = *req.MaxParallelTools
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
	if req.STTOfflineModel != nil {
		s.STTOfflineModel = strings.TrimSpace(*req.STTOfflineModel)
	}
	if req.STTOfflineLanguage != nil {
		s.STTOfflineLanguage = strings.TrimSpace(*req.STTOfflineLanguage)
	}
	if req.VideoProviderID != nil {
		s.VideoProviderID = strings.TrimSpace(*req.VideoProviderID)
	}
	if req.VideoModelID != nil {
		s.VideoModelID = strings.TrimSpace(*req.VideoModelID)
	}
	if req.TTSProviderID != nil {
		s.TTSProviderID = strings.TrimSpace(*req.TTSProviderID)
	}
	if req.TTSModelID != nil {
		s.TTSModelID = strings.TrimSpace(*req.TTSModelID)
	}
	if req.ImageProviderID != nil {
		s.ImageProviderID = strings.TrimSpace(*req.ImageProviderID)
	}
	if req.ImageModelID != nil {
		s.ImageModelID = strings.TrimSpace(*req.ImageModelID)
	}
	if req.VideoGenProviderID != nil {
		s.VideoGenProviderID = strings.TrimSpace(*req.VideoGenProviderID)
	}
	if req.VideoGenModelID != nil {
		s.VideoGenModelID = strings.TrimSpace(*req.VideoGenModelID)
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
	if req.PluginContractMode != nil {
		mode := strings.TrimSpace(*req.PluginContractMode)
		switch mode {
		case domain.PluginContractOff, domain.PluginContractHint, domain.PluginContractRequire:
			s.PluginContractMode = mode
		case "":
			// Reset to "follow the factory default" (anti-stamping: stored
			// empty resolves at runtime via contractMode()).
			s.PluginContractMode = ""
		default:
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "plugin_contract_mode must be off, hint, or require"}
		}
	}
	if req.LearningReviewThreshold != nil {
		v := *req.LearningReviewThreshold
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "learning_review_threshold must be >= 0 (0 disables turn-based review)"}
		}
		s.LearningReviewThreshold = v
	}
	if req.SkillNudgeInterval != nil {
		v := *req.SkillNudgeInterval
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill_nudge_interval must be >= 0 (0 disables tool-based review)"}
		}
		s.SkillNudgeInterval = v
	}
	if req.MaxAutoContinues != nil {
		v := *req.MaxAutoContinues
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max_auto_continues must be >= 0 (0 = unlimited)"}
		}
		s.MaxAutoContinues = v
	}
	if req.SoundNotifications != nil {
		s.SoundNotifications = *req.SoundNotifications
	}
	if req.UserPrompt != nil {
		s.UserPrompt = strings.TrimSpace(*req.UserPrompt)
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
		CompactionEnabled:          s.CompactionEnabled,
		CompactionThreshold:        s.CompactionThreshold,
		CompactionModel:            s.CompactionModel,
		CompactionSummaryMaxTokens: s.CompactionSummaryMaxTokens,
		CompactionSummaryMinChars:  s.CompactionSummaryMinChars,
		ReviewModel:                s.ReviewModel,
		PromptCaching:              s.PromptCaching,
		MaxToolRounds:              s.MaxToolRounds,
		RepeatedToolLimit:          s.RepeatedToolLimit,
		MaxInputTokens:             s.MaxInputTokens,
		MaxOutputTokens:            s.MaxOutputTokens,
		MaxParallelTools:           s.MaxParallelTools,
		EmbeddingProviderID:        s.EmbeddingProviderID,
		EmbeddingModelID:           s.EmbeddingModelID,
		VisionProviderID:           s.VisionProviderID,
		VisionModelID:              s.VisionModelID,
		AudioProviderID:            s.AudioProviderID,
		AudioModelID:               s.AudioModelID,
		VideoProviderID:            s.VideoProviderID,
		VideoModelID:               s.VideoModelID,
		TTSProviderID:              s.TTSProviderID,
		TTSModelID:                 s.TTSModelID,
		ImageProviderID:            s.ImageProviderID,
		ImageModelID:               s.ImageModelID,
		VideoGenProviderID:         s.VideoGenProviderID,
		VideoGenModelID:            s.VideoGenModelID,
		WebAnswerProvider:          s.WebAnswerProvider,
		WebAnswerModel:             s.WebAnswerModel,
		PluginContractMode:         s.PluginContractMode,
		Temperature:                s.Temperature,
		TopP:                       s.TopP,
		TopK:                       s.TopK,
		FrequencyPenalty:           s.FrequencyPenalty,
		PresencePenalty:            s.PresencePenalty,
		LearningReviewThreshold:    s.LearningReviewThreshold,
		SkillNudgeInterval:         s.SkillNudgeInterval,
		MaxAutoContinues:           s.MaxAutoContinues,
		SoundNotifications:         s.SoundNotifications,
		UserPrompt:                 s.UserPrompt,
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
