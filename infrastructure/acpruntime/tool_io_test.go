package acpruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/acpclient"
)

// The fake agent's TOOL_IO prompt emits a tool round with rawInput, status
// updates, and a completed update carrying a content array + rawOutput — the
// exact shape that used to be dropped client-side.
func TestSpawnCapturesToolInputAndOutput(t *testing.T) {
	rt := New()
	defer rt.Close()
	ws := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	run, err := rt.Spawn(ctx, application.AcpSpawnRequest{
		Agent: testAgent(ws), Prompt: "TOOL_IO read the skill", Workspace: ws,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := rt.Wait(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.AcpRunCompleted {
		t.Fatalf("status = %s error=%s", finished.Status, finished.Error)
	}
	var tool *domain.AcpTranscriptChunk
	for i := range finished.Transcript {
		if finished.Transcript[i].Kind == "tool" && finished.Transcript[i].ToolID == "call_read" {
			tool = &finished.Transcript[i]
		}
	}
	if tool == nil {
		t.Fatalf("tool chunk missing: %+v", finished.Transcript)
	}
	if tool.ToolTitle != "Read SKILL.md" || tool.ToolStatus != "completed" {
		t.Fatalf("tool chunk = %+v", tool)
	}
	if !strings.Contains(tool.ToolInput, `"path"`) || !strings.Contains(tool.ToolInput, "SKILL.md") {
		t.Fatalf("tool input = %q", tool.ToolInput)
	}
	if !strings.Contains(tool.ToolOutput, "line one") {
		t.Fatalf("tool output = %q", tool.ToolOutput)
	}
}

func TestAcpDisplayText(t *testing.T) {
	if got := acpDisplayText(nil); got != "" {
		t.Fatalf("nil = %q", got)
	}
	if got := acpDisplayText(json.RawMessage("null")); got != "" {
		t.Fatalf("null = %q", got)
	}
	if got := acpDisplayText(json.RawMessage(`"line one\nline two"`)); got != "line one\nline two" {
		t.Fatalf("string payload = %q", got)
	}
	got := acpDisplayText(json.RawMessage(`{"path":"a.md"}`))
	if !strings.Contains(got, "\n") || !strings.Contains(got, `"path": "a.md"`) {
		t.Fatalf("structured payload = %q", got)
	}
}

func TestAcpToolOutputFlattensContentBlocks(t *testing.T) {
	oldText := "old"
	content := &acpclient.UpdateContent{Blocks: []acpclient.ToolCallContent{
		{Type: "content", Content: &acpclient.ContentBlock{Type: "text", Text: "file body"}},
		{Type: "diff", Path: "a.md", OldText: &oldText, NewText: "new"},
	}}
	got := acpToolOutputText(nil, content)
	for _, want := range []string{"file body", "a.md", "- old", "+ new"} {
		if !strings.Contains(got, want) {
			t.Fatalf("flattened output %q missing %q", got, want)
		}
	}
	if raw := acpToolOutputText(json.RawMessage(`"raw wins"`), content); raw != "raw wins" {
		t.Fatalf("rawOutput must win: %q", raw)
	}
}
