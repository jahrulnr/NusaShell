package skill

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	reposkill "github.com/jahrulnr/nusashell/infrastructure/repository/skill"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

// The tests go through the real wiring: a real catalog over real files,
// real stores over the real migration, and the wire both ways — the
// digest a move sends back is read from the page's own response, never
// handed to the test by a shortcut.

func writeSkillFile(t *testing.T, root, name, description, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return content
}

// oneWorkspace is the explicit answer a test gives when it means a single
// workspace: no other folder holds a skill. Register refuses a nil source, so
// the answer is written out instead of left out.
type oneWorkspace struct{}

func (oneWorkspace) Folders(context.Context) ([]string, error) { return nil, nil }

func skillsRouter(t *testing.T, home, nusashell, builtin string) (*gin.Engine, *sql.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "n.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := gin.New()
	catalog := reposkill.NewLocal(home, nusashell, builtin)
	Register(r.Group("/api"), Deps{
		Catalog:   catalog,
		Ownership: reposkill.NewOwnership(db),
		Proposals: reposkill.NewProposals(db),
		Decisions: reposkill.NewDecisions(db),
		Writer:    catalog,
		Budget:    32768,
		Folders:   oneWorkspace{},
	})
	return r, db
}

func get(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func post(t *testing.T, r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func rowsOf(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("the page answered %d: %s", rec.Code, rec.Body)
	}
	var doc struct {
		Data        []map[string]any `json:"data"`
		Diagnostics []string         `json:"diagnostics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the page is not JSON: %v — %s", err, rec.Body)
	}
	if doc.Diagnostics == nil {
		t.Error("diagnostics must be an array, not null — the page renders it either way")
	}
	return doc.Data
}

func rowByName(t *testing.T, rows []map[string]any, name string) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row["name"] == name {
			return row
		}
	}
	t.Fatalf("the page has no row for %q: %v", name, rows)
	return nil
}

func TestSkillsPageJoinsTheCatalogEndToEnd(t *testing.T) {
	home := t.TempDir()
	nusashell := t.TempDir()
	builtin := t.TempDir()
	wsBase := t.TempDir()
	r, db := skillsRouter(t, home, nusashell, builtin)
	release := writeSkillFile(t, nusashell, "release-notes", "Use when writing release-notes", "## Steps\n1. read the log")
	writeSkillFile(t, nusashell, "deploy-day", "Use when deploying", "## Steps\n1. check the calendar")
	writeSkillFile(t, builtin, "coding-workflow", "Use when changing code", "## Steps\n1. read the guide")
	writeSkillFile(t, filepath.Join(wsBase, ".agents", "skills"), "ws-skill", "Use when in this workspace", "## Steps\n1. ask the room")

	// The row and the pending proposal the page joins: a learned skill at
	// agent level with one proposal waiting on the owner.
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, valueobject.SkillDigest(release))
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewOwnership(db).Save(context.Background(), row, ""); err != nil {
		t.Fatal(err)
	}
	p, err := aggregate.NewSkillProposal("sprop_page", aggregate.SkillProposalInput{
		Op: enum.SkillProposalCreate, Kind: enum.SkillKindMethod,
		Name: "release-notes", Description: "Use when writing release-notes",
		Body: "## Steps\n1. read the log\n2. group by area", Evidence: "write release notes like the last one",
		EvidenceKind: enum.SkillEvidenceQuote,
	}, time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewProposals(db).Add(context.Background(), p); err != nil {
		t.Fatal(err)
	}

	rows := rowsOf(t, get(t, r, "/api/skills"))
	if len(rows) != 3 {
		t.Fatalf("the page lists %d rows without a workspace; want the 3 other roots", len(rows))
	}
	releaseRow := rowByName(t, rows, "release-notes")
	if releaseRow["level"] != "agent" || releaseRow["pending"] != float64(1) {
		t.Errorf("the join missed the row or the proposal: %v", releaseRow)
	}
	if releaseRow["digest"] != valueobject.SkillDigest(release) {
		t.Errorf("the digest is not the whole file's: %v", releaseRow["digest"])
	}
	if releaseRow["source"] != "nusashell" || releaseRow["path"] != filepath.Join(nusashell, "release-notes", "SKILL.md") {
		t.Errorf("the catalog fields did not pass through: %v", releaseRow)
	}
	if got := rowByName(t, rows, "deploy-day"); got["level"] != "user" || got["pending"] != float64(0) {
		t.Errorf("a learned skill with no row must read user with no pending: %v", got)
	}
	if got := rowByName(t, rows, "coding-workflow"); got["level"] != "user" {
		t.Errorf("a builtin has no level but the owner's: %v", got)
	}

	rows = rowsOf(t, get(t, r, "/api/skills?workspace="+wsBase))
	if len(rows) != 4 {
		t.Fatalf("the page lists %d rows with a workspace; want 4", len(rows))
	}
	if got := rowByName(t, rows, "ws-skill"); got["source"] != "workspace" || got["level"] != "user" {
		t.Errorf("the workspace entry did not join as the owner's: %v", got)
	}
}

func TestMoveLevelTakeBackAndRegrantEndToEnd(t *testing.T) {
	home := t.TempDir()
	nusashell := t.TempDir()
	r, db := skillsRouter(t, home, nusashell, t.TempDir())
	release := writeSkillFile(t, nusashell, "release-notes", "Use when writing release-notes", "## Steps\n1. read the log")
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, valueobject.SkillDigest(release))
	if err != nil {
		t.Fatal(err)
	}
	ownership := reposkill.NewOwnership(db)
	if err := ownership.Save(context.Background(), row, ""); err != nil {
		t.Fatal(err)
	}

	// Take the skill back: the digest comes from the page's own response.
	digest := rowByName(t, rowsOf(t, get(t, r, "/api/skills")), "release-notes")["digest"].(string)
	rec := post(t, r, "/api/skills/release-notes/level", `{"level":"user","digest":"`+digest+`"}`)
	if rec.Code != 204 {
		t.Fatalf("the take-back answered %d: %s", rec.Code, rec.Body)
	}
	if got := rowByName(t, rowsOf(t, get(t, r, "/api/skills")), "release-notes"); got["level"] != "user" {
		t.Fatalf("the page still shows the old level: %v", got)
	}
	stored, err := ownership.Get(context.Background(), enum.SkillRootNusashell, "release-notes")
	if err != nil || stored.Level() != enum.SkillLevelUser {
		t.Fatalf("the take-back did not persist: %v %v", stored, err)
	}
	if stored.Digest() != valueobject.SkillDigest(release) {
		t.Errorf("a take-back must keep the digest as history: %q", stored.Digest())
	}

	// A hand edit moves the file on disk; handing the skill back must work
	// against the bytes the owner now sees, not the row's old digest.
	hand := writeSkillFile(t, nusashell, "release-notes", "Use when writing release-notes", "## Steps\n1. read the log twice")
	if valueobject.SkillDigest(hand) == valueobject.SkillDigest(release) {
		t.Fatal("the hand edit did not change the file")
	}
	digest = rowByName(t, rowsOf(t, get(t, r, "/api/skills")), "release-notes")["digest"].(string)
	if digest != valueobject.SkillDigest(hand) {
		t.Fatalf("the page shows a digest the disk does not have: %q", digest)
	}
	rec = post(t, r, "/api/skills/release-notes/level", `{"level":"agent","digest":"`+digest+`"}`)
	if rec.Code != 204 {
		t.Fatalf("the re-grant answered %d: %s", rec.Code, rec.Body)
	}
	stored, err = ownership.Get(context.Background(), enum.SkillRootNusashell, "release-notes")
	if err != nil || stored.Level() != enum.SkillLevelAgent {
		t.Fatalf("the re-grant did not persist: %v %v", stored, err)
	}
	if stored.Digest() != valueobject.SkillDigest(hand) {
		t.Errorf("a re-grant must move the digest to the bytes the owner saw: %q", stored.Digest())
	}
}

func TestMoveLevelRefusesAStalePageInOwnerWords(t *testing.T) {
	home := t.TempDir()
	nusashell := t.TempDir()
	r, db := skillsRouter(t, home, nusashell, t.TempDir())
	release := writeSkillFile(t, nusashell, "release-notes", "Use when writing release-notes", "## Steps\n1. read the log")
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, valueobject.SkillDigest(release))
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewOwnership(db).Save(context.Background(), row, ""); err != nil {
		t.Fatal(err)
	}
	// The page loaded, the file moved, the owner decides on the old bytes.
	writeSkillFile(t, nusashell, "release-notes", "Use when writing release-notes", "## Steps\n1. read the log and the tags")
	rec := post(t, r, "/api/skills/release-notes/level", `{"level":"user","digest":"`+valueobject.SkillDigest(release)+`"}`)
	if rec.Code != 409 {
		t.Fatalf("a stale page answered %d: %s", rec.Code, rec.Body)
	}
	var problem map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"changed since this page was opened", "reload the page"} {
		if detail, _ := problem["detail"].(string); !strings.Contains(detail, want) {
			t.Errorf("the refusal must tell the owner %q, got %q", want, detail)
		}
	}
	stored, err := reposkill.NewOwnership(db).Get(context.Background(), enum.SkillRootNusashell, "release-notes")
	if err != nil || stored.Level() != enum.SkillLevelAgent {
		t.Fatalf("a refused move changed the row: %v %v", stored, err)
	}
}

func TestMoveLevelRefusesWhatItCannotMove(t *testing.T) {
	home := t.TempDir()
	nusashell := t.TempDir()
	builtin := t.TempDir()
	wsBase := t.TempDir()
	r, db := skillsRouter(t, home, nusashell, builtin)
	writeSkillFile(t, nusashell, "release-notes", "Use when writing release-notes", "## Steps\n1. read the log")
	writeSkillFile(t, builtin, "coding-workflow", "Use when changing code", "## Steps\n1. read the guide")
	writeSkillFile(t, filepath.Join(wsBase, ".agents", "skills"), "ws-skill", "Use when in this workspace", "## Steps\n1. ask the room")
	row, err := aggregate.NewSkillOwnership(enum.SkillRootNusashell, "release-notes", enum.SkillLevelAgent, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if err := reposkill.NewOwnership(db).Save(context.Background(), row, ""); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		body string
		code int
	}{
		{"a builtin", "/api/skills/coding-workflow/level", `{"level":"user","digest":"d1"}`, 400},
		{"a workspace skill", "/api/skills/ws-skill/level?workspace=" + wsBase, `{"level":"user","digest":"d1"}`, 400},
		{"a name that is not live", "/api/skills/ghost-skill/level", `{"level":"user","digest":"d1"}`, 404},
		{"a level outside user and agent", "/api/skills/release-notes/level", `{"level":"owner","digest":"d1"}`, 400},
		{"a move without a digest", "/api/skills/release-notes/level", `{"level":"user"}`, 400},
		{"a body that is not one JSON document", "/api/skills/release-notes/level", `{"level":"user"} trailing`, 400},
	}
	for _, c := range cases {
		rec := post(t, r, c.path, c.body)
		if rec.Code != c.code {
			t.Errorf("%s: answered %d, want %d — %s", c.name, rec.Code, c.code, rec.Body)
		}
	}
	// The write-body rules are the shared decode contract: a request that
	// is not application/json never reaches the use case.
	raw := httptest.NewRequest(http.MethodPost, "/api/skills/release-notes/level", strings.NewReader(`{"level":"user","digest":"d1"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, raw)
	if rec.Code != 400 {
		t.Errorf("a request without the content type answered %d, want the decode rule's 400 — %s", rec.Code, rec.Body)
	}
}

func TestRegisterNeedsEveryPort(t *testing.T) {
	// A boundary that is half-wired registers nothing: a hand-built
	// handler without the feature must not answer skill routes at all.
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "n.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := gin.New()
	Register(r.Group("/api"), Deps{Catalog: reposkill.NewLocal(t.TempDir(), t.TempDir(), t.TempDir())})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if w.Code != 404 {
		t.Errorf("a half-wired Deps answered %d; want no route", w.Code)
	}
	// Every port but the folder source: the page would judge names by one
	// workspace without saying so, so it registers nothing either.
	r = gin.New()
	Register(r.Group("/api"), Deps{
		Catalog:   reposkill.NewLocal(t.TempDir(), t.TempDir(), t.TempDir()),
		Ownership: reposkill.NewOwnership(db),
		Proposals: reposkill.NewProposals(db),
	})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if w.Code != 404 {
		t.Errorf("a Deps without its folder source answered %d; want no route", w.Code)
	}
	// Decisions and Writer are what an accept writes through. Without either
	// neither the page nor the queue is mounted.
	catalog := reposkill.NewLocal(t.TempDir(), t.TempDir(), t.TempDir())
	whole := Deps{
		Catalog:   catalog,
		Ownership: reposkill.NewOwnership(db),
		Proposals: reposkill.NewProposals(db),
		Decisions: reposkill.NewDecisions(db),
		Writer:    catalog,
		Folders:   oneWorkspace{},
	}
	noDecisions, noWriter := whole, whole
	noDecisions.Decisions = nil
	noWriter.Writer = nil
	for name, d := range map[string]Deps{"decisions": noDecisions, "writer": noWriter} {
		r := gin.New()
		Register(r.Group("/api"), d)
		for _, path := range []string{"/api/skills", "/api/skill-proposals"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != 404 {
				t.Errorf("a Deps without %s answered %d on %s; want no route", name, w.Code, path)
			}
		}
	}
}
