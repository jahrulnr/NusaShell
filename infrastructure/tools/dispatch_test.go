package tools

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
	docsinfra "nusashell/infrastructure/docs"
	"nusashell/infrastructure/jsonstore"
)

// Dispatcher roots must behave exactly like their canonical per-op
// implementations — same handler, same idempotence guarantees.

func TestExecuteDispatcherMemorySearchRoutes(t *testing.T) {
	st, err := jsonstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recs := &jsonstore.MemoryRecords{S: st}
	if err := recs.Save(&domain.MemoryRecord{
		ID:     "mem_1",
		Type:   domain.MemoryTypePreference,
		Body:   "User prefers Indonesian",
		Status: domain.MemoryStatusLearned,
	}); err != nil {
		t.Fatal(err)
	}
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.MemoryRecords = recs

	out, err := tb.Execute(context.Background(), "memory", []byte(`{"op":"search","query":"Indonesian"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mem_1") {
		t.Fatalf("dispatcher search output = %s", out)
	}
	if _, err := tb.Execute(context.Background(), "memory", []byte(`{"op":"save","content":"User prefers Indonesian"}`)); err == nil {
		t.Fatal("memory save must not route")
	}
}

func TestExecuteDispatcherSkillList(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git-helper", Description: "Help with git operations", Status: domain.SkillStatusTrusted}},
		nil, &stubMCP{},
	)
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "git-helper") {
		t.Fatalf("dispatcher skill list output = %s", out)
	}
}

func TestExecuteDispatcherDocsList(t *testing.T) {
	docsSource, err := docsinfra.New("")
	if err != nil {
		t.Fatal(err)
	}
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.Docs = docsSource

	out, err := tb.Execute(context.Background(), "docs", []byte(`{"op":"list","limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"count: 5", "limit: 5", "automation"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dispatcher docs list output missing %q: %s", want, out)
		}
	}
}

func TestExecuteDispatcherUnknownOpFailsLoud(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	_, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"bogus"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown skill op "bogus"`) || !strings.Contains(err.Error(), "list") {
		t.Fatalf("unknown op error must be self-describing, got: %v", err)
	}
	_, err = tb.Execute(context.Background(), "memory", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "valid ops") {
		t.Fatalf("missing op error must list valid ops, got: %v", err)
	}
}

// Retired per-op names have no door at the toolbox either: only the
// resolved root+op form reaches executeFamily, so a direct legacy call is
// just an unknown tool (fail loud).
func TestRetiredPerOpNamesAreUnknownTools(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git-helper", Status: domain.SkillStatusTrusted}},
		nil, &stubMCP{},
	)
	if _, err := tb.Execute(context.Background(), "skill_list", []byte(`{}`)); err == nil {
		t.Fatal("retired per-op name must be an unknown tool")
	}
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("root+op form must route: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty skill list output")
	}
}

// Every advertised family root+op must reach a real handler through
// Toolbox.Execute. Regression guard for automation pipeline ops that were
// advertised but unreachable: executeFamily had no case for them, so every
// call died with `unknown automation pipeline_list op "list"` while the roster kept
// offering the tool (found by live probe 2026-08-23).
//
// The assertion is routing-only: any error is acceptable as long as it is
// not the executeFamily default (`unknown ... op`) — dependency errors like
// "automation is not configured" prove the call reached its handler.
func TestAllAdvertisedFamilyOpsRoute(t *testing.T) {
	docsSource, err := docsinfra.New("")
	if err != nil {
		t.Fatal(err)
	}
	st, err := jsonstore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recs := &jsonstore.MemoryRecords{S: st}
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git-helper", Content: "how to rebase", Status: domain.SkillStatusTrusted}},
		nil, &stubMCP{},
	)
	tb.Docs = docsSource
	tb.MemoryRecords = recs

	cases := []struct{ name, args string }{
		{"skill", `{"op":"list"}`},
		{"skill", `{"op":"search","query":"git"}`},
		{"skill", `{"op":"save","name":"probe","content":"c"}`},
		{"skill", `{"op":"delete","id":"probe"}`},
		{"memory", `{"op":"search","query":"Indonesian"}`},
		{"memory", `{"op":"get","id":"nope"}`},
		{"memory", `{"op":"list"}`},
		{"docs", `{"op":"list"}`},
		{"docs", `{"op":"search","query":"automation"}`},
		{"docs", `{"op":"read","id":"automation"}`},
		{"memory_project", `{"op":"list"}`},
		{"memory_project", `{"op":"query","kind":"index"}`},
		{"memory_project", `{"op":"read","kind":"index"}`},
		{"memory_project", `{"op":"admit","kind":"debug","id":"BUG-x","content":"SCOPE: x"}`},
		{"memory_project", `{"op":"skip","reason":"nothing durable"}`},
		{"memory_project", `{"op":"archive","id":"BUG-x"}`},
		{"memory_project", `{"op":"lint"}`},
		{"automation", `{"op":"run","workflow_id":"w"}`},
		{"automation", `{"op":"status","run_id":"r"}`},
		{"automation", `{"op":"list"}`},
		{"automation", `{"op":"delete","workflow_id":"w"}`},
		{"automation_schedule", `{"op":"once","at":"2026-09-02T09:00:00Z","yaml":"name: x"}`},
		{"automation_schedule", `{"op":"every","interval":"1h","yaml":"name: x"}`},
		{"conversation", `{"op":"list"}`},
		{"conversation", `{"op":"search","query":"backend"}`},
		{"conversation", `{"op":"send","id":"conv_target","content":"hello"}`},
	}
	for _, tc := range cases {
		if _, err := tb.Execute(context.Background(), tc.name, []byte(tc.args)); err != nil && strings.Contains(err.Error(), "unknown") {
			t.Fatalf("%s %s did not route to a handler: %v", tc.name, tc.args, err)
		}
	}

	// Retired per-op names stay unknown at the boundary — no alias door.
	for _, name := range []string{"ci_pipeline_list", "ci_pipeline_read", "ci_run", "ci_list", "automation_run", "automation_schedule_once", "memory_replace", "skill_save", "docs_read"} {
		if _, err := tb.Execute(context.Background(), name, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "unknown tool") {
			t.Fatalf("retired name %q must fail as unknown tool, got: %v", name, err)
		}
	}
}
