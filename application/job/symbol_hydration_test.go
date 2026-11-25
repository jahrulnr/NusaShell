package job

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	repoprovider "github.com/jahrulnr/nusashell/infrastructure/repository/provider"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	toolsummary "github.com/jahrulnr/nusashell/infrastructure/service/tool/summary"
)

// The tests in this file pin the symbol map's record cadence and its gate:
// one orientation record per live tail, refilled only by a fold, a clear,
// or a workspace rebind, and never written for a root that is the fallback
// home or carries no project marker.

// mapTool is the symbol_map record's shape at the job boundary: a one-shot
// record whose render follows the tree, and whose first answer can be the
// provisional "being built" line. The declarations mirror the real tool's,
// so the job proves the cadence without importing the adapter.
type mapTool struct {
	schema valueobject.ToolSchema
	render func(pass int) string
	passes int
}

func (t *mapTool) Schema() valueobject.ToolSchema { return t.schema }

func (t *mapTool) Execute(context.Context, entity.ToolCall, dservice.ToolEmitter) (dservice.ToolResult, error) {
	t.passes++
	return dservice.ToolResult{Text: t.render(t.passes)}, nil
}

func (t *mapTool) OneShotHydration() bool { return true }

func (t *mapTool) ProvisionalHydration(output string) bool {
	return strings.Contains(output, "is being built")
}

func newMapTool(t *testing.T, render func(pass int) string) *mapTool {
	t.Helper()
	schema, err := valueobject.NewToolSchema("symbol_map", "map", nil, true, enum.ToolPermissionGeneral)
	if err != nil {
		t.Fatal(err)
	}
	return &mapTool{schema: schema, render: render}
}

// mapRecords are the live symbol_map hydration records — the room's
// orientation, never the model's own on-demand calls.
func mapRecords(conv *aggregate.Conversation) []entity.ToolCall {
	var calls []entity.ToolCall
	for _, msg := range conv.Messages() {
		for _, call := range msg.ToolCalls() {
			if call.Type() == enum.TooltypeInjection && call.Name() == "symbol_map" {
				calls = append(calls, call)
			}
		}
	}
	return calls
}

// mappedRoot is a root the hydration gate accepts: a real directory that
// carries a go.mod marker at its top level.
func mappedRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// symbolDeps wires a room on a provider adapter over one workspace, with a
// compaction budget small enough that a fat tool result folds the history
// on the next step. The bundle carries the local workspace port so the
// gate reads a real tree.
func symbolDeps(t *testing.T, svc dservice.TextModelService, tools map[string]dservice.Tool) Deps {
	t.Helper()
	prov, err := aggregate.NewProvider("gw1", "stub", "http://unused", enum.StyleCompletion)
	if err != nil {
		t.Fatal(err)
	}
	m, err := entity.NewModel("m1")
	if err != nil {
		t.Fatal(err)
	}
	prov.SetModels([]entity.Model{m})
	providers := repoprovider.NewProviderRepository(t.TempDir(), nil)
	if err := providers.Save(t.Context(), &prov); err != nil {
		t.Fatal(err)
	}
	return Deps{
		SummaryTool: mustSummaryTool(t),
		Convs:       repoconversation.NewConversationRepository(t.TempDir()),
		Providers:   providers,
		Environments: map[enum.Environment]EnvironmentDriver{
			enum.EnvironmentLocal: EnvironmentBundle{
				WorkspaceStore: repoworkspace.NewLocal(),
				DefaultRoot:    filepath.Join(t.TempDir(), "home"),
				Tools:          tools,
				Services:       map[enum.Style]dservice.TextModelService{enum.StyleCompletion: svc},
			},
		},
		Config: config.ConversationConfig{MaxTurnStep: 4, CompactBudgetPct: 100, CompactKeepTail: 1},
	}
}

// symbolDriver returns the deps' local bundle with tools replaced — the
// driver a test hands runTurn or appendToolContexts directly.
func symbolDriver(t *testing.T, d Deps, tools map[string]dservice.Tool) EnvironmentBundle {
	t.Helper()
	driver, ok := d.Environments[enum.EnvironmentLocal].(EnvironmentBundle)
	if !ok {
		t.Fatal("local environment is not a bundle")
	}
	driver.Tools = tools
	return driver
}

// A tree that changes on every pass re-renders a whole map per turn under
// value dedup. A one-shot slot is skipped by presence instead: the first
// render is kept verbatim and the later passes cost neither the render nor
// a probe of the map.
func TestOneShotRecordSurvivesChanges(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	conv := seedRoom(t, d.Convs, "map-cadence", "go")
	root := mappedRoot(t)
	conv.SetWorkspace(root)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		return fmt.Sprintf("symbol map for %s — render %d", root, pass)
	})
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	ctx := dservice.WithWorkspaceDir(t.Context(), root)

	for pass := 1; pass <= 3; pass++ {
		if err := d.appendToolContexts(ctx, conv.ID(), conv, driver, root); err != nil {
			t.Fatal(err)
		}
		records := mapRecords(conv)
		if len(records) != 1 {
			t.Fatalf("pass %d: live records = %d, want the one orientation map", pass, len(records))
		}
		if want := fmt.Sprintf("symbol map for %s — render 1", root); records[0].Output() != want {
			t.Fatalf("pass %d output = %q, want the first render kept verbatim", pass, records[0].Output())
		}
	}
	if tool.passes != 1 {
		t.Fatalf("tool rendered %d times, want 1 — a filled slot must not pay its render", tool.passes)
	}
	stored, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(stored)); got != 1 {
		t.Fatalf("persisted records = %d, want 1", got)
	}
}

// The model may call symbol_map itself at any time. That call is history,
// not the room's orientation: it never fills the hydration slot.
func TestModelCallDoesNotFillSlot(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	conv := seedRoom(t, d.Convs, "map-on-demand", "go")
	root := mappedRoot(t)
	conv.SetWorkspace(root)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	asked, err := entity.NewMessage(enum.RoleAssistant, "mapping the workspace")
	if err != nil {
		t.Fatal(err)
	}
	call, err := entity.NewToolCall("map_1", "symbol_map", fmt.Sprintf(`{"path":%q}`, root), enum.TooltypeOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if err := asked.AddToolCall(call); err != nil {
		t.Fatal(err)
	}
	if err := asked.SetToolOutput(call.ID(), "symbol map for "+root+" — on demand"); err != nil {
		t.Fatal(err)
	}
	asked.SetFinishReason(enum.FinishReasonStop)
	conv.Append(asked)
	if _, err := d.Convs.AppendMessage(t.Context(), conv.ID(), asked); err != nil {
		t.Fatal(err)
	}

	tool := newMapTool(t, func(int) string { return "symbol map for " + root + " — declarations only" })
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	ctx := dservice.WithWorkspaceDir(t.Context(), root)
	if err := d.appendToolContexts(ctx, conv.ID(), conv, driver, root); err != nil {
		t.Fatal(err)
	}
	if tool.passes != 1 {
		t.Fatalf("render passes = %d, want 1 — the model's call left the slot empty", tool.passes)
	}
	if got := len(mapRecords(conv)); got != 1 {
		t.Fatalf("hydration records = %d, want 1 beside the model's own call", got)
	}
}

// Only the one-shot slot follows the cadence. Every other record keeps
// value dedup: an unchanged render writes nothing, a changed one records.
func TestOtherRecordsKeepValueDedup(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	conv := seedRoom(t, d.Convs, "map-other-dedup", "go")
	root := mappedRoot(t)
	conv.SetWorkspace(root)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	schema, err := valueobject.NewToolSchema("skills", "catalog", nil, true, enum.ToolPermissionSystem)
	if err != nil {
		t.Fatal(err)
	}
	discovery := &skillDiscovery{schema: schema, text: "catalog A"}
	driver := symbolDriver(t, d, map[string]dservice.Tool{"skills": discovery})
	ctx := dservice.WithWorkspaceDir(t.Context(), root)
	pass := func(text string) {
		t.Helper()
		discovery.text = text
		if err := d.appendToolContexts(ctx, conv.ID(), conv, driver, root); err != nil {
			t.Fatal(err)
		}
	}

	pass("catalog A")
	if got := len(skillRecords(conv)); got != 1 {
		t.Fatalf("first pass records = %d, want 1", got)
	}
	pass("catalog A")
	if got := len(skillRecords(conv)); got != 1 {
		t.Fatalf("unchanged pass re-recorded — records = %d, want 1", got)
	}
	pass("catalog B")
	if got := len(skillRecords(conv)); got != 2 {
		t.Fatalf("changed pass records = %d, want 2", got)
	}
	if out := skillRecords(conv)[1].Output(); out != "catalog B" {
		t.Fatalf("second record = %q, want the changed render", out)
	}
}

// foldMapSvc drives a turn that folds mid-flight and records, per real
// provider call, whether the live tail carried a symbol map: the fold
// inside the turn must refill the slot before the call that follows it.
type foldMapSvc struct {
	realCalls int
	sawMap    []bool
}

func (s *foldMapSvc) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, _ entity.Model, _ event.TurnEventHandler) error {
	for _, tool := range conv.Tools() {
		if tool.Name() == toolsummary.ToolName {
			m, err := entity.NewMessage(enum.RoleAssistant, "")
			if err != nil {
				return err
			}
			call, err := entity.NewToolCall("sum_1", toolsummary.ToolName, `{"text":"CHECKPOINT"}`, enum.TooltypeOrigin)
			if err != nil {
				return err
			}
			if err := m.AddToolCall(call); err != nil {
				return err
			}
			m.SetFinishReason(enum.FinishReasonToolCalls)
			conv.Append(m)
			return nil
		}
	}
	s.realCalls++
	s.sawMap = append(s.sawMap, len(mapRecords(conv)) > 0)
	var m entity.Message
	if s.realCalls == 1 {
		m, _ = entity.NewMessage(enum.RoleAssistant, "need a tool")
		call, err := entity.NewToolCall("call_1", "push", `{}`, enum.TooltypeOrigin)
		if err != nil {
			return err
		}
		if err := m.AddToolCall(call); err != nil {
			return err
		}
		m.SetFinishReason(enum.FinishReasonToolCalls)
	} else {
		m, _ = entity.NewMessage(enum.RoleAssistant, "done")
		m.SetFinishReason(enum.FinishReasonStop)
	}
	conv.Append(m)
	return nil
}

// A fold inside a turn seals the record with the history it described, so
// the same step must render one fresh map before the provider call that
// follows: the turn that triggered the fold keeps its orientation.
func TestAutoFoldRefillsSlotInSameTurn(t *testing.T) {
	svc := &foldMapSvc{}
	d := symbolDeps(t, svc, nil)
	conv := seedRoom(t, d.Convs, "map-fold", "go")
	root := mappedRoot(t)
	conv.SetWorkspace(root)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		return fmt.Sprintf("symbol map for %s — render %d", root, pass)
	})
	pushSchema, err := valueobject.NewToolSchema("push", "test tool", nil, true, enum.ToolPermissionAgent)
	if err != nil {
		t.Fatal(err)
	}
	driver := symbolDriver(t, d, map[string]dservice.Tool{
		"push":       fatTool{schema: pushSchema},
		"symbol_map": tool,
	})
	// A window barely over the output reserve leaves a 500-token input
	// allowance: the seed turn fits, the fat tool result overflows it.
	model := entity.Model{}
	model.SetContextWindow(entity.DefaultUnknownMaxOutputTokens + 500)

	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, model, svc, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if conv.Messages()[0].UserType() != enum.LLMUserTypeCompaction {
		t.Fatal("fixture did not fold")
	}
	records := mapRecords(conv)
	if len(records) != 1 {
		t.Fatalf("live records = %d, want exactly the post-fold map", len(records))
	}
	if want := fmt.Sprintf("symbol map for %s — render 2", root); records[0].Output() != want {
		t.Fatalf("live record = %q, want the post-fold render", records[0].Output())
	}
	if tool.passes != 2 {
		t.Fatalf("renders = %d, want one before and one after the fold", tool.passes)
	}
	if len(svc.sawMap) != 2 || !svc.sawMap[0] || !svc.sawMap[1] {
		t.Fatalf("provider calls saw a map = %v, want [true true]", svc.sawMap)
	}
}

// A manual compact is its own job: it seals the record and writes no
// replacement. The room's next turn fills the slot in its first step.
func TestManualCompactFillsSlotOnNextTurn(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	conv := seedRoom(t, d.Convs, "map-compact", "go")
	root := mappedRoot(t)
	conv.SetWorkspace(root)
	conv.SetProviderID("gw1")
	conv.SetModelID("m1")
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		return fmt.Sprintf("symbol map for %s — render %d", root, pass)
	})
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 1 {
		t.Fatalf("first turn records = %d, want 1", got)
	}

	compact := &Compact{Deps: d, Ctx: t.Context()}
	compact.Bind(conv.ID(), nil)
	if err := compact.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	sealed, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(sealed)); got != 0 {
		t.Fatalf("compact wrote %d maps, want none — the slot belongs to the next turn", got)
	}
	if tool.passes != 1 {
		t.Fatalf("compact rendered the map %d times, want none", tool.passes-1)
	}

	user, err := entity.NewMessage(enum.RoleUser, "again")
	if err != nil {
		t.Fatal(err)
	}
	sealed.Append(user)
	if _, err := d.Convs.AppendMessage(t.Context(), conv.ID(), user); err != nil {
		t.Fatal(err)
	}
	if err := d.runTurn(t.Context(), conv.ID(), sealed, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	records := mapRecords(sealed)
	if len(records) != 1 {
		t.Fatalf("next turn records = %d, want one fresh map", len(records))
	}
	if want := fmt.Sprintf("symbol map for %s — render 2", root); records[0].Output() != want {
		t.Fatalf("refilled record = %q, want the render after the fold", records[0].Output())
	}
}

// A cold root answers with the "being built" marker first. That render is
// provisional: it never becomes the room's orientation, so the first turn
// records nothing and the next one records the final map.
func TestColdFirstTurnRecordsNothing(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	conv := seedRoom(t, d.Convs, "map-cold", "go")
	root := mappedRoot(t)
	conv.SetWorkspace(root)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		if pass == 1 {
			return "symbol map for " + root + " is being built — it will appear on a later turn."
		}
		return "symbol map for " + root + " — declarations only"
	})
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})

	sawRecord := false
	svc := doneSvc{onSend: func() { sawRecord = sawRecord || len(mapRecords(conv)) > 0 }}
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, svc, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if sawRecord {
		t.Fatal("the provider call saw a provisional map record")
	}
	if got := len(mapRecords(conv)); got != 0 {
		t.Fatalf("cold first turn records = %d, want none", got)
	}

	user, err := entity.NewMessage(enum.RoleUser, "again")
	if err != nil {
		t.Fatal(err)
	}
	conv.Append(user)
	if _, err := d.Convs.AppendMessage(t.Context(), conv.ID(), user); err != nil {
		t.Fatal(err)
	}
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	records := mapRecords(conv)
	if len(records) != 1 {
		t.Fatalf("records after the build landed = %d, want 1", len(records))
	}
	if want := "symbol map for " + root + " — declarations only"; records[0].Output() != want {
		t.Fatalf("record = %q, want the final map", records[0].Output())
	}
	if tool.passes != 2 {
		t.Fatalf("renders = %d, want the provisional retry then the final map", tool.passes)
	}
}

// appendUser adds one editable user line and persists it — the shape
// runTurn expects between turns.
func appendUser(t *testing.T, d Deps, conv *aggregate.Conversation, text string) {
	t.Helper()
	user, err := entity.NewMessage(enum.RoleUser, text)
	if err != nil {
		t.Fatal(err)
	}
	conv.Append(user)
	if _, err := d.Convs.AppendMessage(t.Context(), conv.ID(), user); err != nil {
		t.Fatal(err)
	}
}

// The fallback root is skipped even when it carries a marker: a home that
// happens to be a dotfiles repo is not the project the room works in.
func TestGateSkipsFallbackHomeEvenWithMarker(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	home := mappedRoot(t)
	conv := seedRoom(t, d.Convs, "map-home", "hi")
	conv.SetWorkspace(home)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(int) string { return "symbol map for " + home })
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	driver.DefaultRoot = home
	ctx := dservice.WithWorkspaceDir(t.Context(), home)
	if err := d.appendToolContexts(ctx, conv.ID(), conv, driver, home); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 0 {
		t.Fatalf("fallback home records = %d, want none", got)
	}
	if tool.passes != 0 {
		t.Fatalf("the gate still rendered the map %d times", tool.passes)
	}
	if got := len(conv.Messages()); got != 1 {
		t.Fatalf("history = %d messages, want the seed line and nothing else", got)
	}
}

// A root with no project marker is skipped before any render: no record,
// no probe, no "skipped" line — the agent is still writing that folder, so
// a map would only repeat what it already knows.
func TestGateSkipsRootWithoutMarker(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	root := t.TempDir()
	conv := seedRoom(t, d.Convs, "map-unmarked", "hi")
	conv.SetWorkspace(root)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(int) string { return "symbol map for " + root })
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	ctx := dservice.WithWorkspaceDir(t.Context(), root)
	if err := d.appendToolContexts(ctx, conv.ID(), conv, driver, root); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 0 {
		t.Fatalf("unmarked root records = %d, want none", got)
	}
	if tool.passes != 0 {
		t.Fatalf("the gate still rendered the map %d times", tool.passes)
	}
	if got := len(conv.Messages()); got != 1 {
		t.Fatalf("history = %d messages, want the seed line and nothing else", got)
	}
}

// A marker at the root is the evidence the gate wants, whatever its kind:
// a manifest, a version-control directory, or a glob-only project file.
func TestGateHydratesMarkerRoot(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{name: "manifest", setup: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "vcs dir", setup: func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "glob marker", setup: func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "App.csproj"), []byte("<Project/>\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := symbolDeps(t, doneSvc{}, nil)
			root := t.TempDir()
			tc.setup(t, root)
			conv := seedRoom(t, d.Convs, "map-marked", "hi")
			conv.SetWorkspace(root)
			if err := d.Convs.Save(t.Context(), conv); err != nil {
				t.Fatal(err)
			}
			tool := newMapTool(t, func(pass int) string { return "symbol map for " + root })
			driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
			ctx := dservice.WithWorkspaceDir(t.Context(), root)
			if err := d.appendToolContexts(ctx, conv.ID(), conv, driver, root); err != nil {
				t.Fatal(err)
			}
			if got := len(mapRecords(conv)); got != 1 {
				t.Fatalf("marker root records = %d, want 1", got)
			}
			if tool.passes != 1 {
				t.Fatalf("renders = %d, want 1", tool.passes)
			}
		})
	}
}

// gatedTurns seeds a room whose root carries no project marker and lets
// the agent write source code there across turns. The gate re-reads the
// root on every step, but nothing the agent wrote is a marker, so every
// step skips the one-shot slot: no record, no render, no build.
func gatedTurns(t *testing.T, name string) (Deps, *aggregate.Conversation, *mapTool, EnvironmentBundle, string) {
	t.Helper()
	d := symbolDeps(t, doneSvc{}, nil)
	root := t.TempDir()
	conv := seedRoom(t, d.Convs, name, "write the project")
	conv.SetWorkspace(root)
	conv.SetProviderID("gw1")
	conv.SetModelID("m1")
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		return fmt.Sprintf("symbol map for %s — render %d", root, pass)
	})
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	turn := func(text string) {
		t.Helper()
		if text != "" {
			appendUser(t, d, conv, text)
		}
		if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
			t.Fatal(err)
		}
	}
	turn("")
	if got := len(mapRecords(conv)); got != 0 {
		t.Fatalf("first turn on an unmarked root records = %d, want none", got)
	}
	// The agent writes code in the root — source files, not a manifest.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	turn("I wrote the code")
	if got := len(mapRecords(conv)); got != 0 {
		t.Fatalf("a root still being written got a map — records = %d", got)
	}
	if tool.passes != 0 {
		t.Fatalf("renders = %d, want none while the gate refuses the root", tool.passes)
	}
	return d, conv, tool, driver, root
}

// A room gated out on a root that is still being written stays unmapped
// turn after turn, and a fold is the next chance rather than a bypass: a
// root that still carries no marker records nothing then either.
func TestGatedRoomStaysUnmappedUntilFold(t *testing.T) {
	d, conv, tool, driver, _ := gatedTurns(t, "map-gated")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 0 {
		t.Fatalf("records = %d, want the room unmapped while its root has no marker", got)
	}
	compact := &Compact{Deps: d, Ctx: t.Context()}
	compact.Bind(conv.ID(), nil)
	if err := compact.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	sealed, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(sealed)); got != 0 {
		t.Fatalf("the fold bypassed the gate — records = %d, want none", got)
	}
	if tool.passes != 0 {
		t.Fatalf("renders = %d, want none for a root that is not a project yet", tool.passes)
	}
}

// Once the root is a project again, the fresh tail records exactly one map
// — the fold is the next chance, and the map is paid once, not per turn.
func TestFoldRecordsOneFreshMap(t *testing.T) {
	d, conv, tool, driver, root := gatedTurns(t, "map-fold-fresh")
	if got := conv.SymbolMapRefusedAt(); got != root {
		t.Fatalf("verdict = %q, want the refused root %q", got, root)
	}
	// The agent initializes the project in that root.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module late\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compact := &Compact{Deps: d, Ctx: t.Context()}
	compact.Bind(conv.ID(), nil)
	if err := compact.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	sealed, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(sealed)); got != 0 {
		t.Fatalf("compact wrote %d maps, want none", got)
	}
	// The fold is a technical reset, so it drops the remembered refusal; the
	// next step re-evaluates the root and now finds its marker.
	if got := sealed.SymbolMapRefusedAt(); got != "" {
		t.Fatalf("verdict = %q after the fold, want it cleared", got)
	}
	appendUser(t, d, sealed, "after the fold")
	if err := d.runTurn(t.Context(), conv.ID(), sealed, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	records := mapRecords(sealed)
	if len(records) != 1 {
		t.Fatalf("records after the fold = %d, want one fresh map", len(records))
	}
	if want := fmt.Sprintf("symbol map for %s — render 1", root); records[0].Output() != want {
		t.Fatalf("fresh record = %q, want the first map of the new tail", records[0].Output())
	}
	appendUser(t, d, sealed, "one more")
	if err := d.runTurn(t.Context(), conv.ID(), sealed, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(sealed)); got != 1 {
		t.Fatalf("records = %d after another turn, want the one fresh map", got)
	}
	if got := sealed.SymbolMapRefusedAt(); got != "" {
		t.Fatalf("verdict = %q after hydrating, want none", got)
	}
	if tool.passes != 1 {
		t.Fatalf("renders = %d, want exactly the post-fold render", tool.passes)
	}
}

// A rebind to a root the gate refuses adds no record: the old record cites
// another tree, so the slot is void — and the gate keeps it void.
func TestRebindToGatedRootAddsNoRecord(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	passing, gated := mappedRoot(t), t.TempDir()
	conv := seedRoom(t, d.Convs, "map-rebind-gated", "start")
	conv.SetWorkspace(passing)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		return fmt.Sprintf("symbol map for %s — render %d", passing, pass)
	})
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 1 {
		t.Fatalf("records on the passing root = %d, want 1", got)
	}

	conv.SetWorkspace(gated)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	appendUser(t, d, conv, "moved")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 1 {
		t.Fatalf("records = %d after a gated rebind, want the old map as history only", got)
	}
	if tool.passes != 1 {
		t.Fatalf("renders = %d, want the gated rebind to render nothing", tool.passes)
	}
}

// A rebind between two roots the gate accepts refills the slot once for
// the new binding, and the following turns leave it alone.
func TestRebindBetweenGatedRootsRefillsOnce(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	first, second := mappedRoot(t), mappedRoot(t)
	conv := seedRoom(t, d.Convs, "map-rebind-two", "start")
	conv.SetWorkspace(first)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		return fmt.Sprintf("symbol map — render %d", pass)
	})
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 1 {
		t.Fatalf("records on the first root = %d, want 1", got)
	}

	conv.SetWorkspace(second)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	appendUser(t, d, conv, "moved")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	records := mapRecords(conv)
	if len(records) != 2 {
		t.Fatalf("records = %d after the rebind, want the new map beside the old one", len(records))
	}
	if want := "symbol map — render 2"; records[1].Output() != want {
		t.Fatalf("refilled record = %q, want a map of the new root", records[1].Output())
	}
	if got := records[1].Input(); !strings.Contains(got, second) {
		t.Fatalf("refilled input = %q, want it to cite the new root", got)
	}

	appendUser(t, d, conv, "still here")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 2 {
		t.Fatalf("records = %d, want the rebind to refill once", got)
	}
	if tool.passes != 2 {
		t.Fatalf("renders = %d, want one per binding", tool.passes)
	}
}

// A room that starts gated out and is rebound to a root the gate accepts
// records one map for the new binding — the rebind is the opportunity.
func TestRebindFromGatedToPassingRootRecordsOnce(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	gated, passing := t.TempDir(), mappedRoot(t)
	conv := seedRoom(t, d.Convs, "map-rebind-out", "start")
	conv.SetWorkspace(gated)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		return fmt.Sprintf("symbol map for %s — render %d", passing, pass)
	})
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 0 {
		t.Fatalf("records on the gated root = %d, want none", got)
	}

	conv.SetWorkspace(passing)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	appendUser(t, d, conv, "moved")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	records := mapRecords(conv)
	if len(records) != 1 {
		t.Fatalf("records after the rebind = %d, want 1", len(records))
	}
	if !strings.Contains(records[0].Input(), passing) {
		t.Fatalf("record input = %q, want it to cite the passing root", records[0].Input())
	}
	appendUser(t, d, conv, "one more")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 1 {
		t.Fatalf("records = %d after another turn, want the one refill", got)
	}
}

// probeCountingWorkspace counts the sessions the hydration gate opens — one
// probe is one Open — so a test can prove a refused root is not probed
// again on the next step.
type probeCountingWorkspace struct {
	drepo.Workspace
	opens atomic.Int32
}

func (w *probeCountingWorkspace) Open(ctx context.Context, dir string) (drepo.WorkspaceSession, error) {
	w.opens.Add(1)
	return w.Workspace.Open(ctx, dir)
}

// markerRootedRoom seeds a room on an unmarked root and runs one turn, so
// the gate refuses the root and the room remembers it.
func markerRootedRoom(t *testing.T, name string) (Deps, *aggregate.Conversation, *mapTool, EnvironmentBundle, string) {
	t.Helper()
	d := symbolDeps(t, doneSvc{}, nil)
	root := t.TempDir()
	conv := seedRoom(t, d.Convs, name, "write the project")
	conv.SetWorkspace(root)
	conv.SetProviderID("gw1")
	conv.SetModelID("m1")
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(pass int) string {
		return fmt.Sprintf("symbol map for %s — render %d", root, pass)
	})
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := conv.SymbolMapRefusedAt(); got != root {
		t.Fatalf("verdict = %q, want the refused root %q", got, root)
	}
	return d, conv, tool, driver, root
}

// A refusal is remembered for the root: a marker appearing later in the same
// conversation does not re-open the question. A fold does. This is the test
// a stateless gate fails — it would record a map on the second turn.
func TestMarkerAppearingMidConversationStaysUnmapped(t *testing.T) {
	d, conv, tool, driver, root := markerRootedRoom(t, "map-late-marker")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module late\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	appendUser(t, d, conv, "the project exists now")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(conv)); got != 0 {
		t.Fatalf("a marker appearing mid-conversation re-opened the gate — records = %d, want the remembered refusal", got)
	}
	if tool.passes != 0 {
		t.Fatalf("renders = %d, want none while the root is refused", tool.passes)
	}
	if got := conv.SymbolMapRefusedAt(); got != root {
		t.Fatalf("verdict = %q, want the refused root %q", got, root)
	}

	// The fold is the next chance, and the root is a project by then.
	compact := &Compact{Deps: d, Ctx: t.Context()}
	compact.Bind(conv.ID(), nil)
	if err := compact.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	sealed, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := sealed.SymbolMapRefusedAt(); got != "" {
		t.Fatalf("verdict = %q after the fold, want it cleared", got)
	}
	appendUser(t, d, sealed, "after the fold")
	if err := d.runTurn(t.Context(), conv.ID(), sealed, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := len(mapRecords(sealed)); got != 1 {
		t.Fatalf("records after the fold = %d, want one map for the now-marked root", got)
	}
}

// The remembered refusal is the point of the state: the next step does not
// open the root again. A different root is a new question and pays a probe.
func TestRefusedRootIsNotReprobed(t *testing.T) {
	d, conv, _, driver, _ := markerRootedRoom(t, "map-refused-probe")
	counter := &probeCountingWorkspace{Workspace: repoworkspace.NewLocal()}
	driver.WorkspaceStore = counter
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := counter.opens.Load(); got != 0 {
		t.Fatalf("probes = %d on a refused root, want none", got)
	}
	appendUser(t, d, conv, "still here")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := counter.opens.Load(); got != 0 {
		t.Fatalf("probes = %d after another step on the refused root, want none", got)
	}

	// A different unmarked root re-evaluates: one probe, one new verdict.
	other := t.TempDir()
	conv.SetWorkspace(other)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	appendUser(t, d, conv, "moved")
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := counter.opens.Load(); got != 1 {
		t.Fatalf("probes = %d after the rebind, want exactly the new root's probe", got)
	}
	if got := conv.SymbolMapRefusedAt(); got != other {
		t.Fatalf("verdict = %q, want the newly refused root %q", got, other)
	}
}

// The verdict is room state persisted in meta.json, restored by Load: a
// restart must not pay the probe again.
func TestVerdictSurvivesReload(t *testing.T) {
	store := t.TempDir()
	d := symbolDeps(t, doneSvc{}, nil)
	d.Convs = repoconversation.NewConversationRepository(store)
	root := t.TempDir()
	conv := seedRoom(t, d.Convs, "map-verdict-reload", "go")
	conv.SetWorkspace(root)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(int) string { return "symbol map for " + root })
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := conv.SymbolMapRefusedAt(); got != root {
		t.Fatalf("verdict = %q, want %q", got, root)
	}

	// meta.json carries the field — one string, omitted when empty.
	raw, err := os.ReadFile(filepath.Join(store, conv.ID(), "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"symbolMapRefusedAt":"`+root+`"`) {
		t.Fatalf("meta.json does not carry the verdict:\n%s", raw)
	}

	reloaded, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.SymbolMapRefusedAt(); got != root {
		t.Fatalf("verdict after reload = %q, want %q", got, root)
	}
	// The restored room does not probe the root again.
	counter := &probeCountingWorkspace{Workspace: repoworkspace.NewLocal()}
	driver.WorkspaceStore = counter
	appendUser(t, d, reloaded, "after restart")
	if err := d.runTurn(t.Context(), conv.ID(), reloaded, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := counter.opens.Load(); got != 0 {
		t.Fatalf("probes = %d on a restored refusal, want none", got)
	}
}

// /clear rebuilds the record from ClearHistory, which drops the verdict: the
// same root is a new question afterwards.
func TestClearDropsVerdict(t *testing.T) {
	d, conv, _, driver, root := markerRootedRoom(t, "map-clear-verdict")
	clear := &Clear{Deps: d, Ctx: t.Context()}
	clear.Bind(conv.ID(), nil)
	if err := clear.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	cleared, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := cleared.SymbolMapRefusedAt(); got != "" {
		t.Fatalf("verdict = %q after clear, want it dropped", got)
	}
	// The emptied room probes the same root again and reaches the same
	// verdict — a fresh episode, not a remembered one.
	counter := &probeCountingWorkspace{Workspace: repoworkspace.NewLocal()}
	driver.WorkspaceStore = counter
	appendUser(t, d, cleared, "fresh start")
	if err := d.runTurn(t.Context(), conv.ID(), cleared, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := counter.opens.Load(); got != 1 {
		t.Fatalf("probes = %d after clear, want the one fresh probe", got)
	}
	if got := cleared.SymbolMapRefusedAt(); got != root {
		t.Fatalf("verdict = %q, want the root refused again", got)
	}
}

// A fork is a new room: it inherits the content, not the gate verdict, and
// reaches the same answer on the same root with one probe.
func TestForkDoesNotCopyVerdict(t *testing.T) {
	d, conv, tool, driver, root := markerRootedRoom(t, "map-fork-verdict")
	dst, err := conv.Fork()
	if err != nil {
		t.Fatal(err)
	}
	if got := dst.SymbolMapRefusedAt(); got != "" {
		t.Fatalf("fork verdict = %q, want none", got)
	}
	fork := &Fork{Deps: d, Ctx: t.Context(), Dst: dst}
	fork.Bind(conv.ID(), nil)
	if err := fork.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	copied, err := d.Convs.Load(t.Context(), dst.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := copied.SymbolMapRefusedAt(); got != "" {
		t.Fatalf("stored fork verdict = %q, want none", got)
	}
	// The copy's first step probes that root itself.
	counter := &probeCountingWorkspace{Workspace: repoworkspace.NewLocal()}
	driver.WorkspaceStore = counter
	appendUser(t, d, copied, "first fork turn")
	if err := d.runTurn(t.Context(), conv.ID(), copied, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := counter.opens.Load(); got != 1 {
		t.Fatalf("probes = %d on the fork's first step, want one", got)
	}
	if got := copied.SymbolMapRefusedAt(); got != root {
		t.Fatalf("fork verdict = %q, want %q after its own probe", got, root)
	}
	if tool.passes != 0 {
		t.Fatalf("renders = %d, want none for the refused root", tool.passes)
	}
}

// The fallback check is free, so it stores nothing: a home root — even one
// that looks like a project — is skipped without a verdict.
func TestFallbackRootStoresNoVerdict(t *testing.T) {
	d := symbolDeps(t, doneSvc{}, nil)
	home := mappedRoot(t)
	conv := seedRoom(t, d.Convs, "map-home-verdict", "hi")
	conv.SetWorkspace(home)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	tool := newMapTool(t, func(int) string { return "symbol map for " + home })
	driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
	driver.DefaultRoot = home
	if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	if got := conv.SymbolMapRefusedAt(); got != "" {
		t.Fatalf("verdict = %q for the fallback root, want none stored", got)
	}
	if tool.passes != 0 {
		t.Fatalf("renders = %d, want none", tool.passes)
	}
	reloaded, err := d.Convs.Load(t.Context(), conv.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.SymbolMapRefusedAt(); got != "" {
		t.Fatalf("stored verdict = %q for the fallback root, want none", got)
	}
}

// faultyWorkspace is a real workspace with one injected failure per probe
// step: opening the root, stat-ing a marker, or listing the root. Each mode
// models a broken link or a permission problem — evidence about the probe,
// never about the tree.
type faultyWorkspace struct {
	drepo.Workspace
	openErr    error
	statErr    error
	listingErr error
	nilFS      bool
}

func (w faultyWorkspace) Open(ctx context.Context, dir string) (drepo.WorkspaceSession, error) {
	if w.openErr != nil {
		return nil, w.openErr
	}
	session, err := w.Workspace.Open(ctx, dir)
	if err != nil {
		return nil, err
	}
	return faultySession{
		WorkspaceSession: session,
		statErr:          w.statErr,
		listingErr:       w.listingErr,
		nilFS:            w.nilFS,
	}, nil
}

type faultySession struct {
	drepo.WorkspaceSession
	statErr    error
	listingErr error
	nilFS      bool
}

func (s faultySession) Stat(name string) (fs.FileInfo, error) {
	if s.statErr != nil {
		return nil, s.statErr
	}
	return s.WorkspaceSession.Stat(name)
}

func (s faultySession) FS() fs.FS {
	if s.nilFS {
		return nil
	}
	if s.listingErr != nil {
		return failingFS{err: s.listingErr}
	}
	return s.WorkspaceSession.FS()
}

// failingFS is a root listing that always fails — a remote whose directory
// read errors out.
type failingFS struct{ err error }

func (f failingFS) Open(string) (fs.File, error) { return nil, f.err }

// An unreadable root is not an evaluated root: a failed open, a failed marker
// stat, or a failed listing skips the step without a verdict, so the next turn
// probes again instead of trusting a refusal the fault invented.
func TestUnreadableRootIsNotRemembered(t *testing.T) {
	cases := []struct {
		name string
		// marked seeds a project marker before the first turn: the fault
		// must hide it without turning into a refusal.
		marked bool
		fault  faultyWorkspace
	}{
		{name: "open fails", marked: true, fault: faultyWorkspace{openErr: fs.ErrPermission}},
		{name: "marker stat fails", marked: true, fault: faultyWorkspace{statErr: fs.ErrPermission}},
		{name: "listing has no filesystem", fault: faultyWorkspace{nilFS: true}},
		{name: "listing fails", fault: faultyWorkspace{listingErr: fs.ErrPermission}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := repoworkspace.NewLocal()
			d := symbolDeps(t, doneSvc{}, nil)
			root := t.TempDir()
			if tc.marked {
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			conv := seedRoom(t, d.Convs, "map-unreadable", "go")
			conv.SetWorkspace(root)
			if err := d.Convs.Save(t.Context(), conv); err != nil {
				t.Fatal(err)
			}
			tool := newMapTool(t, func(pass int) string {
				return fmt.Sprintf("symbol map for %s — render %d", root, pass)
			})
			driver := symbolDriver(t, d, map[string]dservice.Tool{"symbol_map": tool})
			fault := tc.fault
			fault.Workspace = store
			driver.WorkspaceStore = fault

			if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
				t.Fatal(err)
			}
			if got := conv.SymbolMapRefusedAt(); got != "" {
				t.Fatalf("verdict = %q after an unreadable probe, want none remembered", got)
			}
			if got := len(mapRecords(conv)); got != 0 {
				t.Fatalf("records = %d from an unreadable root, want none", got)
			}
			if tool.passes != 0 {
				t.Fatalf("renders = %d, want none while the root is unreadable", tool.passes)
			}

			// The fault clears and the root is a project: the next turn probes
			// again and records, which a remembered refusal would prevent.
			if !tc.marked {
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			driver.WorkspaceStore = store
			appendUser(t, d, conv, "readable again")
			if err := d.runTurn(t.Context(), conv.ID(), conv, driver, nil, entity.Model{}, doneSvc{}, nil, nil, valueobject.TurnOrigin{}); err != nil {
				t.Fatal(err)
			}
			if got := len(mapRecords(conv)); got != 1 {
				t.Fatalf("records = %d once the root is readable and marked, want 1 — the probe was not repeated", got)
			}
			if tool.passes != 1 {
				t.Fatalf("renders = %d, want the one render after the root became readable", tool.passes)
			}
			if got := conv.SymbolMapRefusedAt(); got != "" {
				t.Fatalf("verdict = %q after a successful probe, want none", got)
			}
		})
	}
}
