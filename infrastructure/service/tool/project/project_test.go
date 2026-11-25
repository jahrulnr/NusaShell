package toolproject

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
)

// The schema must stay system-only: the record is harness-written
// context, and an agent-callable copy would double the catalog name on
// the provider's tool list.
func TestProjectContextSchemaIsSystemOnly(t *testing.T) {
	tool, err := NewProjectContextTool(repoworkspace.NewLocal(), "/x")
	if err != nil {
		t.Fatal(err)
	}
	s := tool.Schema()
	if s.Name() != ToolName {
		t.Fatalf("schema name = %q, want %s", s.Name(), ToolName)
	}
	if s.Permission() != enum.ToolPermissionSystem {
		t.Fatalf("permission = %v, want system", s.Permission())
	}
	if s.CallableByAgent() {
		t.Fatal("project_context must not be agent-callable")
	}
	if !s.CallableBySystem() || !s.Enabled() {
		t.Fatal("project_context must be enabled and system-callable")
	}
}

func mkTool(t *testing.T) (dservice.Tool, context.Context, string) {
	t.Helper()
	dir := t.TempDir()
	tool, err := NewProjectContextTool(repoworkspace.NewLocal(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return tool, dservice.WithWorkspaceDir(context.Background(), dir), dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A workspace with conventions and subdirs renders both: the AGENTS.md
// excerpt and a map that names root entries, one level of children, and
// a file count per directory — never deeper listings.
func TestExecuteRendersAgentsAndMap(t *testing.T) {
	tool, ctx, dir := mkTool(t)
	write(t, dir, "AGENTS.md", "repo rules: be terse")
	write(t, dir, "Makefile", "all:")
	write(t, dir, "cmd/app/main.go", "package main")
	write(t, dir, "cmd/app/util.go", "package main")
	write(t, dir, "cmd/tool/x.go", "package tool")
	write(t, dir, "docs/deep/inner/f.go", "x")
	out, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"project context for " + dir,
		"repo rules: be terse",
		"- Makefile",
		"- cmd/ — 3 files",
		"  - app/ — 2 files",
		"  - tool/ — 1 file",
		"- docs/ — 1 file",
		"  - deep/ — 1 file",
		"- AGENTS.md",
	} {
		if !strings.Contains(out.Text, want) {
			t.Fatalf("output missing %q:\n%s", want, out.Text)
		}
	}
	if strings.Contains(out.Text, "inner/") || strings.Contains(out.Text, "f.go") {
		t.Fatalf("map lists depth below one level:\n%s", out.Text)
	}
}

// Without AGENTS.md the map still renders under the marker line.
func TestExecuteWithoutAgentsFile(t *testing.T) {
	tool, ctx, dir := mkTool(t)
	write(t, dir, "src/a.go", "x")
	out, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "no AGENTS.md in this workspace") {
		t.Fatalf("missing marker:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "- src/ — 1 file") {
		t.Fatalf("map missing:\n%s", out.Text)
	}
}

// AGENTS.md is capped at 8 KiB on a rune boundary with the cut marked.
func TestExecuteTruncatesLongAgents(t *testing.T) {
	tool, ctx, dir := mkTool(t)
	write(t, dir, "AGENTS.md", strings.Repeat("x", agentsCap+500))
	out, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := out.Text
	if !strings.Contains(body, "… (truncated)") {
		t.Fatalf("cut marker missing (len %d)", len(body))
	}
	agents := strings.SplitN(body, "AGENTS.md:\n", 2)[1]
	agents = strings.SplitN(agents, "\n\nlayout:", 2)[0]
	if len(agents) > agentsCap+len("\n… (truncated)") {
		t.Fatalf("agents excerpt = %d bytes, cap %d", len(agents), agentsCap)
	}
}

// Two renders of the same tree must be byte-identical — the injection
// dedup compares verbatim, so any volatile field would re-record.
func TestExecuteIsDeterministic(t *testing.T) {
	tool, ctx, dir := mkTool(t)
	write(t, dir, "AGENTS.md", "rules")
	write(t, dir, "a/b/c.go", "x")
	write(t, dir, "a/d.go", "x")
	first, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != second.Text {
		t.Fatalf("renders differ:\n%s\n---\n%s", first.Text, second.Text)
	}
}

// The map section stays inside its line and byte budget and ends with
// the cut marker.
func TestMapStaysWithinBudget(t *testing.T) {
	tool, ctx, dir := mkTool(t)
	for i := 0; i < 80; i++ {
		write(t, dir, fmt.Sprintf("dir-%02d/f.go", i), "x")
	}
	out, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mapp := strings.SplitN(out.Text, "layout:\n", 2)[1]
	if lines := strings.Count(mapp, "\n") + 1; lines > mapLines+1 {
		t.Fatalf("map = %d lines, budget %d", lines, mapLines)
	}
	if !strings.HasSuffix(mapp, "… (truncated)") {
		t.Fatalf("oversized map must end truncated:\n%s", mapp)
	}
}

// Named noise directories are left out at every level even when nothing
// ignores them — outside a worktree nothing would.
func TestNoiseDirsAreSkipped(t *testing.T) {
	tool, ctx, dir := mkTool(t)
	for _, d := range []string{"bin", "tmp", "data", "site", "node_modules", "src/vendor", "src/tmp"} {
		write(t, dir, d+"/x.go", "x")
	}
	write(t, dir, "src/real.go", "x")
	out, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, noise := range []string{"- bin/", "- tmp/", "- data/", "- site/", "- node_modules/", "- vendor/", "- x.go"} {
		if strings.Contains(out.Text, noise) {
			t.Fatalf("noise %q rendered:\n%s", noise, out.Text)
		}
	}
	if !strings.Contains(out.Text, "- src/ — 1 file") {
		t.Fatalf("real files must still count:\n%s", out.Text)
	}
}

// Extra folders render one compact line — the AGENTS.md + layout budget
// belongs to the bound workspace alone, and a per-folder walk every
// pass would multiply the record's cost.
func TestExecuteRendersExtraFolders(t *testing.T) {
	tool, ctx, _ := mkTool(t)
	extra := t.TempDir()
	write(t, extra, "AGENTS.md", "extra rules")
	write(t, extra, "lib/y.go", "y")
	ctx = dservice.WithExtraFolders(ctx, []string{extra})
	out, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "+ "+extra+" — see its AGENTS.md if the turn moves there") {
		t.Fatalf("extra folder line missing:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "extra rules") || strings.Contains(out.Text, "extra folder") {
		t.Fatalf("extra folder must not render a full section:\n%s", out.Text)
	}
}

// The per-directory weight in the layout moves whenever unrelated files
// appear or vanish, which is not a change of the map's content. DedupKey
// normalizes those counts so the record dedups on the layout itself, and
// keeps room for the changes that do matter: a new directory, an edited
// AGENTS.md.
func TestDedupKeyIgnoresFileCounts(t *testing.T) {
	tool, ctx, dir := mkTool(t)
	write(t, dir, "AGENTS.md", "rules")
	write(t, dir, "cmd/app/main.go", "package main")
	first, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A file below the displayed depth changes both counts, no name.
	write(t, dir, "cmd/app/deep/more.go", "package deep")
	second, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text == second.Text {
		t.Fatalf("fixture must move the counts:\n%s", first.Text)
	}
	// The job's ToolDeduper is a method set, not an import: a tool declares
	// it by having DedupKey, and the adapter packages never import the job.
	deduper, ok := tool.(interface{ DedupKey(string) string })
	if !ok {
		t.Fatal("project_context must declare a dedup key")
	}
	if deduper.DedupKey(first.Text) != deduper.DedupKey(second.Text) {
		t.Fatalf("count-only renders must share a dedup key:\n%s\n---\n%s", deduper.DedupKey(first.Text), deduper.DedupKey(second.Text))
	}

	// A new directory is a real layout change.
	write(t, dir, "cmd/extra/notes.md", "x")
	third, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if deduper.DedupKey(second.Text) == deduper.DedupKey(third.Text) {
		t.Fatal("a new directory must change the dedup key")
	}

	// So is an edited AGENTS.md.
	write(t, dir, "AGENTS.md", "rules v2")
	fourth, err := tool.Execute(ctx, entity.ToolCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if deduper.DedupKey(third.Text) == deduper.DedupKey(fourth.Text) {
		t.Fatal("an edited AGENTS.md must change the dedup key")
	}
}
