package job

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// EnvironmentDriver is the per-conversation capability bundle. Tool and
// provider adapters inside one driver must resolve the same workspace; the
// application never mixes ports from different registered environments.
type EnvironmentDriver interface {
	Workspace() drepo.Workspace
	// FallbackRoot is the working directory a turn reports and executes
	// in when the room binds no workspace — "" means unset.
	FallbackRoot() string
	TextService(enum.Style) (dservice.TextModelService, bool)
	Tool(string) (dservice.Tool, bool)
	ToolSchemas() []valueobject.ToolSchema
	// ToolContexts renders the system-owned context records a turn
	// persists before each provider call — the enabled, system-callable
	// tool snapshots, each under its real tool name. skip names the slots
	// the application already carries (or ruled out): their tool is not
	// executed at all, so an orientation record costs neither its probe
	// nor its build on a turn that does not need it.
	ToolContexts(ctx context.Context, skip map[string]bool) ([]ToolContext, error)
}

// ToolContext is one environment-rendered record for the pre-call
// discovery phase. The driver owns what it produces; the turn persists it
// verbatim as a TooltypeInjection call. Dedup, when set, normalizes both
// the newest stored output and this render before the unchanged-record
// comparison so volatile fields (a rendered timestamp) never trigger a
// re-record — Output always persists at full fidelity. Once and
// Provisional carry the producer's cadence declaration: the application
// decides what to do with them.
type ToolContext struct {
	Name   string
	Input  string
	Output string
	Dedup  func(string) string
	// Once marks a record the application writes at most once per live
	// tail — orientation, not a claim about the tree. Presence alone
	// skips it; only a fold, a clear, or a rebind re-opens the slot.
	Once bool
	// Provisional marks a render that must not be persisted: the tool
	// answered before it had a real result. The slot stays open and the
	// next step retries.
	Provisional bool
}

// ToolDeduper is the optional contract a tool implements when its output
// carries volatile fields (a rendered timestamp, a random suffix):
// DedupKey strips them so the hydration record dedups on semantic
// content. Tools whose render is per-pass stable need no method — their
// records dedup on the verbatim output.
type ToolDeduper interface {
	DedupKey(output string) string
}

// OneShotHydration is the optional contract a tool implements when its
// pre-call record is orientation rather than a statement about the
// current tree: the application records it once per live tail and leaves
// it alone until a compaction seals it or the workspace binding changes.
// The tool declares; the application owns the cadence.
type OneShotHydration interface {
	OneShotHydration() bool
}

// ProvisionalHydration is the optional contract a tool implements when
// some of its renders are too early to keep — a first build still
// running. The application drops such a record and retries on the next
// step instead of freezing a placeholder into the room's orientation.
type ProvisionalHydration interface {
	ProvisionalHydration(output string) bool
}

// EnvironmentBundle is the basic driver built from injected ports. Backends
// may implement EnvironmentDriver directly when they need custom lifecycle.
type EnvironmentBundle struct {
	WorkspaceStore drepo.Workspace
	// DefaultRoot feeds FallbackRoot — the directory a room with no bound
	// workspace resolves file tools and exec against. Each delivery picks
	// it: serve passes the owner's home.
	DefaultRoot string
	Services    map[enum.Style]dservice.TextModelService
	Tools       map[string]dservice.Tool
	// Extra offers tools that change while the server runs (the owner's
	// MCP servers); nil means none. A fixed tool wins a name clash.
	Extra ToolSource
}

// ToolSource is a changing set of tools, safe for concurrent use.
type ToolSource interface {
	Tool(name string) (dservice.Tool, bool)
	Tools() []dservice.Tool
}

func (b EnvironmentBundle) Workspace() drepo.Workspace { return b.WorkspaceStore }

// FallbackRoot reports the driver's default working directory — "" when the
// composition wired none.
func (b EnvironmentBundle) FallbackRoot() string { return b.DefaultRoot }

func (b EnvironmentBundle) TextService(style enum.Style) (dservice.TextModelService, bool) {
	s, ok := b.Services[style]
	return s, ok
}

func (b EnvironmentBundle) Tool(name string) (dservice.Tool, bool) {
	if t, ok := b.Tools[name]; ok {
		return t, true
	}
	if b.Extra != nil {
		return b.Extra.Tool(name)
	}
	return nil, false
}

func (b EnvironmentBundle) ToolSchemas() []valueobject.ToolSchema {
	schemas := make([]valueobject.ToolSchema, 0, len(b.Tools))
	for _, tool := range b.Tools {
		schemas = append(schemas, tool.Schema())
	}
	if b.Extra != nil {
		for _, tool := range b.Extra.Tools() {
			if _, fixed := b.Tools[tool.Schema().Name()]; !fixed {
				schemas = append(schemas, tool.Schema())
			}
		}
	}
	slices.SortFunc(schemas, func(a, b valueobject.ToolSchema) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return schemas
}

// ToolContexts executes every system-callable tool against the turn's
// workspace and returns one record per tool, sorted by name so passes are
// deterministic. Agent-only tools never produce context; a tool that
// errors still returns a record — its output carries the error text, the
// same evidence the model would see from a failed call. A tool
// implementing ToolDeduper attaches its normalizer; a tool implementing
// OneShotHydration or ProvisionalHydration marks its record so the job
// can apply the cadence it owns. A name in skip is not executed: the
// application already carries (or ruled out) that slot, and rendering it
// would still pay the probe and any build the record exists to avoid.
func (b EnvironmentBundle) ToolContexts(ctx context.Context, skip map[string]bool) ([]ToolContext, error) {
	names := make([]string, 0, len(b.Tools))
	for name, tool := range b.Tools {
		if tool.Schema().CallableBySystem() && !skip[name] {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	// The input shape mirrors the workspace pin in appendWorkspaceContext
	// — identical name+input lets one dedup key serve both producers, so
	// a project with extra folders keeps exactly one pin instead of a
	// second record that never matches the pin's folders.
	raw, err := json.Marshal(struct {
		Path    string   `json:"path"`
		Folders []string `json:"folders,omitempty"`
	}{Path: dservice.WorkspaceDir(ctx), Folders: dservice.ExtraFolders(ctx)})
	if err != nil {
		return nil, err
	}
	contexts := make([]ToolContext, 0, len(names))
	for _, name := range names {
		id, err := valueobject.NewID("call", "")
		if err != nil {
			return nil, err
		}
		call, err := entity.NewToolCall(id.String(), name, string(raw), enum.TooltypeInjection)
		if err != nil {
			return nil, err
		}
		tool := b.Tools[name]
		result, err := tool.Execute(ctx, call, nil)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			result.Text = "error: " + err.Error()
		}
		record := ToolContext{Name: name, Input: call.Input(), Output: result.Text}
		if deduper, ok := tool.(ToolDeduper); ok {
			record.Dedup = deduper.DedupKey
		}
		if oneShot, ok := tool.(OneShotHydration); ok && oneShot.OneShotHydration() {
			record.Once = true
		}
		if provisional, ok := tool.(ProvisionalHydration); ok && provisional.ProvisionalHydration(result.Text) {
			record.Provisional = true
		}
		contexts = append(contexts, record)
	}
	return contexts, nil
}

// TurnWorkspace resolves the working directory one turn binds into context:
// the room's bound workspace wins; an empty binding falls back to the
// driver's default root so file tools, exec, and the workspace context
// record all cite the same path.
func TurnWorkspace(conv *aggregate.Conversation, driver EnvironmentDriver) string {
	if dir := conv.Workspace(); dir != "" {
		return dir
	}
	return driver.FallbackRoot()
}

func (d Deps) environment(conv *aggregate.Conversation) (EnvironmentDriver, error) {
	kind := conv.Environment()
	driver, ok := d.Environments[kind]
	if !ok || driver == nil {
		return nil, apperror.New(400, "conversation environment unavailable", fmt.Errorf("environment %q is not registered", kind))
	}
	return driver, nil
}
