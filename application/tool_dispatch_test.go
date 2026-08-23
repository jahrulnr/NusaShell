package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
)

func TestDispatchCanonicalRoutesOps(t *testing.T) {
	cases := []struct {
		root string
		args string
		want string
	}{
		{"skill", `{"op":"list","limit":5}`, "skill_list"},
		{"skill", `{"op":"save","name":"x","content":"y"}`, "skill_save"},
		{"memory", `{"op":"save","content":"x"}`, "memory_save"},
		{"memory", `{"op":"SAVE","content":"x"}`, "memory_save"}, // op match is case-insensitive
		{"memory", `{"op":" search ","query":"q"}`, "memory_search"},
		{"docs", `{"op":"search","query":"mcp"}`, "docs_search"},
		{"ci_pipeline", `{"op":"validate","yaml":"jobs: []"}`, "ci_pipeline_validate"},
	}
	for _, tc := range cases {
		got, err := DispatchCanonical(tc.root, []byte(tc.args))
		if err != nil {
			t.Fatalf("DispatchCanonical(%s, %s): %v", tc.root, tc.args, err)
		}
		if got != tc.want {
			t.Fatalf("DispatchCanonical(%s, %s) = %s, want %s", tc.root, tc.args, got, tc.want)
		}
	}
}

func TestDispatchCanonicalRejectsBadOp(t *testing.T) {
	for _, args := range []string{`{}`, `{"op":"fetch"}`, `not-json`} {
		_, err := DispatchCanonical("memory", []byte(args))
		if err == nil {
			t.Fatalf("expected error for args %s", args)
		}
		if !strings.Contains(err.Error(), "op") || !strings.Contains(err.Error(), "save") {
			t.Fatalf("error should be self-describing (mention op + valid ops), got: %v", err)
		}
	}
	if _, err := DispatchCanonical("exec", []byte(`{}`)); err == nil {
		t.Fatal("non-family name must not canonicalize")
	}
}

func TestIsDispatchRoot(t *testing.T) {
	for _, root := range []string{"skill", "memory", "docs", "ci_pipeline"} {
		if !IsDispatchRoot(root) {
			t.Fatalf("%q should be a dispatch root", root)
		}
	}
	for _, other := range []string{"memory_save", "exec", "artifact_create", "skills", "Memory", ""} {
		if IsDispatchRoot(other) {
			t.Fatalf("%q must not be a dispatch root", other)
		}
	}
}

func TestCompactFamilies(t *testing.T) {
	defs := []ToolInfo{
		{Name: "skill_list"}, {Name: "skill_search"}, {Name: "skill_read"}, {Name: "skill_files"}, {Name: "skill_save"},
		{Name: "todo"},
		{Name: "memory_save"}, {Name: "memory_replace"}, {Name: "memory_search"}, {Name: "memory_list"}, {Name: "memory_delete"},
		{Name: "docs_search"}, {Name: "docs_read"},
		{Name: "artifact_create"},
		{Name: "ci_pipeline_list"}, {Name: "ci_pipeline_read"}, {Name: "ci_pipeline_validate"},
		{Name: "ci_run"},
	}
	got := CompactFamilies(defs)
	names := make([]string, 0, len(got))
	for _, d := range got {
		names = append(names, d.Name)
	}
	wantOrder := []string{"skill", "todo", "memory", "docs", "artifact_create", "ci_pipeline", "ci_run"}
	if len(got) != len(wantOrder) {
		t.Fatalf("compacted roster = %v, want %v", names, wantOrder)
	}
	for i, want := range wantOrder {
		if names[i] != want {
			t.Fatalf("roster position %d = %s, want %s (full: %v)", i, names[i], want, names)
		}
	}
}

func TestCompactFamiliesSchemaRequiresOp(t *testing.T) {
	got := CompactFamilies([]ToolInfo{{Name: "docs_read"}, {Name: "todo"}})
	if len(got) != 2 || got[0].Name != "docs" || got[1].Name != "todo" {
		t.Fatalf("unexpected compaction result %+v", got)
	}
	req, ok := got[0].InputSchema["required"].([]string)
	if !ok || len(req) != 1 || req[0] != "op" {
		t.Fatalf("dispatcher schema must require exactly [op], got %#v", got[0].InputSchema["required"])
	}
	if _, hasProps := got[0].InputSchema["properties"]; !hasProps {
		t.Fatal("dispatcher schema must carry properties")
	}
}

func TestCanonicalizeToolCalls(t *testing.T) {
	tcs := []domain.ToolCall{
		{ID: "1", Name: "memory", Args: `{"op":"save","content":"hi"}`},
		{ID: "2", Name: "memory", Args: `{}`},                   // no op → untouched, fails loud later
		{ID: "3", Name: "memory_save", Args: `{"content":"x"}`}, // retired direct call: name untouched, flagged for rejection
		{ID: "4", Name: "skill", Args: `{"op":"read","name":"x"}`},
		{ID: "5", Name: "ci_pipeline", Args: `{"op":"list"}`},
	}
	CanonicalizeToolCalls(tcs)
	wantNames := []string{"memory_save", "memory", "memory_save", "skill_read", "ci_pipeline_list"}
	for i, want := range wantNames {
		if tcs[i].Name != want {
			t.Fatalf("tc[%d] = %s, want %s", i, tcs[i].Name, want)
		}
	}
	// Provenance flags: only the retired direct per-op emission (tc 3) is
	// flagged for loud rejection at execution.
	wantFlags := []bool{false, false, true, false, false}
	for i, want := range wantFlags {
		if tcs[i].LegacyAlias != want {
			t.Fatalf("tc[%d] LegacyAlias = %v, want %v", i, tcs[i].LegacyAlias, want)
		}
	}
	if tcs[0].Args != `{"op":"save","content":"hi"}` {
		t.Fatalf("args must be preserved verbatim, got %s", tcs[0].Args)
	}
}

func TestLegacyAliasErrorTeachesDispatcherForm(t *testing.T) {
	err := LegacyAliasError("memory_save")
	if err == nil {
		t.Fatal("member name must be rejected at the model boundary")
	}
	msg := err.Error()
	for _, want := range []string{`"memory_save"`, `"memory"`, `"save"`} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q must mention %s", msg, want)
		}
	}
	for _, pass := range []string{"exec", "memory", "skill", "ci_pipeline", "file_read"} {
		if LegacyAliasError(pass) != nil {
			t.Fatalf("%q is not a per-op member and must pass through", pass)
		}
	}
}

// Canonicalization is the provenance chokepoint: dispatcher-root emissions
// are rewritten to per-op names and stay unflagged, while direct per-op
// emissions (retired hidden aliases) keep their name and get flagged for
// loud rejection at execution.
func TestCanonicalizeFlagsLegacyMemberEmissions(t *testing.T) {
	calls := []domain.ToolCall{
		{ID: "a", Name: "docs", Args: `{"op":"search","query":"mcp"}`},
		{ID: "b", Name: "memory_save", Args: `{"content":"x"}`},
		{ID: "c", Name: "file_read", Args: `{}`},
	}
	CanonicalizeToolCalls(calls)
	if calls[0].Name != "docs_search" || calls[0].LegacyAlias {
		t.Fatalf("root call must canonicalize without the flag: %+v", calls[0])
	}
	if calls[1].Name != "memory_save" || !calls[1].LegacyAlias {
		t.Fatalf("member emission must keep its name and be flagged: %+v", calls[1])
	}
	if calls[2].LegacyAlias {
		t.Fatal("non-family tools must pass through unflagged")
	}
}

// The hidden-alias path is removed: a model tool call that emits a legacy
// per-op name directly (flagged by CanonicalizeToolCalls) must fail loud
// with the dispatcher rewrite instead of executing.
func TestRunOneToolRejectsLegacyPerOpNames(t *testing.T) {
	app := &App{Logs: &fakeLogStore{}, Bus: NewBus()}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: context.Background()}
	tc := domain.ToolCall{ID: "tc1", Name: "memory_save", Args: `{"content":"x"}`, LegacyAlias: true}
	res := app.runOneTool(run, tc, ModelCapabilities{}, domain.Settings{})
	if res.status != domain.ToolFailed {
		t.Fatalf("status = %v, want ToolFailed", res.status)
	}
	if !strings.Contains(res.output, `"memory"`) || !strings.Contains(res.output, `"save"`) {
		t.Fatalf("output must teach the dispatcher rewrite, got %q", res.output)
	}
}
