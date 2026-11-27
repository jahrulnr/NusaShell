package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	httpdelivery "github.com/jahrulnr/nusashell/infrastructure/delivery/http"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	reposkill "github.com/jahrulnr/nusashell/infrastructure/repository/skill"
)

func TestServePassesSkillSurfaceToNewHandler(t *testing.T) {
	raw, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"learned := eng.skillSurface(b.Config().Skills.Improvement, home)",
		"hub.OnFinished(learned.Hook)",
		"eng.conversations.UseEditEvidence(learned.Edits)",
		"Skills:             learned.Page,",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("serve.go does not contain %q", want)
		}
	}
}

func TestSkillPassSerializesOneRoomAndOverlapsTwo(t *testing.T) {
	origin := valueobject.TurnOrigin{}
	t.Run("one room waits", func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		pass := &skillPass{run: func(string, string, valueobject.TurnOrigin) {
			defer wg.Done()
			entered <- struct{}{}
			<-release
		}}
		pass.hook("room", "send", origin, false, nil)
		pass.hook("room", "send", origin, false, nil)
		<-entered
		select {
		case <-entered:
			t.Fatal("a second pass of the same room entered before the first finished")
		case <-time.After(50 * time.Millisecond):
		}
		close(release)
		<-entered
		wg.Wait()
	})
	t.Run("two rooms overlap", func(t *testing.T) {
		start := make(chan struct{})
		var in atomic.Int32
		var max atomic.Int32
		var wg sync.WaitGroup
		wg.Add(2)
		pass := &skillPass{run: func(string, string, valueobject.TurnOrigin) {
			defer wg.Done()
			<-start
			n := in.Add(1)
			for {
				old := max.Load()
				if n <= old || max.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			in.Add(-1)
		}}
		pass.hook("a", "send", origin, false, nil)
		pass.hook("b", "send", origin, false, nil)
		time.Sleep(20 * time.Millisecond)
		close(start)
		wg.Wait()
		if max.Load() != 2 {
			t.Fatalf("peak in-flight = %d, want 2: the rooms must run together", max.Load())
		}
	})
	ran := 0
	pass := &skillPass{run: func(string, string, valueobject.TurnOrigin) { ran++ }}
	pass.hook("room", "send", origin, false, context.Canceled)
	pass.hook("", "send", origin, false, nil)
	time.Sleep(20 * time.Millisecond)
	if ran != 0 {
		t.Fatalf("a failed turn or an empty room ran %d passes", ran)
	}
}

func TestSkillPassLogsAStoreFailure(t *testing.T) {
	var buf bytes.Buffer
	s := &learnedSkills{convs: &failingConversations{}, log: slog.New(slog.NewTextHandler(&buf, nil))}
	s.pass("room", "send", valueobject.TurnOrigin{})
	if !strings.Contains(buf.String(), "skill_candidate_failed") || !strings.Contains(buf.String(), "conv=room") {
		t.Fatalf("log = %q, want skill_candidate_failed for the room", buf.String())
	}
}

type namedProject struct{ p *aggregate.Project }

func (n namedProject) Get(context.Context, string) (*aggregate.Project, error) { return n.p, nil }

func TestSkillWorkspacePrefersTheProjectThenTheRoomThenTheFallback(t *testing.T) {
	conv, err := aggregate.NewConversation("c_room")
	if err != nil {
		t.Fatal(err)
	}
	conv.SetWorkspace("/rooms/one")
	if got := skillWorkspace(context.Background(), conv, nil, "/fallback"); got != "/rooms/one" {
		t.Fatalf("workspace = %q, want the room's folder", got)
	}
	project, err := aggregate.NewProject("p_one", "demo", []string{"/work/primary", "/work/extra"}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.SetProject(project.ID()); err != nil {
		t.Fatal(err)
	}
	if got := skillWorkspace(context.Background(), conv, namedProject{project}, "/fallback"); got != "/work/primary" {
		t.Fatalf("workspace = %q, want the project's primary folder", got)
	}
	conv, err = aggregate.NewConversation("c_bare")
	if err != nil {
		t.Fatal(err)
	}
	if got := skillWorkspace(context.Background(), conv, nil, "/fallback"); got != "/fallback" {
		t.Fatalf("workspace = %q, want the fallback", got)
	}
}

func TestSuggestMountsThePageAndSweepsTheRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.NewConfig()
	cfg.Storage.Dir = t.TempDir()
	cfg.Skills.Improvement = "suggest"
	cfg.Skills.CatalogMaxBytes = 4096
	skillDir := filepath.Join(cfg.Storage.Dir, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(skillDir, ".skill-orphan")
	if err := os.WriteFile(orphan, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: Use when demonstrating the learned root\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	eng, err := compose(cfg, nil, "", workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		eng.conversations.Shutdown(context.Background())
		eng.logger.Close()
	})
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("the orphan temp survived boot")
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	write := func(dir, name, desc string) {
		t.Helper()
		path := filepath.Join(dir, name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\nbody\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cfg.Storage.Dir, "builtin-skills"), "probe", "Built in probe")
	write(filepath.Join(cfg.Storage.Dir, "skills"), "probe", "Learned probe")
	ws := t.TempDir()
	write(filepath.Join(ws, ".agents", "skills"), "probe", "Workspace probe")

	catalog, err := eng.skills.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	desc := map[string]string{}
	for _, skill := range catalog.Skills {
		found[skill.Name] = skill.Source
		desc[skill.Name] = skill.Description
	}
	if found["demo"] != "nusashell" {
		t.Fatalf("demo source = %q, want nusashell; catalog %+v", found["demo"], catalog.Skills)
	}
	writer, ok := eng.skills.(drepo.SkillWriter)
	if !ok {
		t.Fatal("the catalog is not the learned-skill writer")
	}
	if _, err := writer.Write(context.Background(), enum.SkillRootNusashell, drepo.SkillDocument{
		Name: "from-the-loop", Description: "Use when the learned root is configured", Body: "## Steps\n1. write\n2. read",
	}, ""); err != nil {
		t.Fatalf("write into the learned root: %v", err)
	}
	if found["probe"] != "nusashell" || desc["probe"] != "Learned probe" {
		t.Fatalf("probe without a workspace = %q %q, want the learned root over builtin", found["probe"], desc["probe"])
	}
	catalog, err = eng.skills.List(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	found = map[string]string{}
	desc = map[string]string{}
	for _, skill := range catalog.Skills {
		found[skill.Name] = skill.Source
		desc[skill.Name] = skill.Description
	}
	if found["probe"] != "workspace" || desc["probe"] != "Workspace probe" {
		t.Fatalf("probe with a workspace = %q %q, want the workspace root last", found["probe"], desc["probe"])
	}

	if eng.skillBudget != 4096 {
		t.Fatalf("suggest budget = %d, want the configured 4096", eng.skillBudget)
	}
	surface := eng.skillSurface("suggest", workspaceRoot)
	if surface.Hook == nil || surface.Edits == nil || surface.Page.Catalog == nil || surface.Page.Ownership == nil || surface.Page.Proposals == nil || surface.Page.Folders == nil || surface.Page.Decisions == nil || surface.Page.Writer == nil || surface.Page.Budget != 4096 {
		t.Fatalf("suggest surface is missing a port: hook %v edits %v page %+v", surface.Hook != nil, surface.Edits != nil, surface.Page)
	}
	gin.SetMode(gin.TestMode)
	const authority = "127.0.0.1:8080"
	page := func(skills httpdelivery.Deps) int {
		t.Helper()
		h := httpdelivery.NewHandler(skills)
		req := httptest.NewRequest(http.MethodGet, "http://"+authority+"/api/skills", nil)
		req.Host = authority
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	deps := httpdelivery.Deps{Authorities: []string{authority}, Skills: surface.Page}
	if code := page(deps); code != 200 {
		t.Fatalf("suggest page answered %d, want 200", code)
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+authority+"/api/skills", nil)
	req.Host = authority
	rec := httptest.NewRecorder()
	httpdelivery.NewHandler(deps).ServeHTTP(rec, req)
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("page is not JSON: %v — %s", err, rec.Body)
	}
	raw, _ := json.Marshal(doc["data"])
	if !strings.Contains(string(raw), `"name":"demo"`) || !strings.Contains(string(raw), `"source":"nusashell"`) {
		t.Fatalf("page data = %s, want the learned demo skill", raw)
	}
	decide := httptest.NewRequest(http.MethodPost, "http://"+authority+"/api/skill-proposals/sprop_missing/decide", strings.NewReader(`{"action":"dismiss","reason":"no"}`))
	decide.Host = authority
	decide.Header.Set("Origin", "http://"+authority)
	decide.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	httpdelivery.NewHandler(deps).ServeHTTP(rec, decide)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "no such skill proposal") {
		t.Fatalf("decide through the constructor answered %d: %s", rec.Code, rec.Body)
	}
	off := eng.skillSurface("off", workspaceRoot)
	if code := page(httpdelivery.Deps{Authorities: []string{authority}, Skills: off.Page}); code != 404 {
		t.Fatalf("off page answered %d, want no route", code)
	}
	if code := page(httpdelivery.Deps{Authorities: []string{authority}, Skills: eng.skillSurface("observe", workspaceRoot).Page}); code != 404 {
		t.Fatalf("observe page answered %d, want no route", code)
	}
}

// The learned-skill writer is the port an accept writes through. Suggest needs
// it, and a catalog that cannot write refuses suggest at compose. Compose has no
// injection point for a read-only store, so the table tests the rule itself;
// TestSuggestMountsThePageAndSweepsTheRoot proves compose wires the writer it
// decided, because the page's Writer is non-nil only through that path.
func TestLearnedWriterRefusesACatalogThatCannotWrite(t *testing.T) {
	read := readOnlyCatalog{}
	if w, err := learnedWriter("suggest", read); err == nil || !strings.Contains(err.Error(), "cannot write") || w != nil {
		t.Fatalf("suggest with a read-only catalog: writer %v, err %v; want a refusal that names the cause", w, err)
	}
	for _, mode := range []string{"off", "observe"} {
		if w, err := learnedWriter(mode, read); err != nil || w != nil {
			t.Fatalf("%s with a read-only catalog: writer %v, err %v; want none and no error", mode, w, err)
		}
	}
	local := reposkill.NewLocal(t.TempDir(), t.TempDir(), t.TempDir())
	if w, err := learnedWriter("suggest", local); err != nil || w == nil {
		t.Fatalf("suggest with the real catalog: writer %v, err %v", w, err)
	}
}

// readOnlyCatalog reads and does not write: it embeds the read port only, so
// it has no Write and no Archive.
type readOnlyCatalog struct{ drepo.Skills }
