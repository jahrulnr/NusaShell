package tools

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
	docsinfra "nusashell/infrastructure/docs"
	"nusashell/infrastructure/memorystore"
)

// Dispatcher roots must behave exactly like their canonical per-op
// implementations — same handler, same idempotence guarantees.

func TestExecuteDispatcherMemorySaveRoutesAndDedups(t *testing.T) {
	dir := t.TempDir()
	fragments, err := memorystore.NewFragments(dir)
	if err != nil {
		t.Fatal(err)
	}
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.Fragments = fragments

	out, err := tb.Execute(context.Background(), "memory", []byte(`{"op":"save","content":"User prefers Indonesian\n","category":"user"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status: saved") {
		t.Fatalf("first dispatcher save output = %s", out)
	}
	out, err = tb.Execute(context.Background(), "memory", []byte(`{"op":"save","content":"  User prefers Indonesian  \r\n","category":"user"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status: unchanged") || !strings.Contains(out, "reason: exact_duplicate") {
		t.Fatalf("duplicate dispatcher save output = %s", out)
	}
}

func TestExecuteDispatcherSkillList(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git-helper", Description: "Help with git operations"}},
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
		[]*domain.Skill{{ID: "s1", Name: "git-helper"}},
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
// Toolbox.Execute. Regression guard for ci_pipeline ops that were
// advertised but unreachable: executeFamily had no case for them, so every
// call died with `unknown ci_pipeline_list op "list"` while the roster kept
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
	fragments, err := memorystore.NewFragments(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git-helper", Content: "how to rebase"}},
		nil, &stubMCP{},
	)
	tb.Docs = docsSource
	tb.Fragments = fragments

	cases := []struct{ name, args string }{
		{"skill", `{"op":"list"}`},
		{"skill", `{"op":"search","query":"git"}`},
		{"skill", `{"op":"save","name":"probe","content":"c"}`},
		{"memory", `{"op":"save","content":"User prefers Indonesian","category":"user"}`},
		{"memory", `{"op":"replace","target":"fragment","id":"nope","content":"c"}`},
		{"memory", `{"op":"search","query":"Indonesian"}`},
		{"memory", `{"op":"list"}`},
		{"memory", `{"op":"delete","id":"nope"}`},
		{"docs", `{"op":"search","query":"automation"}`},
		{"docs", `{"op":"read","id":"automation"}`},
	}
	for _, tc := range cases {
		if _, err := tb.Execute(context.Background(), tc.name, []byte(tc.args)); err != nil && strings.Contains(err.Error(), "unknown") {
			t.Fatalf("%s %s did not route to a handler: %v", tc.name, tc.args, err)
		}
	}

	// Retired per-op names stay unknown at the boundary — no alias door.
	for _, name := range []string{"ci_pipeline_list", "ci_pipeline_read", "memory_replace", "skill_save", "docs_read"} {
		if _, err := tb.Execute(context.Background(), name, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "unknown tool") {
			t.Fatalf("retired name %q must fail as unknown tool, got: %v", name, err)
		}
	}
}
