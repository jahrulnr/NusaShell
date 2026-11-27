package appmemory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/redact"
	repomemory "github.com/jahrulnr/nusashell/infrastructure/repository/memory"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

type rooms map[string]string // conversation → project; "off" keeps memory out

func (r rooms) Info(_ context.Context, id string) (drepo.ConversationInfo, error) {
	p, ok := r[id]
	if !ok {
		return drepo.ConversationInfo{}, apperror.New(404, "conversation not found", nil)
	}
	if p == "off" {
		return drepo.ConversationInfo{ID: id, MemoryOff: true}, nil
	}
	return drepo.ConversationInfo{ID: id, ProjectID: p}, nil
}

// embedder maps each text onto a 2-d vector by keyword: "deploy" texts
// point one way, everything else the other.
type embedder struct{ calls int }

func (e *embedder) Embed(_ context.Context, texts []string, _ dservice.EmbedPurpose) ([][]float32, string, error) {
	e.calls++
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if strings.Contains(strings.ToLower(t), "deploy") || strings.Contains(strings.ToLower(t), "ship") {
			out[i] = []float32{1, 0.05}
		} else {
			out[i] = []float32{0.05, 1}
		}
	}
	return out, "p/e", nil
}

func setup(t *testing.T, emb Embedder) (*App, *time.Time, *bool) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "n.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	on := true
	mode := "suggest"
	app := New(Deps{
		Memories: repomemory.New(db), Proposals: repomemory.NewProposals(db),
		Rooms: rooms{"c_web": "", "c_proj": "p_1", "c_other": "p_2", "c_off": "off"}, Embed: emb, Redact: redact.String,
		Enabled: func() bool { return on }, Consolidation: func() string { return mode },
	})
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	app.now = func() time.Time { return now }
	return app, &now, &on
}

func code(err error) int {
	if ae, ok := errors.AsType[*apperror.AppError](err); ok {
		return ae.StatusCode()
	}
	return 0
}

func TestSaveRewritesByNameAndScopesByType(t *testing.T) {
	app, _, _ := setup(t, nil)
	ctx := context.Background()
	first, err := app.Save(ctx, "c_proj", SaveInput{Name: "terse", Type: "feedback", Description: "Owner wants short replies", Body: "Keep it short."})
	if err != nil || !first.Created || first.Memory.Scope != "global" {
		t.Fatalf("feedback defaults to global: %+v %v", first, err)
	}
	res, err := app.Save(ctx, "c_proj", SaveInput{Name: "db", Type: "project", Description: "Postgres 17 is the store", Body: "Chosen for JSONB."})
	if err != nil || res.Memory.Scope != "project:p_1" {
		t.Fatalf("project defaults to the thread's project: %+v %v", res, err)
	}
	res, err = app.Save(ctx, "c_web", SaveInput{Name: "db2", Type: "project", Description: "d", Body: "b"})
	if err != nil || res.Memory.Scope != "global" {
		t.Fatalf("project with no project goes global: %+v %v", res, err)
	}
	if _, err := app.Save(ctx, "c_web", SaveInput{Name: "x", Type: "project", Scope: "project", Description: "d", Body: "b"}); code(err) != 400 {
		t.Fatalf("project scope without a project: %v", err)
	}
	again, err := app.Save(ctx, "c_proj", SaveInput{Name: "terse", Type: "feedback", Description: "Owner wants short replies, no headers", Body: "Short, no headings."})
	if err != nil || again.Created || again.Memory.ID != first.Memory.ID || again.Memory.Body != "Short, no headings." {
		t.Fatalf("same name rewrites: %+v %v", again, err)
	}
	if _, err := app.Save(ctx, "c_proj", SaveInput{Name: "key", Type: "reference", Description: "API key", Body: "the key is sk-abcdefghijklmnop"}); code(err) != 400 {
		t.Fatalf("credential saved: %v", err)
	}
	if _, err := app.Save(ctx, "c_proj", SaveInput{Name: "Bad Name", Type: "user", Description: "d", Body: "b"}); code(err) != 400 {
		t.Fatalf("bad name: %v", err)
	}
	if _, err := app.Save(ctx, "c_missing", SaveInput{Name: "a", Type: "user", Description: "d", Body: "b"}); code(err) != 404 {
		t.Fatalf("unknown thread: %v", err)
	}
}

// A thread sees global memory, its project's, and its own — never another
// project's or another thread's.
func TestVisibilityAndIndex(t *testing.T) {
	app, now, on := setup(t, nil)
	ctx := context.Background()
	must := func(conv string, in SaveInput) {
		t.Helper()
		if _, err := app.Save(ctx, conv, in); err != nil {
			t.Fatal(err)
		}
	}
	must("c_proj", SaveInput{Name: "who", Type: "user", Description: "Owner is a Go backend engineer", Body: "b"})
	must("c_proj", SaveInput{Name: "stack", Type: "project", Description: "Service runs on Postgres", Body: "b"})
	must("c_other", SaveInput{Name: "other-stack", Type: "project", Description: "Other project uses Mongo", Body: "b"})
	must("c_other", SaveInput{Name: "note", Type: "project", Scope: "thread", Description: "Thread-only note", Body: "b"})

	idx, err := app.Index(ctx, "c_proj")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"who [global]", "stack [project]", "## user", "## project"} {
		if !strings.Contains(idx, want) {
			t.Errorf("index lacks %q:\n%s", want, idx)
		}
	}
	for _, leak := range []string{"other-stack", "note"} {
		if strings.Contains(idx, leak) {
			t.Errorf("index leaks %q:\n%s", leak, idx)
		}
	}
	if strings.Index(idx, "## user") > strings.Index(idx, "## project") {
		t.Errorf("user memories come before project ones:\n%s", idx)
	}
	if _, err := app.Recall(ctx, "c_proj", RecallInput{Names: []string{"other-stack"}}); err != nil {
		t.Fatal(err)
	} else if res, _ := app.Recall(ctx, "c_proj", RecallInput{Names: []string{"other-stack"}}); len(res.Missing) != 1 {
		t.Fatalf("another project's memory recalled: %+v", res)
	}

	// A project memory ages: past 15 days "aging", past 30 "STALE".
	*now = now.Add(16 * 24 * time.Hour)
	if idx, _ := app.Index(ctx, "c_proj"); !strings.Contains(idx, "Service runs on Postgres · aging") || strings.Contains(idx, "engineer · aging") {
		t.Fatalf("aging:\n%s", idx)
	}
	*now = now.Add(15 * 24 * time.Hour)
	if idx, _ := app.Index(ctx, "c_proj"); !strings.Contains(idx, "Postgres · STALE") {
		t.Fatalf("stale:\n%s", idx)
	}
	if _, err := app.Mark(ctx, "c_proj", "stack", "", "verified", ""); err != nil {
		t.Fatal(err)
	}
	if idx, _ := app.Index(ctx, "c_proj"); strings.Contains(idx, "Postgres · ") {
		t.Fatalf("verified memory still flagged:\n%s", idx)
	}

	*on = false
	if idx, err := app.Index(ctx, "c_proj"); err != nil || idx != "" {
		t.Fatalf("memory off still indexes: %q %v", idx, err)
	}
	if _, err := app.Save(ctx, "c_proj", SaveInput{Name: "z", Type: "user", Description: "d", Body: "b"}); code(err) != 409 {
		t.Fatalf("memory off still saves: %v", err)
	}
}

func TestMarkForgetRestorePurge(t *testing.T) {
	app, now, _ := setup(t, nil)
	ctx := context.Background()
	res, _ := app.Save(ctx, "c_web", SaveInput{Name: "editor", Type: "user", Description: "Owner uses Helix", Body: "b"})
	if _, err := app.Mark(ctx, "c_web", "editor", "", "stale", ""); code(err) != 400 {
		t.Fatalf("stale without a reason: %v", err)
	}
	v, err := app.Mark(ctx, "c_web", "editor", "", "stale", "owner mentioned switching to Zed")
	if err != nil || v.Status != "stale" || v.Freshness != "stale" {
		t.Fatalf("mark stale: %+v %v", v, err)
	}
	if idx, _ := app.Index(ctx, "c_web"); !strings.Contains(idx, "STALE: owner mentioned switching to Zed") {
		t.Fatalf("index:\n%s", idx)
	}
	if got, _ := app.Recall(ctx, "c_web", RecallInput{Query: "helix editor"}); len(got.Memories) != 0 {
		t.Fatalf("search returned a stale memory by default: %+v", got)
	}
	if got, _ := app.Recall(ctx, "c_web", RecallInput{Query: "helix editor", IncludeStale: true}); len(got.Memories) != 1 {
		t.Fatalf("include_stale: %+v", got)
	}
	if _, err := app.Forget(ctx, "c_web", "editor", "", "owner asked"); err != nil {
		t.Fatal(err)
	}
	if idx, _ := app.Index(ctx, "c_web"); strings.Contains(idx, "editor") {
		t.Fatalf("forgotten memory indexed:\n%s", idx)
	}
	// The name is free again; restoring the old one then clashes.
	if _, err := app.Save(ctx, "c_web", SaveInput{Name: "editor", Type: "user", Description: "Owner uses Zed", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Act(ctx, res.Memory.ID, "restore", ""); code(err) != 409 {
		t.Fatalf("restore over a live name: %v", err)
	}
	*now = now.Add(31 * 24 * time.Hour)
	if n, err := app.Purge(ctx); err != nil || n != 1 {
		t.Fatalf("purge = %d, %v", n, err)
	}
}

func TestRecallBlendsSemanticAndReportsSimilar(t *testing.T) {
	emb := &embedder{}
	app, _, _ := setup(t, emb)
	ctx := context.Background()
	if _, err := app.Save(ctx, "c_web", SaveInput{Name: "release-flow", Type: "project", Description: "How releases go out", Body: "Tag, then ship with make release."}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Save(ctx, "c_web", SaveInput{Name: "cats", Type: "user", Description: "Owner has two cats", Body: "Named Mochi and Kopi."}); err != nil {
		t.Fatal(err)
	}
	// No shared words with "release-flow", but the same direction.
	res, err := app.Recall(ctx, "c_web", RecallInput{Query: "how do we deploy"})
	if err != nil || len(res.Memories) == 0 || res.Memories[0].Name != "release-flow" {
		t.Fatalf("semantic recall: %+v %v", res, err)
	}
	if res.Memories[0].Recalls != 1 {
		t.Fatalf("recall not counted: %+v", res.Memories[0])
	}
	saved, err := app.Save(ctx, "c_web", SaveInput{Name: "deploy-steps", Type: "project", Description: "Deploy steps", Body: "Ship with make release after tagging."})
	if err != nil || len(saved.Similar) == 0 || saved.Similar[0].Name != "release-flow" {
		t.Fatalf("overlap not reported: %+v %v", saved, err)
	}
	for _, s := range saved.Similar {
		if s.Name == "cats" {
			t.Fatalf("unrelated memory reported similar: %+v", saved.Similar)
		}
	}
	idx, err := app.Recall(ctx, "c_web", RecallInput{})
	if err != nil || !strings.Contains(idx.Index, "release-flow") {
		t.Fatalf("empty recall answers the index: %+v %v", idx, err)
	}
}

// Deleting a project can be undone, so its memories are archived, not
// dropped, and come back with it — but not ones forgotten before.
func TestProjectDeleteArchivesAndRestoreBringsBack(t *testing.T) {
	app, _, _ := setup(t, nil)
	ctx := context.Background()
	for _, n := range []string{"keep", "gone"} {
		if _, err := app.Save(ctx, "c_proj", SaveInput{Name: n, Type: "project", Description: "d " + n, Body: "b"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Forget(ctx, "c_proj", "gone", "", "was wrong"); err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetProject(ctx, "p_1"); err != nil {
		t.Fatal(err)
	}
	if idx, _ := app.Index(ctx, "c_proj"); strings.Contains(idx, "keep") {
		t.Fatalf("deleted project's memory indexed:\n%s", idx)
	}
	if err := app.RestoreProject(ctx, "p_1"); err != nil {
		t.Fatal(err)
	}
	idx, _ := app.Index(ctx, "c_proj")
	if !strings.Contains(idx, "keep") || strings.Contains(idx, "gone") {
		t.Fatalf("restore:\n%s", idx)
	}
}
