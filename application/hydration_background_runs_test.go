package application

import (
	"encoding/json"
	"strings"
	"testing"

	"nusashell/domain"
)

// TestHydrationIncludesPendingBackgroundRuns covers the async-tool edge case
// for hydration: after compaction (or at any hydration epoch), the runtime
// context slot must list the active background-agent runs (subagent,
// delegate) so the model knows which background tools were spawned and are
// still pending — correlatable with the later synthetic result calls by ID.
func TestHydrationIncludesPendingBackgroundRuns(t *testing.T) {
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {ID: "c1"}}},
		pendingRuns:   map[string]map[string]string{},
		delegateRuns:  map[string]*domain.AcpRun{},
	}
	// Two pending runs: an ACP subagent (details not tracked) and an
	// internal delegate (carries an AcpRun with worker detail).
	app.trackPendingRun("c1", "run-b", "subagent")
	app.trackPendingRun("c1", "run-a", "delegate")
	app.delegateRuns["run-a"] = &domain.AcpRun{
		AgentName:      "NusaShell delegate",
		CurrentModelID: "glm-5-2",
		Workspace:      "/ws",
	}

	conv := &domain.Conversation{ID: "c1", Workspace: "/ws"}
	msgs := app.buildHydration(conv)
	runtimeSlot := findHydrationTool(msgs, "runtime_context")
	if runtimeSlot == nil {
		t.Fatalf("runtime_context hydration slot missing; slots: %s", hydrationToolNames(msgs))
	}
	var ctx RuntimeContextSnapshot
	if err := json.Unmarshal([]byte(runtimeSlot.Content), &ctx); err != nil {
		t.Fatalf("runtime_context payload not JSON: %v", err)
	}
	if len(ctx.BackgroundRuns) != 2 {
		t.Fatalf("backgroundRuns = %d, want 2 (%s)", len(ctx.BackgroundRuns), runtimeSlot.Content)
	}
	// Deterministic ID order.
	if ctx.BackgroundRuns[0].ID != "run-a" || ctx.BackgroundRuns[1].ID != "run-b" {
		t.Fatalf("backgroundRuns not ID-sorted: %+v", ctx.BackgroundRuns)
	}
	if ctx.BackgroundRuns[0].Tool != "delegate" || ctx.BackgroundRuns[0].Model != "glm-5-2" {
		t.Fatalf("delegate details missing: %+v", ctx.BackgroundRuns[0])
	}
	if ctx.BackgroundRuns[1].Tool != "subagent" {
		t.Fatalf("subagent tool missing: %+v", ctx.BackgroundRuns[1])
	}
}

// TestHydrationOmitsBackgroundRunsWhenNonePending: with no active runs the
// runtime context slot must not carry a backgroundRuns field at all.
func TestHydrationOmitsBackgroundRunsWhenNonePending(t *testing.T) {
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": {ID: "c1"}}},
		pendingRuns:   map[string]map[string]string{},
		delegateRuns:  map[string]*domain.AcpRun{},
	}
	msgs := app.buildHydration(&domain.Conversation{ID: "c1"})
	runtimeSlot := findHydrationTool(msgs, "runtime_context")
	if runtimeSlot == nil {
		t.Fatalf("runtime_context hydration slot missing")
	}
	if strings.Contains(runtimeSlot.Content, "backgroundRuns") {
		t.Fatalf("backgroundRuns present with no pending runs: %s", runtimeSlot.Content)
	}
}

// TestCompactionRehydrationKeepsPendingBackgroundRuns: persistCompactedConversation
// rebuilds hydration; the rebuilt runtime context must still list runs that
// are pending at compaction time, so a continuation agent knows the
// background agents are still out there.
func TestCompactionRehydrationKeepsPendingBackgroundRuns(t *testing.T) {
	conv := &domain.Conversation{ID: "c1", Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: strings.Repeat("q", 200), Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "ans", Status: domain.StatusDone},
	}}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		pendingRuns:   map[string]map[string]string{},
		delegateRuns:  map[string]*domain.AcpRun{},
	}
	app.trackPendingRun("c1", "run-x", "subagent")

	if err := app.persistCompactedConversation(conv, "summary", 64_000); err != nil {
		t.Fatalf("persistCompactedConversation: %v", err)
	}
	saved, err := store.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	var payload string
	for i := range saved.Messages {
		for j := range saved.Messages[i].ToolCalls {
			tc := &saved.Messages[i].ToolCalls[j]
			if tc.Name == "runtime_context" && tc.Output != "" {
				payload = tc.Output
			}
		}
	}
	if payload == "" {
		t.Fatalf("no runtime_context payload after compaction re-hydration")
	}
	var ctx RuntimeContextSnapshot
	if err := json.Unmarshal([]byte(payload), &ctx); err != nil {
		t.Fatalf("runtime_context payload not JSON: %v", err)
	}
	if len(ctx.BackgroundRuns) != 1 || ctx.BackgroundRuns[0].ID != "run-x" {
		t.Fatalf("backgroundRuns after compaction = %+v, want run-x", ctx.BackgroundRuns)
	}
}

// findHydrationTool returns the tool result of the first hydration slot
// matching name.
func findHydrationTool(msgs []ChatMessage, name string) *ToolResult {
	for _, m := range msgs {
		if m.Role != "tool" || m.ToolResult == nil {
			continue
		}
		if m.ToolResult.Name == name {
			return m.ToolResult
		}
	}
	return nil
}

// hydrationToolNames lists the tool names present in a hydration transcript
// (for failure messages).
func hydrationToolNames(msgs []ChatMessage) string {
	var names []string
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolResult != nil {
			names = append(names, m.ToolResult.Name)
		}
	}
	return strings.Join(names, ",")
}
