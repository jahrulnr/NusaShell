package projectmemory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func testStore(t *testing.T) (st *Store, workspace, dataDir string) {
	t.Helper()
	dataDir = t.TempDir()
	workspace = filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	st = New(dataDir, nil)
	st.now = func() time.Time {
		return time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	}
	return st, workspace, dataDir
}

func memoryDir(dataDir, workspace string) string {
	return filepath.Join(dataDir, domain.ProjectMemoryDirName, domain.ProjectMemoryKey(workspace))
}

func debugContent(id, patternKey string) string {
	var b strings.Builder
	b.WriteString("ID: " + id + "\n")
	b.WriteString("KIND: DEBUG\n")
	b.WriteString("SCOPE: " + id + " scope\n")
	b.WriteString("SYMPTOM: " + id + " symptom\n")
	b.WriteString("ROOT_CAUSE: " + id + " root cause\n")
	b.WriteString("FIX: " + id + " fix\n")
	b.WriteString("VALIDATION_COMMAND: true\n")
	if patternKey != "" {
		b.WriteString("PATTERN_KEY: " + patternKey + "\n")
	}
	b.WriteString("REUSE: avoids repeating the same diagnostic flow\n")
	b.WriteString("PROMOTED_TO: []\n")
	b.WriteString("SINCE: 2026-07-24\n")
	return b.String()
}

func TestPatternTrackThresholdAndIdempotence(t *testing.T) {
	st, ws, data := testStore(t)
	if _, err := st.Admit(ws, "debug", "BUG-unrelated", debugContent("BUG-unrelated", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Admit(ws, "debug", "BUG-first", debugContent("BUG-first", "trace-turn")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Admit(ws, "debug", "BUG-second", debugContent("BUG-second", "trace-turn")); err != nil {
		t.Fatal(err)
	}
	patternsPath := filepath.Join(memoryDir(data, ws), "patterns.md")
	if b, err := os.ReadFile(patternsPath); err == nil && strings.Contains(string(b), "ID: P-") {
		t.Fatal("pattern was created before the recurrence threshold")
	}
	res, err := st.Admit(ws, "debug", "BUG-third", debugContent("BUG-third", "trace-turn"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(patternsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "PATTERN_KEY: trace-turn") {
		t.Fatalf("missing PATTERN_KEY: %s", got)
	}
	if !strings.Contains(got, "OCCURRENCES: 3") {
		t.Fatalf("missing OCCURRENCES: %s", got)
	}
	if !strings.Contains(got, "MEMBER_IDS: [BUG-first, BUG-second, BUG-third]") {
		t.Fatalf("member ids: %s", got)
	}
	if !strings.Contains(res.PatternNote, "has occurred 3x") {
		t.Fatalf("threshold suggestion missing: %q", res.PatternNote)
	}
	info, err := os.Stat(patternsPath)
	if err != nil {
		t.Fatal(err)
	}
	mtime := info.ModTime()
	time.Sleep(20 * time.Millisecond)
	if _, err := st.Admit(ws, "debug", "BUG-third", debugContent("BUG-third", "trace-turn")); err != nil {
		t.Fatal(err)
	}
	info2, err := os.Stat(patternsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info2.ModTime().Equal(mtime) {
		t.Fatal("pattern tracking was not idempotent (mtime churned)")
	}
	if strings.Count(string(mustRead(t, patternsPath)), "ID: P-") != 1 {
		t.Fatal("pattern tracking created a duplicate")
	}
}

func TestQueryRelatedAndArchive(t *testing.T) {
	st, ws, data := testStore(t)
	dir := memoryDir(data, ws)
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "debug.md"), `### BEGIN_ENTRY: BUG-deploy-health ###
ID: BUG-deploy-health
KIND: DEBUG
SCOPE: local deploy health check
TOPICS: [deploy, health-check]
LINKS: [validated_by:V-deploy-smoke, procedure:PB-local-deploy]
SYMPTOM: local fixture deployment stayed unhealthy
ROOT_CAUSE: readiness command targeted the wrong port
### END_ENTRY: BUG-deploy-health ###
`)
	mustWrite(t, filepath.Join(dir, "validation.md"), `### BEGIN_ENTRY: V-deploy-smoke ###
ID: V-deploy-smoke
KIND: VALIDATION
SCOPE: local deploy smoke
TOPICS: [deploy]
### END_ENTRY: V-deploy-smoke ###
`)
	mustWrite(t, filepath.Join(dir, "playbook.md"), `### BEGIN_ENTRY: PB-local-deploy ###
ID: PB-local-deploy
KIND: PLAYBOOK
STATUS: ACTIVE
SCOPE: local deploy
TOPICS: [deploy]
### END_ENTRY: PB-local-deploy ###
`)
	mustWrite(t, filepath.Join(dir, "archive", "debug.md"), `### BEGIN_ENTRY: BUG-old-deploy ###
ID: BUG-old-deploy
KIND: DEBUG
SCOPE: archived deploy
TOPICS: [deploy]
### END_ENTRY: BUG-old-deploy ###
`)

	hits, err := st.Query(ws, domain.ProjectMemoryQuery{Topic: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	ids := joinHitIDs(hits)
	if !strings.Contains(ids, "BUG-deploy-health") || !strings.Contains(ids, "V-deploy-smoke") {
		t.Fatalf("topic hits = %s", ids)
	}
	if strings.Contains(ids, "BUG-old-deploy") {
		t.Fatal("archive leaked without Archive=true")
	}

	hits, err = st.Query(ws, domain.ProjectMemoryQuery{Related: "V-deploy-smoke"})
	if err != nil {
		t.Fatal(err)
	}
	ids = joinHitIDs(hits)
	if !strings.Contains(ids, "BUG-deploy-health") || strings.Contains(ids, "V-deploy-smoke") {
		t.Fatalf("related inbound = %s", ids)
	}

	hits, err = st.Query(ws, domain.ProjectMemoryQuery{Related: "BUG-deploy-health"})
	if err != nil {
		t.Fatal(err)
	}
	ids = joinHitIDs(hits)
	if !strings.Contains(ids, "V-deploy-smoke") || !strings.Contains(ids, "PB-local-deploy") {
		t.Fatalf("related outbound = %s", ids)
	}

	hits, err = st.Query(ws, domain.ProjectMemoryQuery{ID: "BUG-deploy-health", Full: true})
	if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Body, "ROOT_CAUSE:") {
		t.Fatalf("full query = %+v err=%v", hits, err)
	}

	hits, err = st.Query(ws, domain.ProjectMemoryQuery{Topic: "deploy", Archive: true})
	if err != nil || !strings.Contains(joinHitIDs(hits), "BUG-old-deploy") {
		t.Fatalf("archive query = %s err=%v", joinHitIDs(hits), err)
	}

	listed, err := st.List(ws)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(listed, ",") != "playbook.md,debug.md,validation.md,archive/debug.md" {
		// playbook before debug in read order; validation after debug; archive last
		joined := strings.Join(listed, ",")
		if !strings.HasPrefix(joined, "playbook.md") || !strings.Contains(joined, "archive/debug.md") {
			t.Fatalf("list order = %s", joined)
		}
	}
}

func TestAdmitLintFailRollsBack(t *testing.T) {
	st, ws, data := testStore(t)
	_, err := st.Admit(ws, "debug", "BUG-ok", debugContent("BUG-ok", ""))
	if err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, filepath.Join(memoryDir(data, ws), "debug.md"))
	_, err = st.Admit(ws, "debug", "BUG-bad", `ID: BUG-bad
KIND: DEBUG
SCOPE: bad
TOPICS: [Deploy, too-many, topics, here]
`)
	if err == nil {
		t.Fatal("expected lint failure")
	}
	if _, ok := err.(*domain.ProjectMemoryLintError); !ok {
		t.Fatalf("want LintError, got %T %v", err, err)
	}
	after := mustRead(t, filepath.Join(memoryDir(data, ws), "debug.md"))
	if after != before {
		t.Fatal("lint failure left the kind file changed")
	}
}

func TestAdmitRejectsUserKindAndPrefixMismatch(t *testing.T) {
	st, ws, _ := testStore(t)
	if _, err := st.Admit(ws, "preferences", "P-x", "ID: P-x\nKIND: PATTERN\n"); err == nil {
		t.Fatal("preferences should be rejected")
	}
	if _, err := st.Admit(ws, "debug", "D-nope", "ID: D-nope\nKIND: DEBUG\nSCOPE: x\n"); err == nil {
		t.Fatal("prefix mismatch should be rejected")
	}
}

func TestArchiveMovesEntry(t *testing.T) {
	st, ws, data := testStore(t)
	if _, err := st.Admit(ws, "debug", "BUG-old", debugContent("BUG-old", "")); err != nil {
		t.Fatal(err)
	}
	if err := st.Archive(ws, "BUG-old"); err != nil {
		t.Fatal(err)
	}
	live := mustRead(t, filepath.Join(memoryDir(data, ws), "debug.md"))
	if strings.Contains(live, "BUG-old") {
		t.Fatal("live file still holds archived id")
	}
	arch := mustRead(t, filepath.Join(memoryDir(data, ws), "archive", "debug.md"))
	if !strings.Contains(arch, "ID: BUG-old") || !strings.Contains(arch, "STATUS: RETIRED") {
		t.Fatalf("archive body = %s", arch)
	}
}

func TestIndexExtract(t *testing.T) {
	st, ws, data := testStore(t)
	_, ok, err := st.IndexExtract(ws)
	if err != nil || ok {
		t.Fatalf("missing index should hide extract ok=%v err=%v", ok, err)
	}
	dir := memoryDir(data, ws)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "index.md"), `### BEGIN_ENTRY: IDX-project ###
ID: IDX-project
KIND: INDEX
PURPOSE: payments API
LOCKS: never rewrite auth
CURRENT_STATE: shipping v2
ROUTES: see playbook.md
### END_ENTRY: IDX-project ###
`)
	x, ok, err := st.IndexExtract(ws)
	if err != nil || !ok {
		t.Fatalf("extract ok=%v err=%v", ok, err)
	}
	if x.Purpose != "payments API" || x.Locks != "never rewrite auth" {
		t.Fatalf("extract = %+v", x)
	}
}

func TestOverrideBaseFromSettings(t *testing.T) {
	data := t.TempDir()
	other := t.TempDir()
	ws := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	base := other
	st := New(data, func() string { return base })
	if _, err := st.Admit(ws, "debug", "BUG-x", debugContent("BUG-x", "")); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(other, domain.ProjectMemoryKey(ws), "debug.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("override base was not used: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, domain.ProjectMemoryDirName, domain.ProjectMemoryKey(ws), "debug.md")); err == nil {
		t.Fatal("default dataDir base should not have been written")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func joinHitIDs(hits []domain.ProjectMemoryHit) string {
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(h.ID)
	}
	return b.String()
}
