// Package toolproject provides the project_context record tool: the
// workspace's AGENTS.md plus a bounded directory map, rendered once per
// pre-call hydration so the model never guesses repo conventions or
// layout. The schema rides the catalog with ToolPermissionSystem — the
// environment runs it before each provider call and persists the render
// as a deduped injection record, so the output is deliberately
// deterministic: no mtimes, byte sizes, or full listings, which would
// churn a new record on every unrelated change.
package toolproject

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ToolName is the catalog name the injection records ride under.
const ToolName = "project_context"

const (
	// agentsCap bounds the AGENTS.md excerpt; the rest is marked cut.
	agentsCap = 8 << 10
	// mapLines and mapBytes bound the layout section — room for the
	// shape of a repo, never its inventory.
	mapLines = 60
	mapBytes = 3 << 10
)

// noiseDirs are omitted at every level even when git does not ignore
// them — generated or vendored weight the map never helps with.
var noiseDirs = map[string]bool{
	".git": true, "vendor": true, "node_modules": true,
	"site": true, "data": true, "bin": true, "tmp": true,
}

// ProjectContextTool renders the workspace conventions record. All file
// access goes through the injected Workspace so a remote driver renders
// its own tree; root is the fallback working directory a workspace-less
// turn resolves to.
type ProjectContextTool struct {
	schema valueobject.ToolSchema
	store  drepo.Workspace
	root   string
}

func NewProjectContextTool(store drepo.Workspace, root string) (dservice.Tool, error) {
	if store == nil {
		return nil, errors.New("project_context needs a workspace store")
	}
	schema, err := valueobject.NewToolSchema(
		ToolName,
		"Project conventions and layout, written by the harness as an injection record; never callable by the agent.",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"absolute working directory"},"folders":{"type":"array","items":{"type":"string"},"description":"further absolute folders the file tools reach"}},"required":["path"]}`),
		true,
		enum.ToolPermissionSystem,
	)
	if err != nil {
		return nil, err
	}
	return &ProjectContextTool{schema: schema, store: store, root: root}, nil
}

func (tool *ProjectContextTool) Schema() valueobject.ToolSchema {
	return tool.schema
}

// Execute renders one section per bound directory: the real dirs come
// from the turn's context bindings, never the call input — input only
// mirrors the workspace pin so the hydration record dedups alike.
func (tool *ProjectContextTool) Execute(ctx context.Context, _ entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return dservice.ToolResult{}, err
	}
	dir := dservice.WorkspaceDir(ctx)
	if dir == "" {
		dir = tool.root
	}
	var b strings.Builder
	tool.renderDir(ctx, &b, "project context for ", dir)
	for _, extra := range dservice.ExtraFolders(ctx) {
		if extra == "" || extra == dir {
			continue
		}
		b.WriteString("\n\n")
		tool.renderDir(ctx, &b, "extra folder ", extra)
	}
	return dservice.ToolResult{Text: b.String()}, nil
}

// readAgents returns the workspace root's AGENTS.md text; any failure —
// unreadable dir or missing file — degrades to the "no AGENTS.md" line
// rather than failing the record.
func (tool *ProjectContextTool) readAgents(ctx context.Context, dir string) (string, error) {
	sess, err := tool.store.Open(ctx, dir)
	if err != nil {
		return "", err
	}
	defer sess.Close()
	body, err := sess.ReadFile("AGENTS.md")
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// renderDir writes one folder's section: the header, its AGENTS.md
// excerpt, and the bounded layout map.
func (tool *ProjectContextTool) renderDir(ctx context.Context, b *strings.Builder, header, dir string) {
	b.WriteString(header + dir)
	b.WriteString("\n\nAGENTS.md:\n")
	body, err := tool.readAgents(ctx, dir)
	if err != nil {
		b.WriteString("no AGENTS.md in this workspace")
	} else {
		b.WriteString(truncate(body, agentsCap))
	}
	b.WriteString("\n\nlayout:\n")
	b.WriteString(tool.renderMap(ctx, dir))
}

// renderMap walks dir once — Entries is lexical and honors the tree's
// ignores — collecting a file count per directory plus the children it
// lists: root entries and one level inside each root directory. Deeper
// levels contribute only to the counts, which is the point of the map.
func (tool *ProjectContextTool) renderMap(ctx context.Context, dir string) string {
	counts := map[string]int{}        // dir prefix -> total files beneath it
	children := map[string][]string{} // "" -> root entries, "<dir>" -> its level-1 entries
	if err := tool.store.Entries(ctx, dir, func(rel string, isDir bool) error {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if isDir && noiseDirs[parts[len(parts)-1]] {
			return fs.SkipDir
		}
		// Only the displayed depths record children; a file still counts
		// toward every ancestor directory the map shows.
		if len(parts) <= 2 {
			parent := strings.Join(parts[:len(parts)-1], "/")
			name := parts[len(parts)-1]
			if isDir {
				name += "/"
			}
			children[parent] = append(children[parent], name)
		}
		if isDir {
			return nil
		}
		for i := 0; i < len(parts)-1 && i < 2; i++ {
			counts[path.Join(parts[:i+1]...)]++
		}
		return nil
	}); err != nil {
		return "(unreadable: " + err.Error() + ")"
	}
	var b strings.Builder
	lines, bytes := 0, 0
	line := func(s string) bool {
		if lines+1 > mapLines || bytes+len(s)+1 > mapBytes {
			return false
		}
		b.WriteString(s + "\n")
		lines++
		bytes += len(s) + 1
		return true
	}
	full := true
	for _, name := range children[""] {
		if !full {
			break
		}
		if !strings.HasSuffix(name, "/") {
			full = line("- " + name)
			continue
		}
		dirName := name[:len(name)-1]
		full = line(fmt.Sprintf("- %s — %s", name, plural(counts[dirName], "file")))
		for _, sub := range children[dirName] {
			if !full {
				break
			}
			if strings.HasSuffix(sub, "/") {
				full = line(fmt.Sprintf("  - %s — %s", sub, plural(counts[dirName+"/"+sub[:len(sub)-1]], "file")))
				continue
			}
			full = line("  - " + sub)
		}
	}
	if !full {
		b.WriteString("… (truncated)")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// plural renders "1 file" / "N files".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// truncate cuts s at cap bytes on a rune boundary and marks the cut.
func truncate(s string, cap int) string {
	if len(s) <= cap {
		return s
	}
	for !utf8.ValidString(s[:cap]) {
		cap--
	}
	return s[:cap] + "\n… (truncated)"
}
