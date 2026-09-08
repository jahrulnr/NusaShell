package tools

import "testing"

func factoryStubTools() []ToolInfo {
	return []ToolInfo{
		{Name: "file_read"},
		{Name: "file_list"},
		{Name: "file_mkdir"},
		{Name: "grep"},
		{Name: "find_file"},
		{Name: "file_info"},
		{Name: "show"},
		{Name: "file_write"},
		{Name: "file_patch"},
		{Name: "file_delete"},
		{Name: "file_move"},
		{Name: "file_copy"},
		{Name: "exec"},
		{Name: "web_search"},
		{Name: "web_fetch"},
		{Name: "todo"},
		{Name: "ask_question"},
		{Name: "mcp_list"},
		{Name: "tool_list"},
		{Name: "tool_schema"},
		{Name: "mcp_search"},
		{Name: "mcp_call"},
		{Name: "memory"},
		{Name: "skill"},
		{Name: "subagent"},
		{Name: "subagent_steer"},
		{Name: "subagent_stop"},
		{Name: "subagent_wait"},
		{Name: "delegate"},
		{Name: "automation"},
	}
}

func namesOf(defs []ToolInfo) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func hasTool(defs []ToolInfo, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

func TestToolFactoryConversationAgent(t *testing.T) {
	f := &ToolFactory{
		Toolbox:     func() []ToolInfo { return factoryStubTools() },
		Dispatchers: FilterDispatcherToolInfos,
	}
	defs := f.Get(AgentConversation, "/ws")
	for _, want := range []string{"file_read", "exec", "subagent", "automation", "memory", "skill", "docs", "memory_project"} {
		if !hasTool(defs, want) {
			t.Fatalf("conversation agent missing %q in %v", want, namesOf(defs))
		}
	}
	// memory_project is workspace-gated.
	if hasTool(f.Get(AgentConversation, ""), "memory_project") {
		t.Fatal("memory_project must be omitted without a workspace")
	}
}

func TestToolFactoryDoesNotDuplicateDispatcherRoots(t *testing.T) {
	f := &ToolFactory{
		Toolbox: func() []ToolInfo {
			return []ToolInfo{
				{Name: "skill"},
				{Name: "memory"},
				{Name: "docs"},
				{Name: "memory_project"},
			}
		},
		Dispatchers: FilterDispatcherToolInfos,
	}
	defs := f.Get(AgentConversation, "/ws")
	counts := map[string]int{}
	for _, def := range defs {
		counts[def.Name]++
	}
	for _, name := range []string{"skill", "memory", "docs", "memory_project"} {
		if counts[name] != 1 {
			t.Fatalf("dispatcher root %q appears %d times in %v", name, counts[name], namesOf(defs))
		}
	}
	if hasTool(f.Get(AgentConversation, ""), "memory_project") {
		t.Fatal("memory_project must stay hidden without a workspace")
	}
}

func TestToolFactoryAutomationAgentOmitsACPTools(t *testing.T) {
	f := &ToolFactory{
		Toolbox:     func() []ToolInfo { return factoryStubTools() },
		Dispatchers: FilterDispatcherToolInfos,
	}
	defs := f.Get(AgentAutomation, "/ws")
	if hasTool(defs, "subagent") {
		t.Fatalf("automation agent must not see subagent, got %v", namesOf(defs))
	}
	if !hasTool(defs, "automation") || !hasTool(defs, "exec") {
		t.Fatalf("automation agent must keep automation/exec tools, got %v", namesOf(defs))
	}
}

func TestToolFactoryLearningAgentsPruneBannedToolsPlusLearn(t *testing.T) {
	f := &ToolFactory{
		Toolbox:     func() []ToolInfo { return factoryStubTools() },
		Dispatchers: FilterDispatcherToolInfos,
	}
	conversation := f.Get(AgentConversation, "/ws")
	if hasTool(conversation, LearnerResultToolName) {
		t.Fatalf("conversation agent must not advertise %s, got %v", LearnerResultToolName, namesOf(conversation))
	}
	for _, kind := range []AgentKind{
		AgentLearner,
		AgentMemoryConsolidator,
		AgentSkillEvolver,
		AgentSkillEvaluator,
	} {
		got := f.Get(kind, "/ws")
		if !hasTool(got, LearnerResultToolName) {
			t.Fatalf("%s missing %s in %v", kind, LearnerResultToolName, namesOf(got))
		}
		if !hasTool(got, "conversation") {
			t.Fatalf("%s missing conversation dispatcher in %v", kind, namesOf(got))
		}
		for _, banned := range []string{
			"memory_project", "subagent", "subagent_steer", "subagent_stop", "subagent_wait",
			"delegate", "mcp_list", "mcp_search", "mcp_call", "tool_list", "tool_schema",
		} {
			if hasTool(got, banned) {
				t.Fatalf("%s must not advertise banned tool %q, got %v", kind, banned, namesOf(got))
			}
		}
		for _, want := range []string{"file_read", "file_write", "file_patch", "skill", "memory", "docs", "exec", "automation"} {
			if !hasTool(got, want) {
				t.Fatalf("%s missing kept tool %q in %v", kind, want, namesOf(got))
			}
		}
	}
	learningNoWS := f.Get(AgentLearner, "")
	if hasTool(learningNoWS, "memory_project") {
		t.Fatal("memory_project must stay hidden without a workspace")
	}
	if !hasTool(learningNoWS, LearnerResultToolName) {
		t.Fatalf("learner without workspace still needs %s", LearnerResultToolName)
	}
}

func TestToolFactoryLearnerSkillDispatcherIsReadOnly(t *testing.T) {
	f := &ToolFactory{
		Toolbox:     func() []ToolInfo { return factoryStubTools() },
		Dispatchers: FilterDispatcherToolInfos,
	}
	var skill ToolInfo
	for _, def := range f.Get(AgentLearner, "/ws") {
		if def.Name == "skill" {
			skill = def
			break
		}
	}
	if skill.Name == "" {
		t.Fatal("learner must retain the skill dispatcher for read-only discovery")
	}
	properties, ok := skill.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("skill schema properties type = %T", skill.InputSchema["properties"])
	}
	op, ok := properties["op"].(map[string]any)
	if !ok {
		t.Fatalf("skill op schema type = %T", properties["op"])
	}
	enum, ok := op["enum"].([]any)
	if !ok {
		t.Fatalf("skill op enum type = %T", op["enum"])
	}
	seen := map[string]bool{}
	for _, value := range enum {
		if name, ok := value.(string); ok {
			seen[name] = true
		}
	}
	if !seen["list"] || !seen["search"] || seen["save"] || seen["delete"] {
		t.Fatalf("learner skill ops = %v, want list/search only", enum)
	}
}

func TestLearnerSkillMutationClassification(t *testing.T) {
	for _, tc := range []struct {
		args string
		want bool
	}{
		{args: `{"op":"save"}`, want: true},
		{args: `{"op":"delete"}`, want: true},
		{args: `{"op":"list"}`, want: false},
		{args: `{"op":"search"}`, want: false},
		{args: `{}`, want: false},
	} {
		if got := IsLearnerSkillMutation("skill", []byte(tc.args)); got != tc.want {
			t.Fatalf("IsLearnerSkillMutation(%s) = %v, want %v", tc.args, got, tc.want)
		}
	}
	if IsLearnerSkillMutation("memory", []byte(`{"op":"save"}`)) {
		t.Fatal("non-skill tools must not be classified as learner skill mutations")
	}
}

func TestLearnerResultToolAdvertisesMemoryScopeFields(t *testing.T) {
	consolidate, ok := LearnerResultTool.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("learner result properties type = %T", LearnerResultTool.InputSchema["properties"])
	}
	consolidateSchema, ok := consolidate["consolidate"].(map[string]any)
	if !ok {
		t.Fatalf("consolidate schema type = %T", consolidate["consolidate"])
	}
	entry, ok := consolidateSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("consolidate properties type = %T", consolidateSchema["properties"])
	}
	entrySchema, ok := entry["entry"].(map[string]any)
	if !ok {
		t.Fatalf("entry schema type = %T", entry["entry"])
	}
	entryProperties, ok := entrySchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("entry properties type = %T", entrySchema["properties"])
	}
	for _, field := range []string{"scope", "project"} {
		if _, ok := entryProperties[field]; !ok {
			t.Fatalf("learner entry schema missing %q", field)
		}
	}
}

func TestToolFactoryCompactionAgentIsSummaryOnly(t *testing.T) {
	// The compaction agent works on a zero factory — it never touches
	// the toolbox or dispatchers.
	var f ToolFactory
	defs := f.Get(AgentCompaction, "")
	if len(defs) != 1 || defs[0].Name != CompactionSummaryToolName {
		t.Fatalf("compaction agent must advertise exactly summary(), got %v", namesOf(defs))
	}
}

func TestToolFactoryDelegateAgentOmitsACPToolsAndDelegate(t *testing.T) {
	f := &ToolFactory{
		Toolbox:     func() []ToolInfo { return factoryStubTools() },
		Dispatchers: FilterDispatcherToolInfos,
	}
	defs := f.Get(AgentDelegate, "/ws")
	if hasTool(defs, "delegate") {
		t.Fatalf("delegate agent must not see the delegate tool (no recursion), got %v", namesOf(defs))
	}
	if hasTool(defs, "subagent") {
		t.Fatalf("delegate agent must not see ACP tools, got %v", namesOf(defs))
	}
	for _, want := range []string{"exec", "file_read", "automation", "memory"} {
		if !hasTool(defs, want) {
			t.Fatalf("delegate agent missing %q in %v", want, namesOf(defs))
		}
	}
}

func TestToolFactoryNilToolbox(t *testing.T) {
	f := &ToolFactory{Dispatchers: FilterDispatcherToolInfos}
	if defs := f.Get(AgentConversation, "/ws"); defs != nil {
		t.Fatalf("conversation agent without toolbox must get nil, got %v", namesOf(defs))
	}
	if defs := f.Get(AgentMemoryConsolidator, ""); defs != nil {
		t.Fatalf("memory consolidator without toolbox must get nil, got %v", namesOf(defs))
	}
	// Compaction still works without a toolbox.
	if defs := f.Get(AgentCompaction, ""); len(defs) != 1 {
		t.Fatalf("compaction agent must work without toolbox, got %v", namesOf(defs))
	}
}
