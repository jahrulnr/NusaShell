package application

import (
	"strings"
	"testing"
)

func TestDispatchOpRoutesOps(t *testing.T) {
	cases := []struct {
		root string
		args string
		want string
	}{
		{"skill", `{"op":"list","limit":5}`, "list"},
		{"skill", `{"op":"save","name":"x","content":"y"}`, "save"},
		{"memory", `{"op":"save","content":"x"}`, "save"},
		{"memory", `{"op":"SAVE","content":"x"}`, "save"}, // op match is case-insensitive
		{"memory", `{"op":" search ","query":"q"}`, "search"},
		{"docs", `{"op":"search","query":"mcp"}`, "search"},
		{"ci_pipeline", `{"op":"validate","yaml":"jobs: []"}`, "validate"},
	}
	for _, tc := range cases {
		got, err := DispatchOp(tc.root, []byte(tc.args))
		if err != nil {
			t.Fatalf("DispatchOp(%s, %s): %v", tc.root, tc.args, err)
		}
		if got != tc.want {
			t.Fatalf("DispatchOp(%s, %s) = %s, want %s", tc.root, tc.args, got, tc.want)
		}
	}
}

func TestDispatchOpRejectsBadOp(t *testing.T) {
	for _, args := range []string{`{}`, `{"op":"fetch"}`, `not-json`} {
		_, err := DispatchOp("memory", []byte(args))
		if err == nil {
			t.Fatalf("expected error for args %s", args)
		}
		if !strings.Contains(err.Error(), "op") || !strings.Contains(err.Error(), "save") {
			t.Fatalf("error should be self-describing (mention op + valid ops), got: %v", err)
		}
	}
	if _, err := DispatchOp("exec", []byte(`{}`)); err == nil {
		t.Fatal("non-family name must not resolve")
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

func TestDispatcherToolInfosSchemaRequiresOp(t *testing.T) {
	got := DispatcherToolInfos()
	if len(got) != 4 {
		t.Fatalf("family defs = %d, want 4", len(got))
	}
	for _, def := range got {
		req, ok := def.InputSchema["required"].([]string)
		if !ok || len(req) != 1 || req[0] != "op" {
			t.Fatalf("%s schema must require exactly [op], got %#v", def.Name, def.InputSchema["required"])
		}
		if _, hasProps := def.InputSchema["properties"]; !hasProps {
			t.Fatalf("%s schema must carry properties", def.Name)
		}
	}
}

// OpArg is the silent extractor used by classification sites (review
// mutations, learning events): unknown/malformed payloads yield "".
func TestOpArg(t *testing.T) {
	cases := []struct {
		args string
		want string
	}{
		{`{"op":"save","content":"x"}`, "save"},
		{`{"op":" SEARCH "}`, "search"},
		{`{}`, ""},
		{`not-json`, ""},
	}
	for _, tc := range cases {
		if got := OpArg([]byte(tc.args)); got != tc.want {
			t.Fatalf("OpArg(%s) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// Single naming layer: a call named like a retired per-op verb is simply an
// unknown tool at the boundary — no alias machinery exists to translate it.
func TestRetiredPerOpNamesAreUnknownTools(t *testing.T) {
	for _, name := range []string{"memory_save", "skill_read", "docs_search"} {
		if IsDispatchRoot(name) {
			t.Fatalf("%q must not be treated as a dispatcher root", name)
		}
		if _, err := DispatchOp(name, []byte(`{}`)); err == nil {
			t.Fatalf("%q must fail DispatchOp loudly", name)
		}
	}
}
