package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture lays out a minimal repository tree in a temp dir:
// resources/agent/docs/ui-source/ui-map.json, frontend/index.html and
// frontend/js/*.js. It returns the repo root and the docs output dir.
func writeFixture(t *testing.T, uiMapJSON, indexHTML string, js map[string]string) (root, docs string) {
	t.Helper()
	root = t.TempDir()
	docs = filepath.Join(root, "resources", "agent", "docs")
	if err := os.MkdirAll(filepath.Join(docs, "ui-source"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "ui-source", "ui-map.json"), []byte(uiMapJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	jsDir := filepath.Join(root, "frontend", "js")
	if err := os.MkdirAll(jsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", "index.html"), []byte(indexHTML), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, src := range js {
		if err := os.WriteFile(filepath.Join(jsDir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, docs
}

// cleanFixture is a minimal self-consistent map + HTML pair: every section
// control exists in the dict, every dict entry is referenced, and every
// mapped id exists in the HTML.
const cleanMap = `{
  "views": {
    "home": {
      "title": "Home",
      "sections": [
        {"heading": "App grid", "controls": ["app-grid", "app-reload-btn"]}
      ]
    }
  },
  "controls": {
    "app-grid": {"label": "App grid", "section": "Home", "type": "grid", "notes": "Tiles."},
    "app-reload-btn": {"label": "Reload", "section": "Home", "type": "button", "action": "Reloads."}
  }
}`

const cleanHTML = `<html><body>
<section class="view home-view" data-view="home">
  <div id="app-grid"></div>
  <button type="button" id="app-reload-btn"></button>
</section>
</body></html>`

func runQuiet(root, docs string, check bool) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(root, docs, check, &out, &errb)
	return code, out.String(), errb.String()
}

func TestCheckPassesOnCleanFixture(t *testing.T) {
	root, docs := writeFixture(t, cleanMap, cleanHTML, nil)
	if code, _, e := runQuiet(root, docs, false); code != 0 {
		t.Fatalf("generate failed: %s", e)
	}
	if code, _, e := runQuiet(root, docs, true); code != 0 {
		t.Fatalf("check failed on freshly generated docs: %s", e)
	}
}

func TestCheckFailsOnMissingSectionControlRef(t *testing.T) {
	bad := strings.Replace(cleanMap,
		`"controls": ["app-grid", "app-reload-btn"]`,
		`"controls": ["app-grid", "ghost-btn"]`, 1)
	root, docs := writeFixture(t, bad, cleanHTML, nil)

	// Generation must succeed, keep the stub, and warn on stderr.
	code, _, genErr := runQuiet(root, docs, false)
	if code != 0 {
		t.Fatalf("generation should not fail on missing refs, got: %s", genErr)
	}
	rendered, err := os.ReadFile(filepath.Join(docs, "ui-home.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "`#ghost-btn`** (missing map entry)") {
		t.Fatalf("expected stub for missing ref, got:\n%s", rendered)
	}
	if !strings.Contains(genErr, "ghost-btn") {
		t.Fatalf("expected stderr warning listing ghost-btn, got: %s", genErr)
	}

	// -check must fail and name the missing id.
	code, _, checkErr := runQuiet(root, docs, true)
	if code == 0 {
		t.Fatal("check should fail when a section references a missing control id")
	}
	if !strings.Contains(checkErr, "ghost-btn") {
		t.Fatalf("check error should name ghost-btn, got: %s", checkErr)
	}
}

func TestCheckFailsOnOrphanedControl(t *testing.T) {
	bad := strings.Replace(cleanMap,
		`"app-reload-btn": {"label": "Reload", "section": "Home", "type": "button", "action": "Reloads."}`,
		`"app-reload-btn": {"label": "Reload", "section": "Home", "type": "button", "action": "Reloads."},
    "unused-btn": {"label": "Unused", "section": "Home", "type": "button", "action": "Does nothing."}`, 1)
	root, docs := writeFixture(t, bad, cleanHTML+`<button type="button" id="unused-btn"></button>`, nil)

	if code, _, e := runQuiet(root, docs, false); code != 0 {
		t.Fatalf("generation failed: %s", e)
	}
	code, _, e := runQuiet(root, docs, true)
	if code == 0 {
		t.Fatal("check should fail on an orphaned controls entry")
	}
	if !strings.Contains(e, "unused-btn") {
		t.Fatalf("check error should name unused-btn, got: %s", e)
	}
}

func TestCoverageWarningIsAdvisoryOnly(t *testing.T) {
	// One interactive unmapped id inside the view, one nested inside a plain
	// inner section, one structural unmapped id, and one interactive id
	// outside every data-view section.
	html := `<html><body>
<section class="view home-view" data-view="home">
  <div id="app-grid"></div>
  <button type="button" id="app-reload-btn"></button>
  <button type="button" id="unmapped-btn"></button>
  <section class="inner"><button type="button" id="nested-btn"></button></section>
  <div id="structural-div"></div>
  <h2 id="structural-heading"></h2>
</section>
<button type="button" id="outside-btn"></button>
</body></html>`
	root, docs := writeFixture(t, cleanMap, html, nil)

	// Generation mode warns but succeeds.
	code, _, e := runQuiet(root, docs, false)
	if code != 0 {
		t.Fatalf("generation failed: %s", e)
	}
	if !strings.Contains(e, "coverage warning: 2 unmapped element ids in home") {
		t.Fatalf("expected coverage warning for home, got: %s", e)
	}
	for _, id := range []string{"unmapped-btn", "nested-btn"} {
		if !strings.Contains(e, id) {
			t.Fatalf("coverage warning should list %s, got: %s", id, e)
		}
	}
	for _, id := range []string{"structural-div", "structural-heading", "outside-btn", "app-grid", "app-reload-btn"} {
		if strings.Contains(e, id) {
			t.Fatalf("coverage warning should not list %s, got: %s", id, e)
		}
	}

	// -check never fails on coverage warnings.
	if code, _, e := runQuiet(root, docs, true); code != 0 {
		t.Fatalf("check should pass despite coverage warning, got: %s", e)
	}
}

func TestRenderViewTrimsOutput(t *testing.T) {
	view := uiView{
		Title: "Agent",
		Sections: []uiSection{{
			Heading:  "Composer",
			Controls: []string{"agent-shell", "composer-input", "status-text", "side-panel", "ghost"},
		}},
	}
	controls := map[string]uiControl{
		// Structural, no action/notes -> bullet skipped entirely.
		"agent-shell": {Label: "Agent shell", Section: "Agent", Type: "container"},
		// Section equals the view title -> Section line dropped.
		"composer-input": {Label: "Composer", Section: "Agent", Type: "textarea", Action: "Sends the message."},
		// Section equals the section heading -> Section line dropped.
		// Non-structural type with no action/notes -> still renders.
		"status-text": {Label: "Status", Section: "Composer", Type: "text"},
		// Distinct section value -> Section line kept; notes keep a
		// structural type visible.
		"side-panel": {Label: "Side", Section: "Side region", Type: "panel", Notes: "Aux."},
	}
	out := renderView("agent", view, controls)

	if strings.Contains(out, "Agent shell") {
		t.Fatalf("structural control without action/notes should be skipped:\n%s", out)
	}
	if strings.Contains(out, "Section: Agent") || strings.Contains(out, "Section: Composer") {
		t.Fatalf("Section lines redundant with ancestor headings should be dropped:\n%s", out)
	}
	if !strings.Contains(out, "Section: Side region") {
		t.Fatalf("distinct Section value should be kept:\n%s", out)
	}
	if !strings.Contains(out, "#status-text") {
		t.Fatalf("non-structural control without action/notes should render:\n%s", out)
	}
	if !strings.Contains(out, "#side-panel") {
		t.Fatalf("structural control with notes should render:\n%s", out)
	}
	if !strings.Contains(out, "`#ghost`** (missing map entry)") {
		t.Fatalf("missing refs should still render stubs:\n%s", out)
	}
}
