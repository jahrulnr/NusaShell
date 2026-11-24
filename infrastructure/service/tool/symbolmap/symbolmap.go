// Package toolsymbolmap provides the symbol_map record tool: a bounded
// map of the workspace's declared symbols, rendered once per pre-call
// hydration so the model picks a file from declarations instead of
// guessing paths. The schema rides the catalog with
// ToolPermissionGeneral — the environment runs it before each provider
// call and persists the render as an injection record, and the record is
// one-shot: the map is an orientation snapshot taken when the conversation
// started, written once per live tail and never refreshed by a file
// change (the application owns that cadence; this tool only declares it).
// The agent may invoke it with a bound folder's path to fetch that
// folder's current map on demand.
package toolsymbolmap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol"
)

// ToolName is the catalog name the injection records ride under.
const ToolName = "symbol_map"

// Tool renders the symbol map record for each bound directory. All file
// access goes through the injected Workspace so a remote driver indexes
// its own tree; lifecycle (change detection, background builds, disk
// cache) lives in the shared Manager so the map costs one build per
// workspace root no matter how many rooms attach to it.
type Tool struct {
	schema valueobject.ToolSchema
	store  drepo.Workspace
	root   string
	maps   *symbol.Manager
}

func New(store drepo.Workspace, root string, maps *symbol.Manager) (dservice.Tool, error) {
	if store == nil {
		return nil, errors.New("symbol_map needs a workspace store")
	}
	if maps == nil {
		return nil, errors.New("symbol_map needs a map manager")
	}
	schema, err := valueobject.NewToolSchema(
		ToolName,
		"Lists a folder's declared symbols (types, functions, methods) with file and line — pick the file here, then read or search it; declarations only, not usages. The harness injects the workspace map when a conversation starts and does not refresh it; invoke this yourself to map a bound folder now, or to refresh the workspace after edits: pass its absolute path and wait for the result (a first build can take tens of seconds). Allowed paths: the workspace directory or a folder bound to this conversation.",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"absolute folder to map — the workspace dir or a bound extra folder"},"folders":{"type":"array","items":{"type":"string"},"description":"bound folders the record lists (harness input; agents should pass only path)"}},"required":["path"]}`),
		true,
		enum.ToolPermissionGeneral,
	)
	if err != nil {
		return nil, err
	}
	return &Tool{schema: schema, store: store, root: root, maps: maps}, nil
}

func (tool *Tool) Schema() valueobject.ToolSchema { return tool.schema }

// OneShotHydration declares the record cadence: the map is orientation,
// not a claim about the tree, so the application writes it once per live
// tail and leaves it alone until a fold or a rebind re-opens the slot.
func (tool *Tool) OneShotHydration() bool { return true }

// ProvisionalHydration names the renders that must not be persisted: a
// first build still running has no map yet, so the record is dropped and
// the next step retries. Every other answer — a map (fresh or
// stale-while-revalidate), a truncation, a skip line — is final.
func (tool *Tool) ProvisionalHydration(output string) bool { return symbol.IsBuilding(output) }

// Execute has two modes. Record mode (the harness pass, marked by a
// folders list in the input) renders the workspace map plus one line
// per bound extra. On-demand mode (a bare path — what an agent sends)
// waits for and returns that folder's map, but only for folders the
// turn is allowed to see: the bound workspace dir or an extra folder.
func (tool *Tool) Execute(ctx context.Context, call entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return dservice.ToolResult{}, err
	}
	dir := dservice.WorkspaceDir(ctx)
	if dir == "" {
		dir = tool.root
	}
	extras := dservice.ExtraFolders(ctx)
	var in struct {
		Path    string   `json:"path"`
		Folders []string `json:"folders"`
	}
	_ = json.Unmarshal([]byte(call.Input()), &in)
	if len(in.Folders) == 0 && in.Path != "" && in.Path != dir {
		allowed := false
		for _, e := range extras {
			if e == in.Path {
				allowed = true
				break
			}
		}
		if !allowed {
			return dservice.ToolResult{Text: fmt.Sprintf(
				"symbol_map: %s is not a folder bound to this conversation — allowed: %s",
				in.Path, strings.Join(append([]string{dir}, extras...), ", "))}, nil
		}
		return dservice.ToolResult{Text: tool.maps.MapSync(ctx, in.Path, in.Path, tool.openRoot(in.Path))}, nil
	}
	var b strings.Builder
	b.WriteString(tool.maps.Map(ctx, dir, dir, tool.openRoot(dir)))
	for _, extra := range extras {
		if extra == "" || extra == dir {
			continue
		}
		b.WriteString("\n")
		// Extra folders get the one-line form: the full budget belongs
		// to the workspace the turn is actually rooted in, and probing
		// every bound folder each pass would multiply the walk cost.
		b.WriteString(tool.maps.MapLine(extra, extra))
	}
	return dservice.ToolResult{Text: b.String()}, nil
}

// openRoot hands the manager a fresh SymbolRoot opener bound to dir —
// the caller closes each root it opens, so a background build never
// borrows a turn's session.
func (tool *Tool) openRoot(dir string) func(context.Context) (dservice.SymbolRoot, error) {
	return func(ctx context.Context) (dservice.SymbolRoot, error) {
		return symbol.OpenWorkspaceRoot(ctx, tool.store, dir)
	}
}
