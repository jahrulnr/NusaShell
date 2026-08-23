// Command scan-ui-docs generates the agent-usable UI documentation corpus
// from resources/agent/docs/ui-source/ui-map.json and validates it
// against the frontend source (frontend/index.html + frontend/js/**/*.js).
//
// The generated files resources/agent/docs/ui-*.md are embedded at
// build time by resources/resources.go so the agent can surface them
// through the `docs` family tool (root + op).
//
// Usage:
//
//	go run ./cmd/scan-ui-docs            # regenerate ui-*.md
//	go run ./cmd/scan-ui-docs -check     # fail if committed ui-*.md are stale
//	go run ./cmd/scan-ui-docs -repo-root /path/to/repo
//
// Validation fails when:
//   - a <section data-view="..."> in the HTML has no entry in ui-map.json
//   - a ui-map.json view (without chrome:true) has no matching data-view
//   - a mapped control ID is not found in the frontend source (HTML or JS)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type uiMap struct {
	Views    map[string]uiView    `json:"views"`
	Controls map[string]uiControl `json:"controls"`
}

type uiView struct {
	Title     string      `json:"title"`
	Purpose   string      `json:"purpose"`
	HowToOpen string      `json:"howToOpen"`
	Notes     string      `json:"notes"`
	Chrome    bool        `json:"chrome"`
	Sections  []uiSection `json:"sections"`
}

type uiSection struct {
	Heading    string   `json:"heading"`
	Paragraphs []string `json:"paragraphs"`
	Controls   []string `json:"controls"`
}

type uiControl struct {
	Label     string   `json:"label"`
	Section   string   `json:"section"`
	Type      string   `json:"type"`
	Action    string   `json:"action"`
	Shortcut  string   `json:"shortcut"`
	Notes     string   `json:"notes"`
	Related   []string `json:"related"`
	Selector  string   `json:"selector"`
	Generated bool     `json:"generated"`
}

var (
	viewRegex   = regexp.MustCompile(`<section[^>]*\bclass="[^"]*view[^"]*"[^>]*\bdata-view="([^"]+)"`)
	htmlIDRegex = regexp.MustCompile(`\bid="([^"]+)"`)
	// Matches $("#id"), document.getElementById("id"),
	// document.querySelector("#id"), document.querySelectorAll("#id").
	jsIDRegex = regexp.MustCompile(`(?:\$|querySelector(?:All)?)\s*\(\s*["']#([^"']+)["']\s*\)|getElementById\s*\(\s*["']([^"']+)["']\s*\)`)
)

func main() {
	repoRoot := flag.String("repo-root", ".", "repository root (defaults to cwd)")
	check := flag.Bool("check", false, "fail if committed ui-*.md differ from generated content (drift gate)")
	outDir := flag.String("out-dir", "", "output directory for ui-*.md (defaults to <repo-root>/resources/agent/docs)")
	flag.Parse()

	root, err := filepath.Abs(*repoRoot)
	if err != nil {
		fail("resolve repo root: %v", err)
	}
	uiMapPath := filepath.Join(root, "resources", "agent", "docs", "ui-source", "ui-map.json")
	htmlPath := filepath.Join(root, "frontend", "index.html")
	jsDir := filepath.Join(root, "frontend", "js")
	dest := *outDir
	if dest == "" {
		dest = filepath.Join(root, "resources", "agent", "docs")
	}

	m, err := loadMap(uiMapPath)
	if err != nil {
		fail("load ui-map: %v", err)
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		fail("read html: %v", err)
	}
	jsFiles, err := walkJS(jsDir)
	if err != nil {
		fail("walk js: %v", err)
	}
	jsSources := make([]string, 0, len(jsFiles))
	for _, f := range jsFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		jsSources = append(jsSources, string(b))
	}

	htmlViews := collectViews(string(html))
	htmlIDs := collectHTMLIDs(string(html))
	jsIDs := collectJSIDs(jsSources)
	sourceIDs := union(htmlIDs, jsIDs)

	var errors []string

	for v := range htmlViews {
		if _, ok := m.Views[v]; !ok {
			errors = append(errors, fmt.Sprintf(`HTML has data-view=%q but ui-map.json has no view entry for it.`, v))
		}
	}
	for v, view := range m.Views {
		if view.Chrome {
			continue
		}
		if !htmlViews[v] {
			errors = append(errors, fmt.Sprintf(`ui-map.json has view %q but HTML has no data-view=%q section.`, v, v))
		}
	}
	for id, c := range m.Controls {
		if c.Generated {
			continue
		}
		if !sourceIDs[id] {
			errors = append(errors, fmt.Sprintf(`ui-map.json control %q is not found in the frontend source.`, id))
		}
	}

	// Render and either write or compare each view.
	viewIDs := sortedKeys(m.Views)
	generated := 0
	for _, id := range viewIDs {
		content := renderView(id, m.Views[id], m.Controls)
		target := filepath.Join(dest, "ui-"+id+".md")
		if *check {
			existing, err := os.ReadFile(target)
			if err != nil {
				errors = append(errors, fmt.Sprintf("check: %s: %v", target, err))
				continue
			}
			if string(existing) != content {
				errors = append(errors, fmt.Sprintf("check: %s is stale (run `make scan-ui-docs` to regenerate)", target))
			}
			generated++
		} else {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				fail("mkdir %s: %v", dest, err)
			}
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				fail("write %s: %v", target, err)
			}
			generated++
		}
	}

	if len(errors) > 0 {
		fmt.Fprintln(os.Stderr, "UI docs validation failed:")
		for _, e := range errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		os.Exit(1)
	}

	mode := "generated"
	if *check {
		mode = "checked"
	}
	fmt.Printf("UI docs %s: %d views (scanned %d JS files in %s)\n", mode, generated, len(jsFiles), filepath.Join("frontend", "js"))
}

func loadMap(path string) (*uiMap, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m uiMap
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

func walkJS(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".js") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func collectViews(html string) map[string]bool {
	views := map[string]bool{}
	for _, m := range viewRegex.FindAllStringSubmatch(html, -1) {
		if len(m) > 1 {
			views[m[1]] = true
		}
	}
	return views
}

func collectHTMLIDs(html string) map[string]bool {
	ids := map[string]bool{}
	for _, m := range htmlIDRegex.FindAllStringSubmatch(html, -1) {
		if len(m) > 1 {
			ids[m[1]] = true
		}
	}
	return ids
}

func collectJSIDs(sources []string) map[string]bool {
	ids := map[string]bool{}
	for _, src := range sources {
		for _, m := range jsIDRegex.FindAllStringSubmatch(src, -1) {
			for _, g := range m[1:] {
				if g != "" {
					ids[g] = true
				}
			}
		}
	}
	return ids
}

func union(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func controlRef(id string, c uiControl) string {
	if c.Selector != "" {
		return "`" + c.Selector + "`"
	}
	return "`#" + id + "`"
}

func renderControl(id string, c uiControl, all map[string]uiControl) string {
	label := c.Label
	if label == "" {
		label = id
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("- **%s** (%s):", label, controlRef(id, c)))
	if c.Section != "" {
		lines = append(lines, "  - Section: "+c.Section)
	}
	if c.Type != "" {
		lines = append(lines, "  - Type: "+c.Type)
	}
	if c.Action != "" {
		lines = append(lines, "  - Action: "+c.Action)
	}
	if c.Shortcut != "" {
		lines = append(lines, "  - Shortcut: "+c.Shortcut)
	}
	if len(c.Related) > 0 {
		var parts []string
		for _, rid := range c.Related {
			r, ok := all[rid]
			rl := rid
			if ok && r.Label != "" {
				rl = r.Label
			}
			parts = append(parts, fmt.Sprintf("%s (%s)", rl, controlRef(rid, r)))
		}
		lines = append(lines, "  - Related: "+strings.Join(parts, ", "))
	}
	if c.Notes != "" {
		lines = append(lines, "  - Notes: "+c.Notes)
	}
	return strings.Join(lines, "\n")
}

func renderView(id string, v uiView, controls map[string]uiControl) string {
	title := v.Title
	if title == "" {
		title = id
	}
	var lines []string
	lines = append(lines, "# "+title, "")
	if v.Purpose != "" {
		lines = append(lines, v.Purpose, "")
	}
	if v.HowToOpen != "" {
		lines = append(lines, "**How to open:** "+v.HowToOpen, "")
	}
	if v.Notes != "" {
		lines = append(lines, v.Notes, "")
	}
	for _, s := range v.Sections {
		lines = append(lines, "## "+s.Heading, "")
		for _, p := range s.Paragraphs {
			lines = append(lines, p, "")
		}
		for _, cid := range s.Controls {
			c, ok := controls[cid]
			if !ok {
				lines = append(lines, fmt.Sprintf("- **`#%s`** (missing map entry)", cid), "")
				continue
			}
			lines = append(lines, renderControl(cid, c, controls), "")
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "scan-ui-docs: "+format+"\n", args...)
	os.Exit(1)
}
