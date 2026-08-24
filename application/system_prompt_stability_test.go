package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

// TestSystemPromptCacheStable verifies the system prompt never carries
// runtime state: delegation config, continuation instructions, or async
// subagent results must travel as tool hydration/tool descriptions so the
// system prefix keeps its prompt-cache hits.
func TestSystemPromptCacheStable(t *testing.T) {
	c := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "hi"},
			{ID: "a1", Role: domain.RoleAssistant, Content: "hello"},
			{ID: "a2", Role: domain.RoleAssistant, Content: "partial", Status: domain.StatusDone},
		},
	}
	prompt := buildSystemPrompt(c, "user instructions")
	for _, forbidden := range []string{
		"Available ACP agents",
		"Subagent delegation",
		"interrupted by a transient upstream failure",
		"continue_stream",
		"subagent_result",
		"Backend restarted",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("system prompt must not contain runtime state %q", forbidden)
		}
	}
}

// TestAppendContinuationTool verifies the continuation instruction is
// delivered as an ephemeral synthetic tool call + result (announcement
// style), never as a system prompt mutation.
func TestAppendContinuationTool(t *testing.T) {
	base := []ChatMessage{{Role: "user", Content: "hi"}}
	msgs := appendContinuationTool(base)

	if len(msgs) != len(base)+2 {
		t.Fatalf("want 2 synthetic messages appended, got %d total", len(msgs))
	}
	asst := msgs[len(base)]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 {
		t.Fatalf("synthetic assistant message wrong: %+v", asst)
	}
	call := asst.ToolCalls[0]
	if call.Name != domain.ContinueStreamToolName {
		t.Fatalf("tool name = %q, want %q", call.Name, domain.ContinueStreamToolName)
	}
	if !domain.IsContinueStreamCallID(call.ID) {
		t.Fatalf("call id %q must use the cont- prefix", call.ID)
	}
	if call.Status != domain.ToolOK || call.Output != domain.ContinueStreamMessage {
		t.Fatalf("tool call must be pre-completed: %+v", call)
	}
	res := msgs[len(msgs)-1]
	if res.Role != "tool" || res.ToolResult == nil {
		t.Fatalf("last message must be the tool result: %+v", res)
	}
	if res.ToolResult.ToolCallID != call.ID || res.ToolResult.Content != domain.ContinueStreamMessage {
		t.Fatalf("tool result must reference the same call with the same content: %+v", res.ToolResult)
	}
}

// TestAcpDelegationDescription verifies the delegation guidance renders
// only when agents are enabled and stays out of the system prompt.
func TestAcpDelegationDescription(t *testing.T) {
	if got := AcpDelegationDescription(nil); got != "" {
		t.Fatalf("nil agents must produce empty description, got %q", got)
	}
	if got := AcpDelegationDescription([]*domain.AcpAgent{}); got != "" {
		t.Fatalf("no agents must produce empty description, got %q", got)
	}

	agents := []*domain.AcpAgent{
		{ID: "acp_1", Name: "Cursor", Enabled: true},
		{ID: "acp_2", Name: "Claude Code", Enabled: true},
	}
	desc := AcpDelegationDescription(agents)
	if !strings.Contains(desc, "Cursor (acp_1)") || !strings.Contains(desc, "Claude Code (acp_2)") {
		t.Fatalf("description must list enabled agents: %q", desc)
	}
	if !strings.Contains(desc, "Default ACP agent: Cursor") {
		t.Fatalf("description must name the default agent: %q", desc)
	}
}

// TestSubagentResultMessageShape verifies the synthetic subagent_result
// message mirrors the announcement pattern: pre-completed tool call with
// the full result, persisted as an assistant message.
func TestSubagentResultMessageShape(t *testing.T) {
	run := &domain.AcpRun{
		ID:               "run_abc",
		Status:           domain.AcpRunCompleted,
		ParentToolCallID: "call_parent",
		Transcript: []domain.AcpTranscriptChunk{
			{Kind: "text", Text: "work done"},
		},
	}
	app := &App{}
	msg := app.subagentResultMessage(run, "/data/run_abc.json", domain.ToolOK)

	if msg.Role != domain.RoleAssistant || msg.Status != domain.StatusDone {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != domain.SubagentResultToolName {
		t.Fatalf("tool name = %q, want %q", tc.Name, domain.SubagentResultToolName)
	}
	if !domain.IsSubagentResultCallID(tc.ID) {
		t.Fatalf("call id %q must use the subagent-result- prefix", tc.ID)
	}
	if tc.Args != domain.SubagentResultArgs(run.ID) {
		t.Fatalf("args must carry the run id: %q", tc.Args)
	}
	if tc.Status != domain.ToolOK {
		t.Fatalf("completed run must be ToolOK, got %v", tc.Status)
	}
	if !strings.Contains(tc.Output, "work done") || !strings.Contains(tc.Output, "output_path: /data/run_abc.json") {
		t.Fatalf("output must carry the full subagent result: %q", tc.Output)
	}
}

// TestCompleteSubagentRun verifies the atomic completion path: the
// original `subagent` tool call transitions to a brief terminal status
// while the full result is delivered via the synthetic subagent_result
// message, both in one conversation save.
func TestCompleteSubagentRun(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{
				ID: "m1", Role: domain.RoleUser, Content: "delegate", Status: domain.StatusDone,
			},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{
					ID: "call_parent", Name: "subagent", Status: domain.ToolRunning,
					Output: "---\nstatus: starting",
				}},
			},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus()}

	run := &domain.AcpRun{
		ID:               "run_xyz",
		Status:           domain.AcpRunCompleted,
		ParentToolCallID: "call_parent",
		Transcript:       []domain.AcpTranscriptChunk{{Kind: "text", Text: "done"}},
	}
	app.completeSubagentRun("c1", run.ParentToolCallID, domain.ToolOK, run, "/data/run_xyz.json")

	saved := store.convs["c1"]
	if len(saved.Messages) != 3 {
		t.Fatalf("want 3 messages after completion, got %d", len(saved.Messages))
	}
	original := saved.Messages[1].ToolCalls[0]
	if original.Status != domain.ToolOK {
		t.Fatalf("original tool call must be ToolOK, got %v", original.Status)
	}
	if strings.Contains(original.Output, "done") || !strings.Contains(original.Output, "subagent_result") {
		t.Fatalf("original tool call must carry only the brief pointer: %q", original.Output)
	}
	synthetic := saved.Messages[2]
	if synthetic.Role != domain.RoleAssistant || len(synthetic.ToolCalls) != 1 {
		t.Fatalf("synthetic message missing: %+v", synthetic)
	}
	stc := synthetic.ToolCalls[0]
	if stc.Name != domain.SubagentResultToolName || !domain.IsSubagentResultCallID(stc.ID) {
		t.Fatalf("synthetic tool call wrong: %+v", stc)
	}
	if !strings.Contains(stc.Output, "done") {
		t.Fatalf("synthetic tool call must carry the full result: %q", stc.Output)
	}
}

// TestCompleteSubagentRunFailedStatus verifies failed/cancelled runs flip
// the synthetic tool call to ToolFailed while still delivering the full
// result.
func TestCompleteSubagentRunFailedStatus(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			{ID: "m1", Role: domain.RoleUser, Content: "delegate", Status: domain.StatusDone},
			{
				ID: "m2", Role: domain.RoleAssistant, Status: domain.StatusDone,
				ToolCalls: []domain.ToolCall{{ID: "call_parent", Name: "subagent", Status: domain.ToolRunning}},
			},
		},
	}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{Conversations: store, Bus: NewBus()}

	run := &domain.AcpRun{
		ID:               "run_fail",
		Status:           domain.AcpRunFailed,
		ParentToolCallID: "call_parent",
		Error:            "boom",
	}
	app.completeSubagentRun("c1", run.ParentToolCallID, domain.ToolFailed, run, "")

	saved := store.convs["c1"]
	if saved.Messages[1].ToolCalls[0].Status != domain.ToolFailed {
		t.Fatalf("original tool call must be ToolFailed, got %v", saved.Messages[1].ToolCalls[0].Status)
	}
	stc := saved.Messages[2].ToolCalls[0]
	if stc.Status != domain.ToolFailed {
		t.Fatalf("synthetic tool call must be ToolFailed, got %v", stc.Status)
	}
	if !strings.Contains(stc.Output, "boom") {
		t.Fatalf("synthetic output must include the error: %q", stc.Output)
	}
}
