package domain

import (
	"strings"
	"testing"
)

func TestSubagentCompletionResultNormalText(t *testing.T) {
	run := &AcpRun{
		Status:    AcpRunCompleted,
		Workspace: "/tmp/proj",
		Transcript: []AcpTranscriptChunk{
			{Kind: "text", Text: "I fixed the bug by updating the handler."},
			{Kind: "text", Text: " All tests pass."},
		},
	}
	got := SubagentCompletionResult(run, "/data/acp_runs.jsonl")
	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("expected YAML frontmatter, got %q", got[:20])
	}
	if !strings.Contains(got, "status: completed") {
		t.Fatalf("expected status in header, got %q", got)
	}
	if !strings.Contains(got, "workspace: /tmp/proj") {
		t.Fatalf("expected workspace in header, got %q", got)
	}
	if !strings.Contains(got, "output_path: /data/acp_runs.jsonl") {
		t.Fatalf("expected output_path in header, got %q", got)
	}
	if !strings.Contains(got, "I fixed the bug by updating the handler. All tests pass.") {
		t.Fatalf("expected body text, got %q", got)
	}
}

func TestSubagentCompletionResultFailedWithText(t *testing.T) {
	run := &AcpRun{
		Status:    AcpRunFailed,
		Error:     "connection refused",
		Workspace: "/tmp/proj",
		Transcript: []AcpTranscriptChunk{
			{Kind: "text", Text: "I started the refactor."},
		},
	}
	got := SubagentCompletionResult(run, "")
	if !strings.Contains(got, "status: failed") {
		t.Fatalf("expected status: failed, got %q", got)
	}
	if !strings.Contains(got, "I started the refactor.") {
		t.Fatalf("expected partial text, got %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Fatalf("expected error reason, got %q", got)
	}
}

func TestSubagentCompletionResultFailedNoText(t *testing.T) {
	run := &AcpRun{
		Status:     AcpRunFailed,
		Error:      "timeout",
		StopReason: "max_tokens",
		Transcript: []AcpTranscriptChunk{{Kind: "thought", Text: "thinking..."}},
	}
	got := SubagentCompletionResult(run, "")
	if !strings.Contains(got, "status: failed") {
		t.Fatalf("expected status: failed, got %q", got)
	}
	if !strings.Contains(got, "timeout") {
		t.Fatalf("expected error reason, got %q", got)
	}
}

func TestSubagentCompletionResultToolOnly(t *testing.T) {
	run := &AcpRun{
		Status:     AcpRunCompleted,
		StopReason: "end_turn",
		Transcript: []AcpTranscriptChunk{
			{Kind: "tool", ToolTitle: "edit_file", ToolStatus: "completed"},
		},
	}
	got := SubagentCompletionResult(run, "")
	if !strings.Contains(got, "no text output") {
		t.Fatalf("expected no-text-output indicator, got %q", got)
	}
	if !strings.Contains(got, "edit_file") {
		t.Fatalf("expected last tool name in fallback, got %q", got)
	}
}

func TestSubagentCompletionResultEmpty(t *testing.T) {
	run := &AcpRun{
		Status:     AcpRunCompleted,
		StopReason: "end_turn",
	}
	got := SubagentCompletionResult(run, "")
	if !strings.Contains(got, "no text output") {
		t.Fatalf("expected no-text-output fallback, got %q", got)
	}
}

func TestSubagentCompletionResultThinkingOnly(t *testing.T) {
	run := &AcpRun{
		Status: AcpRunCompleted,
		Transcript: []AcpTranscriptChunk{
			{Kind: "thought", Text: "I should consider the edge cases..."},
		},
	}
	got := SubagentCompletionResult(run, "")
	if strings.Contains(got, "I should consider the edge cases") {
		t.Fatalf("thinking text must not leak into summary, got %q", got)
	}
	if !strings.Contains(got, "no text output") {
		t.Fatalf("expected no-text-output fallback, got %q", got)
	}
}

func TestSubagentCompletionResultCancelledWithText(t *testing.T) {
	run := &AcpRun{
		Status: AcpRunCancelled,
		Transcript: []AcpTranscriptChunk{
			{Kind: "text", Text: "Partial work done."},
		},
	}
	got := SubagentCompletionResult(run, "")
	if !strings.Contains(got, "status: cancelled") {
		t.Fatalf("expected status: cancelled, got %q", got)
	}
	if !strings.Contains(got, "Partial work done.") {
		t.Fatalf("expected partial text, got %q", got)
	}
	if !strings.Contains(got, "cancelled") {
		t.Fatalf("expected cancellation indicator, got %q", got)
	}
}

func TestSubagentCompletionResultCancelledNoText(t *testing.T) {
	run := &AcpRun{Status: AcpRunCancelled}
	got := SubagentCompletionResult(run, "")
	if !strings.Contains(got, "status: cancelled") {
		t.Fatalf("expected status: cancelled, got %q", got)
	}
	if !strings.Contains(got, "cancelled") {
		t.Fatalf("expected cancellation indicator, got %q", got)
	}
}

func TestSubagentCompletionResultTruncatesLongText(t *testing.T) {
	longText := strings.Repeat("x", 5000)
	run := &AcpRun{
		Status:     AcpRunCompleted,
		Transcript: []AcpTranscriptChunk{{Kind: "text", Text: longText}},
	}
	got := SubagentCompletionResult(run, "")
	bodyStart := strings.Index(got, "---\n\n")
	if bodyStart < 0 {
		t.Fatalf("expected body separator, got %q", got[:50])
	}
	body := got[bodyStart+5:]
	if len(body) > 4100 {
		t.Fatalf("expected truncation to ~4000 chars, got %d", len(body))
	}
}

func TestSubagentCompletionResultYAMLHeaderFormat(t *testing.T) {
	run := &AcpRun{
		Status:    AcpRunCompleted,
		Workspace: "/home/user/project",
	}
	got := SubagentCompletionResult(run, "/data/acp_runs.jsonl")
	// Must start with ---
	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("expected --- prefix, got %q", got[:10])
	}
	// Must have closing --- before body
	if !strings.Contains(got, "\n---\n\n") {
		t.Fatalf("expected closing --- separator, got %q", got)
	}
	// No output_path when empty
	got2 := SubagentCompletionResult(run, "")
	if strings.Contains(got2, "output_path:") {
		t.Fatalf("expected no output_path when empty, got %q", got2)
	}
}

func TestYamlScalarQuoting(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"", `""`},
		{"has: colon", `"has: colon"`},
		{`has"quote`, `"has\"quote"`},
		{"- leading dash", `"- leading dash"`},
	}
	for _, c := range cases {
		if got := YamlScalar(c.in); got != c.want {
			t.Errorf("YamlScalar(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
