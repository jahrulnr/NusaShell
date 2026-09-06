package domain

import "testing"

func messageWithToolCalls(id string, calls ...ToolCall) Message {
	return Message{ID: id, Role: RoleAssistant, ToolCalls: calls}
}

func TestExtractExperienceSkipsHarnessCalls(t *testing.T) {
	conv := &Conversation{
		Messages: []Message{
			{ID: "m1", Role: RoleUser, Content: "petakan tools ask_question"},
			messageWithToolCalls("m2",
				ToolCall{ID: "hydrate-ab", Name: "runtime_context", Args: "{}"},
				ToolCall{ID: "hydrate-cd", Name: "mcp_list", Args: "{}"},
				ToolCall{ID: "hydrate-ef", Name: "tool_list", Args: "{}"},
				// Old transcripts store hydration without the hydrate- id
				// prefix; the name list must still exclude them.
				ToolCall{ID: "plain-1", Name: "runtime_context", Args: "{}"},
			),
			messageWithToolCalls("m3",
				ToolCall{ID: "ann-1", Name: "announcement", Args: "{}"},
				ToolCall{ID: "real-1", Name: "docs", Args: `{"op":"search"}`},
				ToolCall{ID: "real-2", Name: "file_read", Args: `{"path":"x"}`},
			),
		},
	}
	exp := ExtractExperience(conv, false)
	if len(exp.Actions) != 2 {
		t.Fatalf("actions = %d, want 2 (docs + file_read only): %+v", len(exp.Actions), exp.Actions)
	}
	if exp.Actions[0].Name != "docs" || exp.Actions[1].Name != "file_read" {
		t.Fatalf("actions = %+v", exp.Actions)
	}
	fp := exp.Signals.ProcedureFingerprint
	if fp != "docs>file_read" {
		t.Fatalf("fingerprint = %q, want docs>file_read", fp)
	}
	if exp.Signals.FailureSignature != "" {
		t.Fatalf("harness calls must never become the failure signature, got %q", exp.Signals.FailureSignature)
	}
}

func TestExtractExperienceCapBeyond40(t *testing.T) {
	conv := &Conversation{}
	var calls []ToolCall
	for i := 0; i < 150; i++ {
		calls = append(calls, ToolCall{ID: "tc_", Name: "exec", Args: "{}"})
	}
	conv.Messages = []Message{
		{ID: "m1", Role: RoleUser, Content: "do the thing"},
		messageWithToolCalls("m2", calls...),
	}
	exp := ExtractExperience(conv, false)
	if len(exp.Actions) < 100 {
		t.Fatalf("actions = %d, want the raised cap (>=100) to retain long turns", len(exp.Actions))
	}
	if len(exp.Actions) > maxActionSteps {
		t.Fatalf("actions = %d, over cap %d", len(exp.Actions), maxActionSteps)
	}
}

func TestCountUnreviewedLearningProgressSkipsHarnessRounds(t *testing.T) {
	conv := &Conversation{
		Messages: []Message{
			messageWithToolCalls("m1",
				ToolCall{ID: "hydrate-a", Name: "runtime_context"},
				ToolCall{ID: "hydrate-b", Name: "skill", Args: `{"op":"list"}`},
			),
			messageWithToolCalls("m2", ToolCall{ID: "ann", Name: "announcement"}),
			{ID: "m3", Role: RoleUser, Content: "continue"},
			messageWithToolCalls("m4", ToolCall{ID: "real", Name: "exec", Args: "{}"}),
		},
	}
	userTurns, toolIters := CountUnreviewedLearningProgress(conv.Messages, 0)
	if userTurns != 1 {
		t.Fatalf("userTurns = %d, want 1", userTurns)
	}
	if toolIters != 1 {
		t.Fatalf("toolIters = %d, want 1 (only the exec round counts)", toolIters)
	}
}

func TestBuildApplyBlockRanksAndDedupes(t *testing.T) {
	records := []*MemoryRecord{
		{Type: MemoryTypeFact, Body: "fact one here", Status: MemoryStatusLearned, EvidenceCount: 1},
		{Type: MemoryTypePreference, Body: "prefer SSE over WS for one-way streams", Status: MemoryStatusLearned, EvidenceCount: 2},
		{Type: MemoryTypeConstraint, Body: "never break wire contracts silently", Status: MemoryStatusLearned, EvidenceCount: 3},
		// Near-duplicate of the preference above (punctuation variant).
		{Type: MemoryTypePreference, Body: "prefer SSE over WS, for one-way streams!", Status: MemoryStatusLearned, EvidenceCount: 1},
	}
	got := BuildApplyBlock(records, 400)
	for _, want := range []string{"never break wire contracts silently", "prefer SSE over WS"} {
		if !containsAll(got, want) {
			t.Fatalf("APPLY block missing %q: %s", want, got)
		}
	}
	// The near-duplicate preference must collapse to one line.
	if n := countOccurrences(got, "prefer SSE over WS"); n != 1 {
		t.Fatalf("duplicate preference lines = %d, want 1: %s", n, got)
	}
	// Constraint (rank 0) must appear before the lower-ranked records.
	cIdx := indexOf(got, "never break wire contracts silently")
	pIdx := indexOf(got, "prefer SSE over WS")
	if cIdx > pIdx {
		t.Fatalf("constraint should rank before preference: %s", got)
	}
}

func TestBuildApplyBlockTrimsLongBodies(t *testing.T) {
	long := "This is a very long research note about provider credit and quota APIs that keeps going and going and going without ever stopping because the learner wrote a paragraph instead of a distilled entry and now the whole APPLY budget is one record"
	records := []*MemoryRecord{
		{Type: MemoryTypeFact, Body: long, Status: MemoryStatusLearned, EvidenceCount: 1},
		{Type: MemoryTypePreference, Body: "prefer Go for backend work", Status: MemoryStatusLearned, EvidenceCount: 1},
	}
	got := BuildApplyBlock(records, 400)
	if !containsAll(got, "prefer Go for backend work") {
		t.Fatalf("long record starved the block: %s", got)
	}
	if !containsAll(got, "…") {
		t.Fatalf("long body should be trimmed with ellipsis: %s", got)
	}
}

func countOccurrences(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}

func indexOf(s, sub string) int {
	return indexStr(s, sub)
}
