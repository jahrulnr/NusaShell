package plugins

import (
	"context"

	"nusashell/contracts"
	"nusashell/domain"
)

// Store is the persistence port for installed plugins. Defined here so
// this package does not import the root application ports.
type Store interface {
	List() ([]*domain.Plugin, error)
	Get(id string) (*domain.Plugin, error)
	Save(p *domain.Plugin) error
	Delete(id string) error
}

// Installer fetches plugins from the curated catalog, a GitHub repository,
// or a local ZIP archive and installs or updates them.
type Installer interface {
	Catalog(ctx context.Context) ([]domain.PluginCatalogEntry, error)
	Install(ctx context.Context, req domain.PluginInstallRequest) (*domain.Plugin, error)
	CheckUpdates(ctx context.Context, installed []*domain.Plugin) ([]domain.PluginCatalogEntry, error)
	Update(ctx context.Context, pluginID string) (*domain.Plugin, error)
}

// Toolbox is the MCP connection cache used to start, stop, and inspect
// a plugin's tools.
type Toolbox interface {
	ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
	Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
	Drop(serverID string)
}

// SkillMounter mounts and unmounts skills bundled inside a plugin directory.
type SkillMounter interface {
	MountPluginSkills(pluginID, pluginSkillsDir string) error
	UnmountPluginSkills(pluginID string) error
}

// AutomationCaps disables automations that depend on a deleted plugin.
type AutomationCaps interface {
	Dependents(ctx context.Context, pluginID string) ([]*domain.WorkflowDefinition, error)
	SetDisabled(ctx context.Context, pluginID string, disabled bool) error
}

// Logger records a structured application log line.
type Logger func(level, source, format string, args ...any)

// Deps is the narrow wiring for New. Feature packages never receive *App.
type Deps struct {
	Store     Store
	Installer Installer
	MCP       Toolbox
	Skills    SkillMounter
	Caps      AutomationCaps
	Log       Logger
}

// Service owns plugin RPC handlers. Constructed by the root App.
type Service struct {
	store     Store
	installer Installer
	mcp       Toolbox
	skills    SkillMounter
	caps      AutomationCaps
	log       Logger
}

// New builds a plugin Service from Deps. Missing optional ports (Skills,
// Caps, Installer, Log) are treated as unavailable at the handler.
func New(d Deps) *Service {
	return &Service{
		store:     d.Store,
		installer: d.Installer,
		mcp:       d.MCP,
		skills:    d.Skills,
		caps:      d.Caps,
		log:       d.Log,
	}
}

func (s *Service) info(format string, args ...any) {
	s.write("info", format, args...)
}

func (s *Service) warn(format string, args ...any) {
	s.write("warn", format, args...)
}

func (s *Service) write(level, format string, args ...any) {
	if s.log != nil {
		s.log(level, "plugin", format, args...)
	}
}
