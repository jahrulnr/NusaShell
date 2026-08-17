package domain

import (
	"testing"
	"time"
)

func TestBuildDAGParallelReady(t *testing.T) {
	jobs := []Job{
		{ID: "frontend", Steps: []Step{{Run: "echo f"}}},
		{ID: "backend", Steps: []Step{{Run: "echo b"}}},
		{ID: "build", Needs: []JobNeed{{Job: "frontend"}, {Job: "backend"}}, Steps: []Step{{Run: "echo x"}}},
	}
	d, issues := BuildDAG(jobs)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	if len(d.Order) != 3 {
		t.Fatalf("order = %v", d.Order)
	}
	status := map[string]RunStatus{"frontend": StatusQueued, "backend": StatusQueued, "build": StatusQueued}
	ready := ReadyJobs(d, status, nil)
	if len(ready) != 2 {
		t.Fatalf("ready = %v, want frontend+backend", ready)
	}
	status["frontend"] = StatusSuccess
	status["backend"] = StatusSuccess
	ready = ReadyJobs(d, status, nil)
	if len(ready) != 1 || ready[0] != "build" {
		t.Fatalf("ready after deps = %v, want [build]", ready)
	}
}

func TestBuildDAGCycle(t *testing.T) {
	jobs := []Job{
		{ID: "a", Needs: []JobNeed{{Job: "b"}}, Steps: []Step{{Run: "x"}}},
		{ID: "b", Needs: []JobNeed{{Job: "a"}}, Steps: []Step{{Run: "x"}}},
	}
	_, issues := BuildDAG(jobs)
	if len(issues) == 0 {
		t.Fatal("expected cycle")
	}
	found := false
	for _, i := range issues {
		if i.Code == "cycle" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want cycle, got %+v", issues)
	}
}

func TestBuildDAGUnknownNeed(t *testing.T) {
	jobs := []Job{
		{ID: "build", Needs: []JobNeed{{Job: "compile"}}, Steps: []Step{{Run: "x"}}},
	}
	_, issues := BuildDAG(jobs)
	if len(issues) != 1 || issues[0].Code != "unknown_job" {
		t.Fatalf("got %+v", issues)
	}
	if issues[0].Path != "jobs.build.needs[0]" {
		t.Fatalf("path = %s", issues[0].Path)
	}
}

func TestBuildDAGDuplicateJob(t *testing.T) {
	jobs := []Job{
		{ID: "a", Steps: []Step{{Run: "x"}}},
		{ID: "a", Steps: []Step{{Run: "y"}}},
	}
	_, issues := BuildDAG(jobs)
	if len(issues) == 0 || issues[0].Code != "duplicate_job" {
		t.Fatalf("got %+v", issues)
	}
}

func TestBlockedByFailurePropagates(t *testing.T) {
	jobs := []Job{
		{ID: "test", Steps: []Step{{Run: "x"}}},
		{ID: "build", Needs: []JobNeed{{Job: "test"}}, Steps: []Step{{Run: "x"}}},
		{ID: "pkg", Needs: []JobNeed{{Job: "build"}}, Steps: []Step{{Run: "x"}}},
	}
	d, issues := BuildDAG(jobs)
	if issues != nil {
		t.Fatal(issues)
	}
	status := map[string]RunStatus{"test": StatusFailed, "build": StatusQueued, "pkg": StatusQueued}
	blocked := BlockedByFailure(d, "test", false, status)
	if len(blocked) != 2 {
		t.Fatalf("blocked = %v", blocked)
	}
	blocked = BlockedByFailure(d, "test", true, status)
	if len(blocked) != 0 {
		t.Fatalf("continue_on_error should not block, got %v", blocked)
	}
}

func TestValidateSyntaxValid(t *testing.T) {
	at := time.Date(2026, 8, 18, 9, 0, 0, 0, time.FixedZone("WIB", 7*3600))
	w := &WorkflowDefinition{
		Name:    "NusaShell verification",
		Version: 1,
		Triggers: []Trigger{
			{Kind: TriggerManual, Manual: true},
			{Kind: TriggerOnce, At: &at, Timezone: "Asia/Jakarta"},
			{Kind: TriggerCron, Cron: "0 12 * * *", Timezone: "Asia/Jakarta"},
			{Kind: TriggerInterval, Interval: time.Hour},
			{Kind: TriggerEvent, Event: "email.received", Where: map[string]any{"mailbox": "finance"}},
		},
		Jobs: []Job{
			{ID: "frontend", Steps: []Step{{Name: "Test", Run: "node --test"}}},
			{ID: "backend", Steps: []Step{{Name: "Test", Run: "go test ./..."}}},
			{ID: "build", Needs: []JobNeed{{Job: "frontend"}, {Job: "backend"}}, Steps: []Step{{Run: "go build ./..."}}},
		},
	}
	r := ValidateSyntax(w)
	if r.Verdict() != "VALID" {
		t.Fatalf("verdict = %s issues=%+v", r.Verdict(), r.Issues)
	}
}

func TestValidateSyntaxDoesNotConflateMissingCapability(t *testing.T) {
	w := &WorkflowDefinition{
		Name: "x",
		Jobs: []Job{{ID: "a", Steps: []Step{{Uses: "email.send"}}}},
	}
	r := ValidateSyntax(w)
	if r.Verdict() != "VALID" {
		t.Fatalf("syntax should be valid without resolving capabilities: %+v", r.Issues)
	}
	if got := w.ReferencedCapabilities(); len(got) != 1 || got[0] != "email.send" {
		t.Fatalf("capabilities = %v", got)
	}
}

func TestValidateSyntaxCycleIsInvalid(t *testing.T) {
	w := &WorkflowDefinition{
		Name: "x",
		Jobs: []Job{
			{ID: "a", Needs: []JobNeed{{Job: "b"}}, Steps: []Step{{Run: "x"}}},
			{ID: "b", Needs: []JobNeed{{Job: "a"}}, Steps: []Step{{Run: "x"}}},
		},
	}
	r := ValidateSyntax(w)
	if r.Verdict() != "INVALID" || r.Syntax != "INVALID" {
		t.Fatalf("%+v", r)
	}
}

func TestValidateAbsoluteArtifactPath(t *testing.T) {
	cases := []string{
		"/etc/passwd",
		`\Windows\System32\config`,
		`C:\secrets\out.bin`,
		"../escape",
	}
	for _, p := range cases {
		w := &WorkflowDefinition{
			Name: "x",
			Jobs: []Job{{ID: "a", Artifacts: ArtifactSpec{Paths: []string{p}}, Steps: []Step{{Run: "x"}}}},
		}
		r := ValidateSyntax(w)
		if r.Verdict() != "INVALID" {
			t.Fatalf("path %q should be INVALID, got %+v", p, r.Issues)
		}
	}
	ok := &WorkflowDefinition{
		Name: "x",
		Jobs: []Job{{ID: "a", Artifacts: ArtifactSpec{Paths: []string{"dist/app.bin"}}, Steps: []Step{{Run: "x"}}}},
	}
	if r := ValidateSyntax(ok); r.Verdict() == "INVALID" {
		t.Fatalf("relative artifact path should be valid: %+v", r.Issues)
	}
}

func TestCanTransition(t *testing.T) {
	if !CanTransition(StatusQueued, StatusRunning) {
		t.Fatal("queued -> running")
	}
	if !CanTransition(StatusBlocked, StatusQueued) {
		t.Fatal("blocked can become runnable")
	}
	if CanTransition(StatusSuccess, StatusRunning) {
		t.Fatal("success is terminal without retry")
	}
	if !CanTransition(StatusFailed, StatusQueued) {
		t.Fatal("failed can retry")
	}
	if !CanTransition(StatusRunning, StatusWaiting) {
		t.Fatal("running -> waiting for wait_until")
	}
}
