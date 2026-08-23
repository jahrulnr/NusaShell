package application

import (
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
		{ID: "2", Name: "memory", Args: `{}`},                 // no op → untouched, fails loud later
		{ID: "3", Name: "memory_save", Args: `{"content":"x"}`}, // legacy direct call untouched
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
	if tcs[0].Args != `{"op":"save","content":"hi"}` {
		t.Fatalf("args must be preserved verbatim, got %s", tcs[0].Args)
	}
}
