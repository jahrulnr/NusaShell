package domain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMemoryKey(t *testing.T) {
	got := ProjectMemoryKey("/apps/payments/api")
	if got != "apps-payments-api" {
		t.Fatalf("Key(/apps/payments/api) = %q, want apps-payments-api", got)
	}
	if ProjectMemoryKey("") != "unknown-project" {
		t.Fatalf("empty path should be unknown-project")
	}
	if ProjectMemoryKey("///") != "unknown-project" {
		t.Fatalf("slash-only path should be unknown-project, got %q", ProjectMemoryKey("///"))
	}
	mixed := ProjectMemoryKey("/Apps/Pay:ments/API extra")
	if mixed != "apps-pay-ments-api-extra" {
		t.Fatalf("mixed sanitization = %q", mixed)
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
	got := ResolveProjectMemoryBase("/data/nusashell", "")
	want := filepath.Join("/data/nusashell", ProjectMemoryDirName)
	if got != want {
		t.Fatalf("default base = %q, want %q", got, want)
	}
	prev := osUserHomeDir
	osUserHomeDir = func() (string, error) { return "/home/tester", nil }
	defer func() { osUserHomeDir = prev }()
	got = ResolveProjectMemoryBase("/data", "~/.memory")
	if got != filepath.Join("/home/tester", ".memory") {
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
	joined := problemsJoin(problems)
	if !strings.Contains(joined, "kebab-case") {
		t.Fatalf("expected kebab topic error, got %s", joined)
	}
	if !strings.Contains(joined, "unknown link relation") {
		t.Fatalf("expected unknown relation, got %s", joined)
	}
	if !strings.Contains(joined, "does not exist") {
		t.Fatalf("expected dangling target, got %s", joined)
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
