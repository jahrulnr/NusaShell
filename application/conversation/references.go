package appconversation

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Place names where the owner is composing: an existing room, or a draft
// that will open in a project or a workspace ("" for none).
type Place struct {
	ConversationID string
	ProjectID      string
	Workspace      string
}

// Roots resolves the folders a turn at the place works in, primary first,
// and the workspace storage that reaches them — the same resolution a
// turn makes, so what the owner can pick is what the turn can open.
func (app *ConversationApp) Roots(ctx context.Context, p Place) (drepo.Workspace, []string, error) {
	deps := app.depsSnapshot()
	env := enum.EnvironmentLocal
	var roots []string
	projectID := p.ProjectID
	if p.ConversationID != "" {
		conv, err := deps.Convs.Load(ctx, p.ConversationID)
		if err != nil {
			return nil, nil, err
		}
		env, projectID = conv.Environment(), conv.ProjectID()
		driver, ok := deps.Environments[env]
		if !ok || driver == nil {
			return nil, nil, apperror.New(400, "conversation environment unavailable", nil)
		}
		if projectID == "" {
			if dir := appjob.TurnWorkspace(conv, driver); dir != "" {
				roots = []string{dir}
			}
		}
	} else if dir := strings.TrimSpace(p.Workspace); dir != "" && projectID == "" {
		if !filepath.IsAbs(dir) {
			return nil, nil, apperror.New(400, "workspace must be an absolute path", nil)
		}
		roots = []string{filepath.Clean(dir)}
	}
	driver, ok := deps.Environments[env]
	if !ok || driver == nil {
		return nil, nil, apperror.New(400, "conversation environment unavailable", nil)
	}
	if projectID != "" && deps.Projects != nil {
		proj, err := deps.Projects.Get(ctx, projectID)
		if err != nil && apperror.As(err).StatusCode() != apperror.StatusNotFound {
			return nil, nil, err
		}
		if err == nil {
			roots = proj.Folders()
		}
	}
	if len(roots) == 0 {
		if dir := driver.FallbackRoot(); dir != "" {
			roots = []string{dir}
		}
	}
	return driver.Workspace(), roots, nil
}

// ReferenceCandidate is something the owner can select in a message.
type ReferenceCandidate struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

const (
	referencesPerKind = 25
	referencesTotal   = 100
	referenceWalk     = 50000
)

// UseReferenceSources sets where skills and MCP server names come from.
func (app *ConversationApp) UseReferenceSources(skills drepo.Skills, mcpNames func(context.Context) []string) {
	app.mu.Lock()
	app.skills, app.mcpNames = skills, mcpNames
	app.mu.Unlock()
}

// ReferenceCandidates searches the files and folders under the place's
// roots, the skills its workspace sees, and the MCP servers, for names
// containing query. Results favour names that start with it, capped per
// kind and in total; truncated reports that some were left out.
func (app *ConversationApp) ReferenceCandidates(ctx context.Context, p Place, query string) ([]ReferenceCandidate, bool, error) {
	ws, roots, err := app.Roots(ctx, p)
	if err != nil {
		return nil, false, err
	}
	app.mu.Lock()
	skills, mcpNames := app.skills, app.mcpNames
	app.mu.Unlock()

	q := strings.ToLower(query)
	var items []ReferenceCandidate
	seen := map[string]bool{}
	add := func(c ReferenceCandidate) {
		key := c.Kind + "\x00" + c.ID
		if !seen[key] && strings.Contains(strings.ToLower(c.Name+" "+c.Detail), q) {
			seen[key] = true
			items = append(items, c)
		}
	}
	if mcpNames != nil {
		for _, name := range mcpNames(ctx) {
			add(ReferenceCandidate{Kind: enum.ReferenceMCP.String(), ID: name, Name: name, Detail: "MCP server"})
		}
	}
	if skills != nil && len(roots) > 0 {
		catalog, err := skills.List(ctx, roots[0])
		if err != nil {
			return nil, false, err
		}
		for _, s := range catalog.Skills {
			add(ReferenceCandidate{Kind: enum.ReferenceSkill.String(), ID: s.Path, Name: s.Name, Detail: s.Description})
		}
	}
	truncated := false
	visited := 0
	for _, root := range roots {
		err := ws.Entries(ctx, root, func(rel string, isDir bool) error {
			visited++
			if visited > referenceWalk {
				truncated = true
				return fs.SkipAll
			}
			kind := enum.ReferenceFile
			if isDir {
				kind = enum.ReferenceFolder
			}
			add(ReferenceCandidate{Kind: kind.String(), ID: filepath.Join(root, rel), Name: filepath.Base(rel), Detail: filepath.ToSlash(rel)})
			return nil
		})
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		if err != nil {
			truncated = true
		}
		if visited > referenceWalk {
			break
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if ai, bi := strings.HasPrefix(a, q), strings.HasPrefix(b, q); ai != bi {
			return ai
		}
		if a != b {
			return a < b
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID < items[j].ID
	})
	counts := map[string]int{}
	out := make([]ReferenceCandidate, 0, min(len(items), referencesTotal))
	for _, it := range items {
		if counts[it.Kind] >= referencesPerKind || len(out) >= referencesTotal {
			truncated = true
			continue
		}
		counts[it.Kind]++
		out = append(out, it)
	}
	return out, truncated, nil
}
