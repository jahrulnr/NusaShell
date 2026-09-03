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
		{"skill", `{"op":"delete","id":"x"}`, "delete"},
		{"memory", `{"op":"save","content":"x"}`, "save"},
		{"memory", `{"op":"SAVE","content":"x"}`, "save"}, // op match is case-insensitive
		{"memory", `{"op":" search ","query":"q"}`, "search"},
		{"docs", `{"op":"list"}`, "list"},
		{"docs", `{"op":"search","query":"mcp"}`, "search"},
		{"memory_project", `{"op":"query","kind":"index"}`, "query"},
		{"memory_project", `{"op":"skip","reason":"nothing durable"}`, "skip"},
		{"automation", `{"op":"run","workflow_id":"nightly"}`, "run"},
		{"automation", `{"op":"STATUS","run_id":"run_1"}`, "status"},
		{"automation", `{"op":"delete","workflow_id":"nightly"}`, "delete"},
		{"automation_schedule", `{"op":"every","cron":"0 9 * * 1-5"}`, "every"},
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
	for _, root := range []string{"skill", "memory", "docs", "memory_project", "automation", "automation_schedule", "conversation"} {
		if !IsDispatchRoot(root) {
			t.Fatalf("%q should be a dispatch root", root)
		}
	}
	for _, other := range []string{"memory_save", "exec", "show", "skills", "Memory", ""} {
		if IsDispatchRoot(other) {
			t.Fatalf("%q must not be a dispatch root", other)
		}
	}
}

func TestDispatcherToolInfosSchemaRequiresOp(t *testing.T) {
	got := DispatcherToolInfos()
	if len(got) != 7 {
		t.Fatalf("family defs = %d, want 7", len(got))
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

func TestFilterDispatcherToolInfosHidesProjectMemoryWithoutWorkspace(t *testing.T) {
	hidden := FilterDispatcherToolInfos("")
	for _, info := range hidden {
		if info.Name == "memory_project" {
			t.Fatal("memory_project must be hidden without a workspace")
		}
	}
	shown := FilterDispatcherToolInfos("/apps/payments/api")
	found := false
	for _, info := range shown {
		if info.Name == "memory_project" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("memory_project must be advertised when a workspace is set")
	}
	if len(shown) != 7 {
		t.Fatalf("with workspace, family defs = %d, want 7", len(shown))
	}
}

func TestDocsDispatcherOpsAreExplicit(t *testing.T) {
	for _, op := range []string{"list", "search", "read"} {
		if _, err := DispatchOp("docs", []byte(`{"op":"`+op+`"}`)); err != nil {
			t.Fatalf("docs op %q rejected: %v", op, err)
		}
	}
	var docs ToolInfo
	for _, info := range DispatcherToolInfos() {
		if info.Name == "docs" {
			docs = info
			break
		}
	}
	properties, ok := docs.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("docs schema must expose properties")
	}
	opSchema, ok := properties["op"].(map[string]any)
	if !ok {
		t.Fatal("docs schema must expose the op property")
	}
	values, ok := opSchema["enum"].([]any)
	if !ok {
		t.Fatal("docs op schema must expose an enum")
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if op, ok := value.(string); ok {
			seen[op] = true
		}
	}
	for _, want := range []string{"list", "search", "read"} {
		if !seen[want] {
			t.Fatalf("docs op schema must advertise %q", want)
		}
	}
	if !strings.Contains(docs.Description, "read {id}") {
		t.Fatalf("docs description must advertise the read follow-up: %s", docs.Description)
	}
}

func TestMemoryDispatcherUsesCanonicalUserTier(t *testing.T) {
	var memory ToolInfo
	for _, info := range DispatcherToolInfos() {
		if info.Name == "memory" {
			memory = info
			break
		}
	}
	if strings.Contains(strings.ToLower(memory.Description), "primary") {
		t.Fatalf("memory description must use canonical user tier, got: %s", memory.Description)
	}
	properties, ok := memory.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("memory schema must expose properties")
	}
	target, ok := properties["target"].(map[string]any)
	if !ok {
		t.Fatal("memory schema must expose target")
	}
	values, ok := target["enum"].([]any)
	if !ok {
		t.Fatal("memory target schema must expose an enum")
	}
	for _, value := range values {
		if value == "primary" {
			t.Fatal("memory target schema must not advertise the removed primary alias")
		}
	}
}

func TestSkillDispatcherOpsAreExplicit(t *testing.T) {
	for _, op := range []string{"list", "search", "save", "delete"} {
		if _, err := DispatchOp("skill", []byte(`{"op":"`+op+`"}`)); err != nil {
			t.Fatalf("skill op %q rejected: %v", op, err)
		}
	}
	var skill ToolInfo
	for _, info := range DispatcherToolInfos() {
		if info.Name == "skill" {
			skill = info
			break
		}
	}
	if !strings.Contains(skill.Description, "file_read") || !strings.Contains(skill.Description, "MUST") {
		t.Fatalf("skill description must require file_read after discovery: %s", skill.Description)
	}
	properties, ok := skill.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("skill schema must expose properties")
	}
	opSchema, ok := properties["op"].(map[string]any)
	if !ok {
		t.Fatal("skill schema must expose the op property")
	}
	values, ok := opSchema["enum"].([]any)
	if !ok {
		t.Fatal("skill op schema must expose an enum")
	}
	for _, value := range values {
		if value == "delete" {
			return
		}
	}
	t.Fatal("skill op schema must advertise delete")
}

func TestAutomationDispatcherOpsAreExplicit(t *testing.T) {
	want := []string{"run", "wait", "status", "logs", "cancel", "steer", "list", "read", "validate", "create", "enable", "disable", "delete"}
	for _, op := range want {
		if _, err := DispatchOp("automation", []byte(`{"op":"`+op+`"}`)); err != nil {
			t.Fatalf("automation op %q rejected: %v", op, err)
		}
	}
	for _, op := range []string{"once", "every"} {
		if _, err := DispatchOp("automation_schedule", []byte(`{"op":"`+op+`"}`)); err != nil {
			t.Fatalf("automation_schedule op %q rejected: %v", op, err)
		}
	}
	if _, err := DispatchOp("automation", []byte(`{"op":"once"}`)); err == nil {
		t.Fatal("schedule operation must not leak into automation dispatcher")
	}

	var automation ToolInfo
	for _, info := range DispatcherToolInfos() {
		if info.Name == "automation" {
			automation = info
			break
		}
	}
	properties, ok := automation.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("automation schema must expose properties")
	}
	opSchema, ok := properties["op"].(map[string]any)
	if !ok {
		t.Fatal("automation schema must expose the op property")
	}
	values, ok := opSchema["enum"].([]any)
	if !ok {
		t.Fatal("automation op schema must expose an enum")
	}
	for _, value := range values {
		if value == "delete" {
			return
		}
	}
	t.Fatal("automation op schema must advertise delete")
}
