package domain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMemoryKey(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace := filepath.Join(workspaceRoot, "apps", "payments", "api")
	got := ProjectMemoryKey(workspace)
	if !strings.HasSuffix(got, "-apps-payments-api") {
		t.Fatalf("Key(%s) = %q, want suffix -apps-payments-api", workspace, got)
	}
	if ProjectMemoryKey("") != "unknown-project" {
		t.Fatalf("empty path should be unknown-project")
	}
	for _, rootOnly := range []string{"///", strings.Repeat("\\", 3)} {
		if ProjectMemoryKey(rootOnly) != "unknown-project" {
			t.Fatalf("root-only path %q should be unknown-project, got %q", rootOnly, ProjectMemoryKey(rootOnly))
		}
	}
	mixedPath := filepath.Join(workspaceRoot, "Apps", "Pay:ments", "API extra")
	mixed := ProjectMemoryKey(mixedPath)
	if !strings.HasSuffix(mixed, "-apps-pay-ments-api-extra") {
		t.Fatalf("mixed sanitization for %s = %q, want suffix -apps-pay-ments-api-extra", mixedPath, mixed)
	}
}

func TestNormalizeProjectKindFile(t *testing.T) {
	if got := NormalizeProjectKindFile("decision"); got != "decisions" {
		t.Fatalf("decision alias = %q", got)
	}
	if got := NormalizeProjectKindFile("DEV_ACCESS"); got != "dev-access" {
		t.Fatalf("underscore = %q", got)
	}
	if !IsCanonicalProjectKind("guardrails") {
		t.Fatal("guardrails should be canonical")
	}
	if IsCanonicalProjectKind("preferences") {
		t.Fatal("preferences must not be canonical")
	}
}

func TestProjectKindIDPrefix(t *testing.T) {
	if !ProjectKindIDValid("debug", "BUG-deploy-health") {
		t.Fatal("BUG- prefix should match debug")
	}
	if ProjectKindIDValid("debug", "D-reasoning") {
		t.Fatal("decision prefix must not match debug")
	}
	if ProjectKindIDValid("index", "IDX-project") != true {
		t.Fatal("IDX-project should match index")
	}
	if ProjectEntryKind("guardrails") != "GUARDRAIL" {
		t.Fatalf("guardrails entry kind = %q", ProjectEntryKind("guardrails"))
	}
	if ProjectEntryKind("dev-access") != "DEV_ACCESS" {
		t.Fatalf("dev-access entry kind = %q", ProjectEntryKind("dev-access"))
	}
}

func TestRejectProjectUserKind(t *testing.T) {
	for _, kind := range []string{"preferences", "user-profile", "USER_PROFILE", "user_profile"} {
		if RejectProjectUserKind(kind) == "" {
			t.Fatalf("expected reject for %q", kind)
		}
	}
	if RejectProjectUserKind("debug") != "" {
		t.Fatal("debug should be allowed")
	}
}

func TestResolveProjectMemoryBase(t *testing.T) {
	dataDir := t.TempDir()
	got := ResolveProjectMemoryBase(dataDir, "")
	want := filepath.Join(dataDir, ProjectMemoryDirName)
	if got != want {
		t.Fatalf("default base = %q, want %q", got, want)
	}
	homeDir := t.TempDir()
	prev := osUserHomeDir
	osUserHomeDir = func() (string, error) { return homeDir, nil }
	defer func() { osUserHomeDir = prev }()
	got = ResolveProjectMemoryBase(dataDir, "~/.memory")
	if got != filepath.Join(homeDir, ".memory") {
		t.Fatalf("~ override = %q", got)
	}
}

func TestParseAndQuerySelectors(t *testing.T) {
	debug := `### BEGIN_ENTRY: BUG-deploy-health ###
ID: BUG-deploy-health
KIND: DEBUG
SCOPE: local deploy health check
TOPICS: [deploy, health-check]
LINKS: [validated_by:V-deploy-smoke, procedure:PB-local-deploy]
ROOT_CAUSE: readiness command targeted the wrong port
### END_ENTRY: BUG-deploy-health ###
`
	validation := `### BEGIN_ENTRY: V-deploy-smoke ###
ID: V-deploy-smoke
KIND: VALIDATION
SCOPE: local deploy smoke
TOPICS: [deploy]
### END_ENTRY: V-deploy-smoke ###
`
	playbook := `### BEGIN_ENTRY: PB-local-deploy ###
ID: PB-local-deploy
KIND: PLAYBOOK
SCOPE: local deploy
TOPICS: [deploy]
### END_ENTRY: PB-local-deploy ###
`
	archived := `### BEGIN_ENTRY: BUG-old-deploy ###
ID: BUG-old-deploy
KIND: DEBUG
SCOPE: archived deploy
TOPICS: [deploy]
### END_ENTRY: BUG-old-deploy ###
`
	var all []ProjectMemoryEntry
	all = append(all, ParseProjectMemoryEntries(debug, "debug.md", "debug", false)...)
	all = append(all, ParseProjectMemoryEntries(validation, "validation.md", "validation", false)...)
	all = append(all, ParseProjectMemoryEntries(playbook, "playbook.md", "playbook", false)...)
	liveOnly := all
	all = append(all, ParseProjectMemoryEntries(archived, "archive/debug.md", "debug", true)...)

	hits := MatchProjectMemoryQuery(liveOnly, ProjectMemoryQuery{Topic: "deploy"})
	ids := hitIDs(hits)
	if !strings.Contains(ids, "BUG-deploy-health") || !strings.Contains(ids, "V-deploy-smoke") {
		t.Fatalf("topic deploy hits = %s", ids)
	}
	if strings.Contains(ids, "BUG-old-deploy") {
		t.Fatal("archive must be excluded without Archive=true")
	}

	hits = MatchProjectMemoryQuery(liveOnly, ProjectMemoryQuery{Related: "V-deploy-smoke"})
	ids = hitIDs(hits)
	if !strings.Contains(ids, "BUG-deploy-health") {
		t.Fatalf("inbound related missing: %s", ids)
	}
	if strings.Contains(ids, "V-deploy-smoke") {
		t.Fatal("related query must exclude the related id itself")
	}

	hits = MatchProjectMemoryQuery(liveOnly, ProjectMemoryQuery{Related: "BUG-deploy-health"})
	ids = hitIDs(hits)
	if !strings.Contains(ids, "V-deploy-smoke") || !strings.Contains(ids, "PB-local-deploy") {
		t.Fatalf("outbound related missing: %s", ids)
	}
	if strings.Contains(ids, "BUG-deploy-health") {
		t.Fatal("related id itself leaked")
	}

	hits = MatchProjectMemoryQuery(liveOnly, ProjectMemoryQuery{ID: "BUG-deploy-health", Full: true})
	if len(hits) != 1 || !strings.Contains(hits[0].Body, "ROOT_CAUSE: readiness command targeted the wrong port") {
		t.Fatalf("full id query = %+v", hits)
	}

	hits = MatchProjectMemoryQuery(all, ProjectMemoryQuery{Topic: "deploy", Archive: true})
	if !strings.Contains(hitIDs(hits), "BUG-old-deploy") {
		t.Fatal("archive selector should include BUG-old-deploy")
	}

	hits = MatchProjectMemoryQuery(liveOnly, ProjectMemoryQuery{Kind: "dev-access"})
	if len(hits) != 0 {
		t.Fatalf("kind filter leaked: %+v", hits)
	}
	hits = MatchProjectMemoryQuery(liveOnly, ProjectMemoryQuery{Kind: "debug"})
	if hitIDs(hits) != "BUG-deploy-health" {
		t.Fatalf("kind debug = %s", hitIDs(hits))
	}
}

func TestLintMalformedTopicsAndLinks(t *testing.T) {
	raw := `### BEGIN_ENTRY: X-bad-links ###
ID: X-bad-links
KIND: EXAMPLE
SCOPE: invalid retrieval metadata
TOPICS: [Deploy, too-many, topics, here]
LINKS: [mystery:V-missing]
### END_ENTRY: X-bad-links ###
`
	problems := LintProjectMemory([]ProjectMemoryFileBlob{{
		Rel: "bad-links.md", Kind: "bad-links", Raw: raw,
	}}, 3)
	if len(problems) == 0 {
		t.Fatal("lint accepted malformed topics and dangling links")
	}
	got := FormatLintReport(problems)
	want := strings.Join([]string{
		"LINT FAIL [bad-links.md]: X-bad-links has 4 TOPICS; maximum is 3.",
		"LINT FAIL [bad-links.md]: X-bad-links topic 'Deploy' must be lowercase kebab-case.",
		"LINT FAIL [bad-links.md]: X-bad-links uses unknown link relation 'mystery'.",
		"LINT FAIL [bad-links.md]: X-bad-links link target 'V-missing' does not exist in live memory or archive.",
		"memory-lint: 4 issue(s) found.",
	}, "\n")
	if got != want {
		t.Fatalf("lint report != memory-lint.sh stdout\ngot:\n%s\nwant:\n%s", got, want)
	}
	err := (&ProjectMemoryLintError{Problems: problems}).Error()
	if err != want {
		t.Fatalf("LintError.Error() must match memory-lint.sh stdout, got:\n%s", err)
	}
}

func TestLintReportsMalformedLinkWithoutColon(t *testing.T) {
	raw := `### BEGIN_ENTRY: X-bad-form ###
ID: X-bad-form
KIND: EXAMPLE
SCOPE: missing colon
LINKS: [related_to D-missing]
### END_ENTRY: X-bad-form ###
`
	problems := LintProjectMemory([]ProjectMemoryFileBlob{{
		Rel: "bad-form.md", Kind: "bad-form", Raw: raw,
	}}, 3)
	got := FormatLintReport(problems)
	if !strings.Contains(got, "X-bad-form link 'related_to D-missing' must be relation:TARGET_ID.") {
		t.Fatalf("expected malformed-link message from memory-lint.sh, got:\n%s", got)
	}
}

func TestLintOrphansAndDuplicateScopeMatchScript(t *testing.T) {
	raw := `# heading allowed
loose fact that is invisible to anchors

### BEGIN_ENTRY: D-one ###
ID: D-one
KIND: DECISION
STATUS: ACTIVE
SCOPE: login service
### END_ENTRY: D-one ###
### BEGIN_ENTRY: D-two ###
ID: D-two
KIND: DECISION
STATUS: ACTIVE
SCOPE: login service
### END_ENTRY: D-two ###
`
	problems := LintProjectMemory([]ProjectMemoryFileBlob{{
		Rel: "decisions.md", Kind: "decisions", Raw: raw,
	}}, 3)
	got := FormatLintReport(problems)
	if !strings.Contains(got, "LINT FAIL [decisions.md]: unresolved duplicate SCOPE \"login service\":") {
		t.Fatalf("missing duplicate SCOPE block:\n%s", got)
	}
	if !strings.Contains(got, "  ID=D-one STATUS=ACTIVE") || !strings.Contains(got, "  ID=D-two STATUS=ACTIVE") {
		t.Fatalf("missing SCOPE member rows:\n%s", got)
	}
	if !strings.Contains(got, "  -> merge into one entry, mark the older one SUPERSEDED/RETIRED, set SUPERSEDES.") {
		t.Fatalf("missing duplicate SCOPE hint:\n%s", got)
	}
	if !strings.Contains(got, "LINT FAIL [decisions.md]: text found outside any anchored entry (invisible to anchor-based reads):") {
		t.Fatalf("missing orphan header:\n%s", got)
	}
	if !strings.Contains(got, "  line 2: loose fact that is invisible to anchors") {
		t.Fatalf("missing orphan line listing:\n%s", got)
	}
}

func TestFormatProjectMemoryHitsMatchesQueryScript(t *testing.T) {
	hits := []ProjectMemoryHit{
		{ID: "BUG-deploy-health", Kind: "debug", File: "debug.md", Scope: "local deploy health check", Body: "### BEGIN_ENTRY: BUG-deploy-health ###\nID: BUG-deploy-health\n### END_ENTRY: BUG-deploy-health ###\n"},
	}
	compact := FormatProjectMemoryHits(hits, false)
	if compact != "BUG-deploy-health\tdebug\tdebug.md\tlocal deploy health check\n" {
		t.Fatalf("compact query != memory-query.sh TSV: %q", compact)
	}
	full := FormatProjectMemoryHits(hits, true)
	if !strings.HasPrefix(full, "### BEGIN_ENTRY: BUG-deploy-health ###\n") {
		t.Fatalf("full query missing body: %q", full)
	}
	if !strings.HasSuffix(full, "\n\n") {
		t.Fatalf("memory-query.sh --full prints body plus extra newline, got %q", full)
	}
}

func TestFormatPatternTrackNoteMatchesScript(t *testing.T) {
	got := FormatPatternTrackNote("trace-turn", "debug", 3, "/tmp/memory/proj/scripts/trace-turn.sh")
	want := "memory-pattern: 'trace-turn' (debug) has occurred 3x.\n  Promote the stable procedure to playbook.md.\n  If the steps are deterministic, consider a shared shortcut script:\n    /tmp/memory/proj/scripts/trace-turn.sh"
	if got != want {
		t.Fatalf("pattern note != memory-pattern-track.sh stdout\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatLintReportClean(t *testing.T) {
	if got := FormatLintReport(nil); got != "memory-lint: clean" {
		t.Fatalf("clean report = %q", got)
	}
}

func TestEvaluateMemoryGateMatchesScript(t *testing.T) {
	pass := EvaluateMemoryGate(MemoryGateInput{})
	if !pass.OK || !strings.Contains(pass.Message, "memory-lint: clean") || !strings.Contains(pass.Message, "no working-tree changes to evaluate") {
		t.Fatalf("empty worktree gate = %+v", pass)
	}
	fail := EvaluateMemoryGate(MemoryGateInput{
		ChangedFiles: []string{"a.go", "b.go", "c.go"},
	})
	if fail.OK || !strings.Contains(fail.Message, "3 file(s) changed") {
		t.Fatalf("durable without memory = %+v", fail)
	}
	if !strings.Contains(fail.Message, `memory_project(op="gate", reason=`) {
		t.Fatalf("gate fail must point at the NusaShell op, got %s", fail.Message)
	}
	ok := EvaluateMemoryGate(MemoryGateInput{
		ChangedFiles:   []string{"a.go", "b.go", "c.go"},
		NoUpdateReason: "implementation only",
	})
	if !ok.OK || !strings.Contains(ok.Message, "no memory update admitted: implementation only") {
		t.Fatalf("--no-update equivalent = %+v", ok)
	}
}

func TestLintDevAccessContract(t *testing.T) {
	good := `### BEGIN_ENTRY: DEV-local-admin ###
ID: DEV-local-admin
KIND: DEV_ACCESS
STATUS: ACTIVE
SCOPE: disposable local admin fixture
TOPICS: [local-development, auth]
ENVIRONMENT: local-development
MATERIAL_TYPE: username-password
ACCESS: username=admin; password=admin
SAFE_TO_DISCLOSE: true
PRODUCTION_REUSE: forbidden
SOURCE: checked-in:testdata/local-users.json
VERIFY: target is bound to localhost and contains fixture data only
LAST_VERIFIED: 2026-07-24
SUPERSEDES: []
### END_ENTRY: DEV-local-admin ###
`
	files := []ProjectMemoryFileBlob{{Rel: "dev-access.md", Kind: "dev-access", Raw: good}}
	if problems := LintProjectMemory(files, 3); len(problems) != 0 {
		t.Fatalf("clean fixture failed: %s", problemsJoin(problems))
	}
	unsafe := strings.ReplaceAll(good, "SAFE_TO_DISCLOSE: true", "SAFE_TO_DISCLOSE: false")
	if problems := LintProjectMemory([]ProjectMemoryFileBlob{{Rel: "dev-access.md", Kind: "dev-access", Raw: unsafe}}, 3); len(problems) == 0 {
		t.Fatal("lint accepted missing disclosure safety")
	}
	badSource := strings.ReplaceAll(good, "SOURCE: checked-in:testdata/local-users.json", "SOURCE: copied-from:.env")
	if problems := LintProjectMemory([]ProjectMemoryFileBlob{{Rel: "dev-access.md", Kind: "dev-access", Raw: badSource}}, 3); len(problems) == 0 {
		t.Fatal("lint accepted untraceable source")
	}
}

func TestLintMultipleIndexEntries(t *testing.T) {
	raw := `### BEGIN_ENTRY: IDX-one ###
ID: IDX-one
KIND: INDEX
SCOPE: first
### END_ENTRY: IDX-one ###
### BEGIN_ENTRY: IDX-two ###
ID: IDX-two
KIND: INDEX
SCOPE: second
### END_ENTRY: IDX-two ###
`
	problems := LintProjectMemory([]ProjectMemoryFileBlob{{Rel: "index.md", Kind: "index", Raw: raw}}, 3)
	if len(problems) == 0 {
		t.Fatal("lint accepted multiple live INDEX entries")
	}
}

func TestCompactProjectIndex(t *testing.T) {
	raw := `### BEGIN_ENTRY: IDX-project ###
ID: IDX-project
KIND: INDEX
PURPOSE: payments API
LOCKS: never rewrite auth
CURRENT_STATE: shipping v2
ROUTES: see playbook.md
### END_ENTRY: IDX-project ###
`
	ents := ParseProjectMemoryEntries(raw, "index.md", "index", false)
	x := CompactProjectIndex(ents[0])
	if x.Purpose != "payments API" || x.Locks != "never rewrite auth" {
		t.Fatalf("extract = %+v", x)
	}
}

func TestNormalizePatternKey(t *testing.T) {
	if NormalizePatternKey("none") != "" || NormalizePatternKey("n-a") != "" {
		t.Fatal("none/n-a must be ignored")
	}
	if got := NormalizePatternKey("Trace Turn"); got != "trace-turn" {
		t.Fatalf("sanitize = %q", got)
	}
	if PatternEntryID("debug", "trace-turn") != "P-debug-trace-turn" {
		t.Fatalf("id = %s", PatternEntryID("debug", "trace-turn"))
	}
}

func hitIDs(hits []ProjectMemoryHit) string {
	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(h.ID)
	}
	return b.String()
}

func problemsJoin(ps []ProjectMemoryLintProblem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Message)
		b.WriteByte('\n')
	}
	return b.String()
}
