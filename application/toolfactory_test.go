package application

import (
	"context"
	"testing"
)

// factoryStubToolbox advertises a fixed tool set standing in for the real
// toolbox (which the factory only reads via ListTools).
type factoryStubToolbox struct{ tools []ToolInfo }

func (s *factoryStubToolbox) ListTools() []ToolInfo { return s.tools }

func (s *factoryStubToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	return "", nil
}

func factoryStubTools() []ToolInfo {
	return []ToolInfo{
		{Name: "file_read"},
		{Name: "file_write"},
		{Name: "exec"},
		{Name: "memory"},
		{Name: "skill"},
		{Name: "subagent"},
		{Name: "delegate"},
		{Name: "ci_run"},
	}
}

func namesOf(defs []ToolDef) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Name)
	}
	return out
}

func hasTool(defs []ToolDef, name string) bool {
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
	for _, want := range []string{"file_read", "exec", "subagent", "ci_run", "memory", "skill", "docs", "memory_project"} {
		if !hasTool(defs, want) {
			t.Fatalf("conversation agent missing %q in %v", want, namesOf(defs))
		}
	}
	// memory_project is workspace-gated.
	if hasTool(f.Get(AgentConversation, ""), "memory_project") {
		t.Fatal("memory_project must be omitted without a workspace")
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
	if !hasTool(defs, "ci_run") || !hasTool(defs, "exec") {
		t.Fatalf("automation agent must keep ci/exec tools, got %v", namesOf(defs))
	}
}

func TestToolFactoryReviewAgentWhitelistedWithLocalTools(t *testing.T) {
	f := &ToolFactory{
		Toolbox:     func() []ToolInfo { return factoryStubTools() },
		Dispatchers: FilterDispatcherToolInfos,
	}
	defs := f.Get(AgentReview, "")
	// Local tools always present.
	if !hasTool(defs, reviewTranscriptToolName) || !hasTool(defs, modelOverrideToolName) {
		t.Fatalf("review agent missing local tools, got %v", namesOf(defs))
	}
	// Whitelist: memory, skill, file_read only.
	for _, want := range []string{"memory", "skill", "file_read"} {
		if !hasTool(defs, want) {
			t.Fatalf("review agent missing whitelisted %q in %v", want, namesOf(defs))
		}
	}
	for _, banned := range []string{"exec", "file_write", "subagent", "ci_run", "memory_project", "docs"} {
		if hasTool(defs, banned) {
			t.Fatalf("review agent must not see %q, got %v", banned, namesOf(defs))
		}
	}
}

func TestToolFactoryCompactionAgentIsSummaryOnly(t *testing.T) {
	// The compaction agent works on a zero factory — it never touches
	// the toolbox or dispatchers.
	var f ToolFactory
	defs := f.Get(AgentCompaction, "")
	if len(defs) != 1 || defs[0].Name != compactionSummaryToolName {
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
	for _, want := range []string{"exec", "file_read", "ci_run", "memory"} {
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
	if defs := f.Get(AgentReview, ""); defs != nil {
		t.Fatalf("review agent without toolbox must get nil, got %v", namesOf(defs))
	}
	// Compaction still works without a toolbox.
	if defs := f.Get(AgentCompaction, ""); len(defs) != 1 {
		t.Fatalf("compaction agent must work without toolbox, got %v", namesOf(defs))
	}
}
