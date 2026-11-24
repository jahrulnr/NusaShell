//go:build repomap

// Selection-policy benchmark for the symbol map. It answers "which policy
// and which token budget" with numbers, not opinion: a fixed target set per
// repo, the policy × budget grid, and four concrete metrics.
//
//	repomap -bench -root .            # NusaShell target set
//	repomap -bench -root ../remotion  # remotion target set
//	repomap -bench -root X -targets t.json  # custom targets
//
// Target selection is by repo basename; unknown roots need -targets.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/service/symbol"
)

// target is one "thing a person would search for": the file it lives in and
// the symbol name as rendered (Owner.Name flattened — the name half).
type target struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// targetSets are keyed by the workspace root's basename. They are hand-picked
// to cover entry points and major types spread over many top-level dirs —
// the distribution is the point; a set that lives in one directory measures
// nothing about selection policy.
var targetSets = map[string][]target{
	"NusaShell": {
		{"application/agent/agent.go", "AgentApp"},
		{"application/conversation/conversation.go", "ConversationApp"},
		{"application/setting/setting.go", "SettingApp"},
		{"application/mcp/mcp.go", "Runtime"},
		{"application/browser/annotate.go", "UseAnnotations"},
		{"application/provider/autoimport.go", "AutoImportResult"},
		{"application/auth/auth.go", "AuthApp"},
		{"application/channel/catalog.go", "Agents"},
		{"application/control/activity.go", "ActivityEntry"},
		{"application/job/agent.go", "runAgentTurn"},
		{"application/memory/consolidate.go", "Generator"},
		{"application/automation/automation.go", "Runner"},
		{"application/notify/notify.go", "Rooms"},
		{"application/update/update.go", "Info"},
		{"application/websearch/websearch.go", "Settings"},
		{"application/media/media.go", "Assignment"},
		{"application/project/project.go", "Rooms"},
		{"infrastructure/domain/aggregate/agent.go", "Agent"},
		{"infrastructure/domain/service/agent_runtime.go", "Elicitor"},
		{"infrastructure/repository/workspace/workspace.go", "Local"},
		{"infrastructure/repository/store/store.go", "Open"},
		{"infrastructure/service/tool/file/search.go", "FileSearchTool"},
		{"infrastructure/service/tool/terminal/exec.go", "ExecTool"},
		{"infrastructure/service/agent/acp/client.go", "ClientConfig"},
		{"infrastructure/service/mcp/client.go", "Client"},
		{"infrastructure/config/agents.go", "AgentsConfig"},
		{"infrastructure/worker/worker.go", "Job"},
		{"infrastructure/system/node.go", "NodeDir"},
		{"infrastructure/delivery/http/agent/agent.go", "Service"},
		{"infrastructure/delivery/cli/admin/admin.go", "Boot"},
		{"infrastructure/error/error.go", "IAppError"},
		{"infrastructure/redact/redact.go", "Value"},
		{"infrastructure/prompt/prompt.go", "SystemPrompt"},
		{"cmd/nusashell/main.go", "main"},
		{"web/app.js", "App"},
		{"docs/api/openapi_test.go", "TestOpenAPIIsConsistent"},
		{"assets/skills/embed.go", "Builtins"},
	},
	"remotion": {
		{"video/src/components/Karaoke.tsx", "Karaoke"},
		{"video/src/components/BibCard.tsx", "BibCard"},
		{"video/src/components/Checklist.tsx", "CheckItem"},
		{"video/src/components/DeskVisuals.tsx", "MismatchCards"},
		{"video/src/components/Fx.tsx", "Cam"},
		{"video/src/components/Icons.tsx", "base"},
		{"video/src/linefollower/kit.tsx", "atWord"},
		{"video/src/linefollower/kit.tsx", "poseAt"},
		{"video/src/linefollower/kit.tsx", "StaticGrain"},
		{"video/src/linefollower/Bench3D.tsx", "Cam"},
		{"video/src/linefollower/Pcb.tsx", "Trace"},
		{"video/src/linefollower/timing.ts", "Entry"},
		{"video/src/journal/SimReport.tsx", "Src"},
		{"video/src/journal/IndexGauge.tsx", "ValueKey"},
		{"video/src/journal/CapStrip.tsx", "chunk"},
		{"video/src/LineFollowerLoop.tsx", "LineFollowerLoop"},
		{"video/src/LineFollowerLoop3D.tsx", "LineFollowerLoop3D"},
		{"video/src/JournalReel.tsx", "JournalReel"},
		{"video/src/LineFollowerReel.tsx", "trackX"},
		{"video/src/CraftLab.tsx", "Grid"},
		{"video/src/DeskReel.tsx", "Sfx"},
		{"video/src/scenes_deskreject.tsx", "atWord"},
		{"video/src/scenes_journal.tsx", "Desk"},
		{"video/src/AtomLab.tsx", "rng"},
		{"video/src/EngineLab.tsx", "pulse"},
		{"video/scripts/gen-journal-sfx.mjs", "wav"},
		{"video/scripts/gen-sfx.mjs", "wav"},
		{"pipeline/scripts/narrate.py", "keywords"},
		{"pipeline/scripts/narrate_gptaudio.py", "generate"},
		{"pipeline/scripts/narrate_journal.py", "main"},
		{"pipeline/scripts/narrate_heygen.py", "tts"},
		{"pipeline/scripts/vc_journal.py", "dur"},
	},
}

// cell is one grid point: named policy × budget.
type cell struct {
	name string
	pol  symbol.Policy
}

func gridCells(budgets []int) []cell {
	var out []cell
	for _, b := range budgets {
		out = append(out,
			cell{"A lexical", symbol.Policy{Budget: b}},
			cell{"B quota25", symbol.Policy{Budget: b, DirQuota: 0.25}},
			cell{"B quota50", symbol.Policy{Budget: b, DirQuota: 0.50}},
			cell{"C +public", symbol.Policy{Budget: b, DirQuota: 0.25, PublicFirst: true}},
			cell{"D c12", symbol.Policy{Budget: b, DirQuota: 0.25, PublicFirst: true, ShallowFirst: true, MaxPerFile: 12}},
			cell{"D c24", symbol.Policy{Budget: b, DirQuota: 0.25, PublicFirst: true, ShallowFirst: true, MaxPerFile: 24}},
			cell{"D c48", symbol.Policy{Budget: b, DirQuota: 0.25, PublicFirst: true, ShallowFirst: true, MaxPerFile: 48}},
			cell{"E c12+kinds", symbol.Policy{Budget: b, DirQuota: 0.25, PublicFirst: true, ShallowFirst: true, MaxPerFile: 12, KindFirst: true}},
			cell{"D c12 q50", symbol.Policy{Budget: b, DirQuota: 0.50, PublicFirst: true, ShallowFirst: true, MaxPerFile: 12}},
		)
	}
	return out
}

// metrics is one measured row.
type metrics struct {
	label        string
	budget       int
	hitRate      float64
	dirCoverage  float64
	publicShare  float64
	tokens       int
	firstHitRank int
	hits         int
	targets      int
}

func measure(syms []svc.Symbol, targets []target, c cell) metrics {
	text, emitted := symbol.RenderDetail(syms, c.pol)
	emittedSet := map[[2]string]int{}
	allDirs := map[string]bool{}
	hitDirs := map[string]bool{}
	for _, s := range syms {
		allDirs[topDir(s.Path)] = true
	}
	pub := 0
	for i, s := range emitted {
		emittedSet[[2]string{s.Path, s.Name}] = i
		hitDirs[topDir(s.Path)] = true
		if s.Public {
			pub++
		}
	}
	hits, first := 0, -1
	for _, t := range targets {
		if idx, ok := emittedSet[[2]string{t.Path, t.Name}]; ok {
			hits++
			if first < 0 || idx < first {
				first = idx
			}
		}
	}
	m := metrics{
		label: c.name, budget: c.pol.Budget,
		hits: hits, targets: len(targets),
		hitRate:      float64(hits) / float64(len(targets)),
		dirCoverage:  float64(len(hitDirs)) / float64(len(allDirs)),
		tokens:       len(text) / symbol.CharsPerToken,
		firstHitRank: first,
	}
	if len(emitted) > 0 {
		m.publicShare = float64(pub) / float64(len(emitted))
	}
	return m
}

func topDir(p string) string {
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

func runBench(ctx context.Context, idx svc.SymbolIndex, wsroot svc.SymbolRoot, targetsPath string) error {
	targets, ok := targetSets[filepath.Base(strings.TrimSuffix(wsrootName(wsroot), "/"))]
	if targetsPath != "" {
		b, err := os.ReadFile(targetsPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &targets); err != nil {
			return fmt.Errorf("targets %s: %w", targetsPath, err)
		}
		ok = true
	}
	if !ok {
		keys := make([]string, 0, len(targetSets))
		for k := range targetSets {
			keys = append(keys, k)
		}
		return fmt.Errorf("no built-in target set for %q (have: %s); pass -targets FILE.json",
			filepath.Base(wsrootName(wsroot)), strings.Join(keys, ", "))
	}

	syms, err := idx.Index(ctx, wsroot)
	if err != nil {
		return err
	}
	budgets := []int{1000, 2000, 3000, 4000, 5000}
	fmt.Printf("# bench: %d symbols, %d targets, root %s\n\n", len(syms), len(targets), wsrootName(wsroot))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "policy\tbudget\thitRate\tdirCov\tpubShare\ttokens\tfirstHit")
	for _, c := range gridCells(budgets) {
		m := measure(syms, targets, c)
		fmt.Fprintf(w, "%s\t%d\t%.0f%% (%d/%d)\t%.0f%%\t%.0f%%\t%d\t%d\n",
			m.label, m.budget, m.hitRate*100, m.hits, m.targets,
			m.dirCoverage*100, m.publicShare*100, m.tokens, m.firstHitRank)
	}
	w.Flush()
	return nil
}

// wsrootName is a tiny escape hatch: the CLI wraps the root, we only need a
// display/lookup name. WorkspaceRoot keeps its dir for this.
func wsrootName(r svc.SymbolRoot) string {
	if n, ok := r.(interface{ Name() string }); ok {
		return n.Name()
	}
	return "."
}
