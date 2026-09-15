package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSubagentCompletionResultNormalText(t *testing.T) {
	run := &AcpRun{
		TaskState: TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		Workspace: "/tmp/proj",
		Transcript: []AcpTranscriptChunk{
			{Kind: "text", Text: "I fixed the bug by updating the handler."},
			{Kind: "text", Text: "All tests pass."},
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
	// Only the LAST text chunk should appear — not intermediate progress.
	if !strings.Contains(got, "All tests pass.") {
		t.Fatalf("expected last text chunk in body, got %q", got)
	}
	if strings.Contains(got, "I fixed the bug by updating the handler.") {
		t.Fatalf("intermediate text must not leak into summary, got %q", got)
	}
}

func TestSubagentCompletionResultFailedWithText(t *testing.T) {
	run := &AcpRun{
		TaskState: TaskState[AcpRunStatus]{Status: AcpRunFailed, Error: "connection refused"},
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
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunFailed, Error: "timeout"},
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
	// Last thought should appear as fallback when no text chunk exists.
	if !strings.Contains(got, "thinking...") {
		t.Fatalf("expected last thought as fallback, got %q", got)
	}
}

func TestSubagentCompletionResultToolOnly(t *testing.T) {
	run := &AcpRun{
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
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
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		StopReason: "end_turn",
	}
	got := SubagentCompletionResult(run, "")
	if !strings.Contains(got, "no text output") {
		t.Fatalf("expected no-text-output fallback, got %q", got)
	}
}

func TestSubagentCompletionResultThinkingOnly(t *testing.T) {
	run := &AcpRun{
		TaskState: TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		Transcript: []AcpTranscriptChunk{
			{Kind: "thought", Text: "I should consider the edge cases..."},
		},
	}
	got := SubagentCompletionResult(run, "")
	// Last thought should appear as fallback when no text chunk exists.
	if !strings.Contains(got, "I should consider the edge cases...") {
		t.Fatalf("expected last thought as fallback, got %q", got)
	}
}

func TestSubagentCompletionResultCancelledWithText(t *testing.T) {
	run := &AcpRun{
		TaskState: TaskState[AcpRunStatus]{Status: AcpRunCancelled},
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
	run := &AcpRun{TaskState: TaskState[AcpRunStatus]{Status: AcpRunCancelled}}
	got := SubagentCompletionResult(run, "")
	if !strings.Contains(got, "status: cancelled") {
		t.Fatalf("expected status: cancelled, got %q", got)
	}
	if !strings.Contains(got, "cancelled") {
		t.Fatalf("expected cancellation indicator, got %q", got)
	}
}

// TestSubagentCompletionResultLastTextOnly verifies that when a run has
// multiple text chunks (intermediate progress + final summary), only the
// LAST text chunk appears in the body — the full transcript stays in the
// persisted JSON output file.
func TestSubagentCompletionResultLastTextOnly(t *testing.T) {
	run := &AcpRun{
		TaskState: TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		Transcript: []AcpTranscriptChunk{
			{Kind: "text", Text: "Starting the refactor."},
			{Kind: "tool", ToolTitle: "edit_file", ToolStatus: "completed"},
			{Kind: "text", Text: "Halfway done, updating tests."},
			{Kind: "tool", ToolTitle: "run_tests", ToolStatus: "completed"},
			{Kind: "text", Text: "Refactor complete. All 42 tests pass."},
		},
	}
	got := SubagentCompletionResult(run, "/data/run.json")
	if !strings.Contains(got, "Refactor complete. All 42 tests pass.") {
		t.Fatalf("expected last text chunk in body, got %q", got)
	}
	if strings.Contains(got, "Starting the refactor.") {
		t.Fatalf("first text chunk must not leak, got %q", got)
	}
	if strings.Contains(got, "Halfway done, updating tests.") {
		t.Fatalf("intermediate text chunk must not leak, got %q", got)
	}
}

// TestSubagentCompletionResultBoundedText verifies the bounded-cap contract:
// a normal technical report (5000 chars) is delivered in full without an
// omission marker, while output above the cap is bounded with the marker.
// The cap is rune-based so multibyte UTF-8 is never split mid-character.
func TestSubagentCompletionResultBoundedText(t *testing.T) {
	// 5000-char final text — below the cap, must be delivered without "…".
	fiveK := strings.Repeat("x", 5000)
	run := &AcpRun{
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		Transcript: []AcpTranscriptChunk{{Kind: "text", Text: fiveK}},
	}
	got := SubagentCompletionResult(run, "")
	bodyStart := strings.Index(got, "---\n\n")
	if bodyStart < 0 {
		t.Fatalf("expected body separator, got %q", got[:50])
	}
	body := got[bodyStart+5:]
	if len(body) != 5000 {
		t.Fatalf("5000-char text must be delivered in full (no truncation), got %d chars", len(body))
	}
	if strings.Contains(body, "…") {
		t.Fatalf("5000-char text must not carry an omission marker, got %q", body[:min(40, len(body))])
	}
}

// extractBody pulls the markdown body out of a SubagentCompletionResult
// YAML-frontmatter payload, or fatals when the separator is missing.
func extractBody(t *testing.T, got string) string {
	t.Helper()
	bodyStart := strings.Index(got, "---\n\n")
	if bodyStart < 0 {
		t.Fatalf("expected body separator, got %q", got[:min(50, len(got))])
	}
	return got[bodyStart+5:]
}

// TestSubagentCompletionResultBondsAboveCap verifies that text above the cap
// is bounded with the omission marker and remains valid UTF-8 (rune-safe
// truncation, never a mid-character byte slice).
func TestSubagentCompletionResultBondsAboveCap(t *testing.T) {
	overCap := strings.Repeat("x", MaxSubagentResultRunes+4000)
	run := &AcpRun{
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		Transcript: []AcpTranscriptChunk{{Kind: "text", Text: overCap}},
	}
	body := extractBody(t, SubagentCompletionResult(run, ""))
	runeCount := utf8.RuneCountInString(body)
	// Bounded: larger than the old 4000 cap, within the new cap + marker.
	if runeCount <= 4001 {
		t.Fatalf("above-cap text must use the larger bound, got %d runes (old 4000 cap)", runeCount)
	}
	if runeCount > MaxSubagentResultRunes+1 {
		t.Fatalf("above-cap text must be bounded to %d runes + marker, got %d runes", MaxSubagentResultRunes, runeCount)
	}
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("above-cap text must carry the omission marker, got %q", body[:min(40, len(body))])
	}
}

// TestSubagentCompletionResultUTF8SafeTruncation verifies rune-based
// truncation with multibyte characters: byte slicing would split a 3-byte
// "€" mid-character and produce invalid UTF-8.
func TestSubagentCompletionResultUTF8SafeTruncation(t *testing.T) {
	// "€" is 3 bytes; 20000 runes = 60000 bytes, well above the cap.
	overCap := strings.Repeat("€", MaxSubagentResultRunes+4000)
	run := &AcpRun{
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		Transcript: []AcpTranscriptChunk{{Kind: "text", Text: overCap}},
	}
	body := extractBody(t, SubagentCompletionResult(run, ""))
	if !utf8.ValidString(body) {
		t.Fatalf("truncated body must be valid UTF-8 (rune-safe), got invalid bytes")
	}
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("above-cap multibyte text must carry the omission marker")
	}
}

// TestSubagentCompletionResultFailedLongTextPreservesErrorSuffix verifies
// that a failed run with long text keeps the diagnostic error suffix within
// the bounded result.
func TestSubagentCompletionResultFailedLongTextPreservesErrorSuffix(t *testing.T) {
	longText := strings.Repeat("x", MaxSubagentResultRunes+2000)
	run := &AcpRun{
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunFailed, Error: "connection refused"},
		Transcript: []AcpTranscriptChunk{{Kind: "text", Text: longText}},
	}
	body := extractBody(t, SubagentCompletionResult(run, ""))
	if !strings.Contains(body, "[Subagent failed: connection refused]") {
		t.Fatalf("failed+text must preserve the error suffix, got %q", body[:min(80, len(body))])
	}
	// The combined result stays bounded (text budget + suffix).
	if len(body) > MaxSubagentResultRunes+200 {
		t.Fatalf("failed+text result must stay bounded, got %d", len(body))
	}
}

// TestSubagentCompletionResultCancelledLongTextPreservesSuffix verifies
// that a cancelled run with long text keeps the cancellation suffix and
// stays bounded.
func TestSubagentCompletionResultCancelledLongTextPreservesSuffix(t *testing.T) {
	longText := strings.Repeat("x", MaxSubagentResultRunes+2000)
	run := &AcpRun{
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCancelled},
		Transcript: []AcpTranscriptChunk{{Kind: "text", Text: longText}},
	}
	body := extractBody(t, SubagentCompletionResult(run, ""))
	if !strings.Contains(body, "[Subagent was cancelled.]") {
		t.Fatalf("cancelled+text must preserve the cancellation suffix, got %q", body[:min(80, len(body))])
	}
	if len(body) > MaxSubagentResultRunes+200 {
		t.Fatalf("cancelled+text result must stay bounded, got %d", len(body))
	}
}

// TestSubagentCompletionResultThoughtFallbackBounded verifies the
// thought-only fallback path uses the same rune-safe bound.
func TestSubagentCompletionResultThoughtFallbackBounded(t *testing.T) {
	longThought := strings.Repeat("y", MaxSubagentResultRunes+2000)
	run := &AcpRun{
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		Transcript: []AcpTranscriptChunk{{Kind: "thought", Text: longThought}},
	}
	body := extractBody(t, SubagentCompletionResult(run, ""))
	runeCount := utf8.RuneCountInString(body)
	if runeCount <= 4001 {
		t.Fatalf("thought fallback must use the larger bound, got %d runes (old 4000 cap)", runeCount)
	}
	if runeCount > MaxSubagentResultRunes+1 {
		t.Fatalf("thought fallback must be bounded to %d runes + marker, got %d runes", MaxSubagentResultRunes, runeCount)
	}
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("above-cap thought must carry the omission marker")
	}
}

// TestTranscriptSummaryBounded verifies TranscriptSummary (used by
// FormatSpawnResult for a run that finishes before the spawn response)
// applies the same rune-safe bound.
func TestTranscriptSummaryBounded(t *testing.T) {
	t.Run("below cap delivered in full", func(t *testing.T) {
		fiveK := strings.Repeat("x", 5000)
		run := &AcpRun{
			TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
			Transcript: []AcpTranscriptChunk{{Kind: "text", Text: fiveK}},
		}
		got := TranscriptSummary(run)
		if len(got) != 5000 {
			t.Fatalf("5000-char transcript must be delivered in full, got %d", len(got))
		}
		if strings.Contains(got, "…") {
			t.Fatalf("below-cap transcript must not carry an omission marker")
		}
	})
	t.Run("above cap bounded with marker", func(t *testing.T) {
		overCap := strings.Repeat("x", MaxSubagentResultRunes+4000)
		run := &AcpRun{
			TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
			Transcript: []AcpTranscriptChunk{{Kind: "text", Text: overCap}},
		}
		got := TranscriptSummary(run)
		runeCount := utf8.RuneCountInString(got)
		if runeCount <= 4001 {
			t.Fatalf("above-cap transcript must use the larger bound, got %d runes (old 4000 cap)", runeCount)
		}
		if runeCount > MaxSubagentResultRunes+1 {
			t.Fatalf("above-cap transcript must be bounded to %d runes + marker, got %d runes", MaxSubagentResultRunes, runeCount)
		}
		if !strings.HasSuffix(got, "…") {
			t.Fatalf("above-cap transcript must carry the omission marker")
		}
	})
}

// TestFormatSpawnResultUsesBoundedSummary verifies FormatSpawnResult bounds
// the per-run summary with the same cap when a run already finished.
func TestFormatSpawnResultUsesBoundedSummary(t *testing.T) {
	overCap := strings.Repeat("x", MaxSubagentResultRunes+4000)
	run := &AcpRun{
		TaskState:  TaskState[AcpRunStatus]{Status: AcpRunCompleted},
		Transcript: []AcpTranscriptChunk{{Kind: "text", Text: overCap}},
	}
	got := FormatSpawnResult([]AcpSpawned{{Run: run}})
	// The summary field must be bounded + marker, not the old 4000 cap.
	if !strings.Contains(got, "summary:") {
		t.Fatalf("expected a summary field for a finished run, got %q", got[:min(80, len(got))])
	}
	// Extract the summary value (YAML-quoted or plain) and check it is bounded.
	// The summary is a single YAML scalar; find "summary:" and read to the
	// next YAML key or end of the runs entry.
	summaryStart := strings.Index(got, "summary:")
	if summaryStart < 0 {
		t.Fatal("missing summary field")
	}
	rest := got[summaryStart+len("summary:"):]
	// The summary ends at the next "\n" followed by a YAML key or the
	// closing "---". For a single-line bounded summary it is one line.
	endLine := strings.IndexByte(rest, '\n')
	if endLine < 0 {
		endLine = len(rest)
	}
	summary := strings.TrimSpace(rest[:endLine])
	// YAML may quote the value; strip surrounding quotes for length check.
	summary = strings.Trim(summary, "\"")
	runeCount := utf8.RuneCountInString(summary)
	if runeCount <= 4001 {
		t.Fatalf("FormatSpawnResult summary must use the larger bound, got %d runes (old 4000 cap)", runeCount)
	}
	if runeCount > MaxSubagentResultRunes+1 {
		t.Fatalf("FormatSpawnResult summary must be bounded to %d runes + marker, got %d runes", MaxSubagentResultRunes, runeCount)
	}
}

func TestSubagentCompletionResultYAMLHeaderFormat(t *testing.T) {
	run := &AcpRun{
		TaskState: TaskState[AcpRunStatus]{Status: AcpRunCompleted},
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

func TestSubagentSteerResultOmitsTranscriptAndPrompt(t *testing.T) {
	run := &AcpRun{
		TaskState: TaskState[AcpRunStatus]{ID: "acprun_steer", Status: AcpRunRunning},
		Workspace: "/tmp/proj",
		Prompt:    "private parent prompt that must not leak",
		Transcript: []AcpTranscriptChunk{
			{Kind: "text", Text: "Intermediate progress."},
			{Kind: "thought", Text: "long private reasoning"},
			{Kind: "text", Text: "Last meaningful turn."},
		},
	}
	got := SubagentSteerResult(run)
	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("expected YAML frontmatter, got %q", got[:min(20, len(got))])
	}
	for _, want := range []string{
		"status: running",
		"id: acprun_steer",
		"workspace: /tmp/proj",
		"Steer accepted.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("steer result missing %q:\n%s", want, got)
		}
	}
	for _, leaked := range []string{
		"transcript:",
		run.Prompt,
		"Intermediate progress.",
		"long private reasoning",
		"Last meaningful turn.",
		"output_path:",
	} {
		if strings.Contains(got, leaked) {
			t.Errorf("steer result leaked %q:\n%s", leaked, got)
		}
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
