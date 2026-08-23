package tools

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
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
