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

func TestToolFactoryLearningAgentsKeepConversationToolsPlusLearn(t *testing.T) {
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
		for _, want := range namesOf(conversation) {
			if !hasTool(got, want) {
				t.Fatalf("%s missing conversation tool %q in %v", kind, want, namesOf(got))
			}
		}
	}
	for _, want := range []string{
		"file_write", "file_patch", "file_mkdir", "file_delete", "file_move", "file_copy",
		"skill", "memory_project", "subagent", "delegate", "automation", "mcp_call",
	} {
		if !hasTool(conversation, want) {
			t.Fatalf("conversation fixture missing full-tool assertion %q in %v", want, namesOf(conversation))
		}
	}
	learningNoWS := f.Get(AgentMemoryConsolidator, "")
	conversationNoWS := f.Get(AgentConversation, "")
	if hasTool(conversationNoWS, "memory_project") || hasTool(learningNoWS, "memory_project") {
		t.Fatal("memory_project must stay hidden without a workspace")
	}
	if !hasTool(learningNoWS, LearnerResultToolName) {
		t.Fatalf("learner without workspace still needs %s", LearnerResultToolName)
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
