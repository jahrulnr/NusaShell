package application

import (
	"context"
	"nusashell/domain"
	"strings"
	"testing"
)

type listingToolbox struct {
	names []string
	calls []string
}

func (l *listingToolbox) ListTools() []ToolInfo {
	out := make([]ToolInfo, 0, len(l.names))
	for _, n := range l.names {
		out = append(out, ToolInfo{Name: n})
	}
	return out
}

func (l *listingToolbox) Execute(_ context.Context, name string, _ []byte) (string, error) {
	l.calls = append(l.calls, name)
	return "ok:" + name, nil
}

// streamedListingToolbox is a listingToolbox that also supports streaming
// execution (optional capability exercised by FilteredToolbox).
type streamedListingToolbox struct {
	*listingToolbox
}

func (s *streamedListingToolbox) ExecuteStreamed(_ context.Context, name string, _ []byte, onChunk func(string)) (string, error) {
	s.listingToolbox.calls = append(s.listingToolbox.calls, "stream:"+name)
	if onChunk != nil {
		onChunk("chunk-" + name)
	}
	return "streamed:" + name, nil
}

func TestFilterACPToolsHidesSubagentSurface(t *testing.T) {
	inner := &listingToolbox{names: []string{"automation", "subagent", "subagent_steer", "subagent_stop", "subagent_wait", "docs"}}
	filtered := FilterACPTools(inner)
	got := map[string]bool{}
	for _, ti := range filtered.ListTools() {
		got[ti.Name] = true
		if IsACPTool(ti.Name) {
			t.Fatalf("pipeline toolbox listed ACP tool %q", ti.Name)
		}
	}
	if !got["automation"] || !got["docs"] {
		t.Fatalf("non-ACP tools must remain visible, got %v", got)
	}
	if got["subagent"] {
		t.Fatal("subagent must not be visible to pipeline agents")
	}
}

func TestFilterACPToolsRejectsExecute(t *testing.T) {
	inner := &listingToolbox{names: []string{"subagent", "automation"}}
	filtered := FilterACPTools(inner)
	if _, err := filtered.Execute(context.Background(), "subagent", []byte(`{"prompt":"x"}`)); err == nil || !strings.Contains(err.Error(), "not available to pipeline agent") {
		t.Fatalf("expected pipeline deny, got %v", err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("inner must not execute hidden tools, calls=%v", inner.calls)
	}
	out, err := filtered.Execute(context.Background(), "automation", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok:automation" {
		t.Fatalf("got %q", out)
	}
}

func TestFilteredToolboxExecuteStreamedPassthrough(t *testing.T) {
	inner := &streamedListingToolbox{listingToolbox: &listingToolbox{names: []string{"exec"}}}
	filtered := FilterACPTools(inner)
	var chunks []string
	out, err := filtered.ExecuteStreamed(context.Background(), "exec", []byte(`{}`), func(text string) { chunks = append(chunks, text) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "streamed:exec" {
		t.Fatalf("got %q", out)
	}
	if len(chunks) != 1 || chunks[0] != "chunk-exec" {
		t.Fatalf("chunks = %v", chunks)
	}
}

func TestFilteredToolboxExecuteStreamedFallback(t *testing.T) {
	inner := &listingToolbox{names: []string{"exec"}}
	filtered := FilterACPTools(inner)
	var chunks []string
	out, err := filtered.ExecuteStreamed(context.Background(), "exec", []byte(`{}`), func(text string) { chunks = append(chunks, text) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok:exec" {
		t.Fatalf("got %q", out)
	}
	if len(chunks) != 0 {
		t.Fatalf("inner without ExecuteStreamed must not stream, chunks = %v", chunks)
	}
}

func TestPipelineAgentRunnerDoesNotLeakACP(t *testing.T) {
	inner := &listingToolbox{names: []string{"subagent", "automation"}}
	runner := NewPipelineAgentRunner(inner, nil)
	_, _, err := runner.RunAgentStep(context.Background(), "do work", "", domain.TrustSafe, nil)
	if err == nil || !strings.Contains(err.Error(), "agent steps are not configured") {
		t.Fatalf("want not-configured after hiding ACP, got %v", err)
	}
	for _, ti := range runner.Tools.ListTools() {
		if IsACPTool(ti.Name) {
			t.Fatalf("pipeline runner listed %q", ti.Name)
		}
	}
}

func TestInteractiveToolboxKeepsACPTools(t *testing.T) {
	inner := &listingToolbox{names: []string{"subagent", "automation"}}
	names := map[string]bool{}
	for _, ti := range inner.ListTools() {
		names[ti.Name] = true
	}
	if !names["subagent"] {
		t.Fatal("interactive agent must still see subagent when ACP is enabled")
	}
}
