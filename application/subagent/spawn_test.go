package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/domain"
)

const testBrief = "## Objective\nBuild the feature\n\n## Done when\nTests pass\n\n## Findings\nsrc/x.go:10"

func TestWithParentPlanFileInsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	planDir := filepath.Join(ws, ".nusashell", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(planDir, "conv_1.plan.md")
	if err := os.WriteFile(planPath, []byte(testBrief), 0o600); err != nil {
		t.Fatal(err)
	}

	got := withParentPlan("do the slice", planPath, testBrief, ws)
	if !strings.Contains(got, "do the slice") {
		t.Errorf("original prompt lost:\n%s", got)
	}
	if !strings.Contains(got, "Parent plan file (read this first): "+planPath) {
		t.Errorf("plan path not injected:\n%s", got)
	}
	if strings.Contains(got, "Parent plan summary") {
		t.Errorf("in-workspace plan must not add a fallback summary:\n%s", got)
	}
}

func TestWithParentPlanFileOutsideWorkspaceAddsSummary(t *testing.T) {
	planDir := t.TempDir()
	ws := t.TempDir()
	planPath := filepath.Join(planDir, "conv_1.plan.md")
	if err := os.WriteFile(planPath, []byte(testBrief), 0o600); err != nil {
		t.Fatal(err)
	}

	got := withParentPlan("do the slice", planPath, testBrief, ws)
	if !strings.Contains(got, "Parent plan file (read this first): "+planPath) {
		t.Errorf("plan path not injected:\n%s", got)
	}
	if !strings.Contains(got, "Parent plan summary") {
		t.Errorf("out-of-workspace plan must add a fallback summary:\n%s", got)
	}
	if !strings.Contains(got, "## Objective\nBuild the feature") {
		t.Errorf("summary missing Objective:\n%s", got)
	}
	if strings.Contains(got, "Findings") {
		t.Errorf("summary must stay compact (no Findings):\n%s", got)
	}
}

func TestWithParentPlanMissingFileFallsBackToSummary(t *testing.T) {
	got := withParentPlan("do the slice", "/nonexistent/conv_1.plan.md", testBrief, t.TempDir())
	if strings.Contains(got, "Parent plan file") {
		t.Errorf("missing file must not be advertised:\n%s", got)
	}
	if !strings.Contains(got, "Parent plan summary:\n## Objective\nBuild the feature") {
		t.Errorf("missing file must inline the summary:\n%s", got)
	}
}

func TestWithParentPlanNoPathUsesSummary(t *testing.T) {
	got := withParentPlan("do the slice", "", testBrief, "")
	if !strings.Contains(got, "Parent plan summary") {
		t.Errorf("empty plan path must inline the summary:\n%s", got)
	}
}

// fakeBriefs is a minimal TodoBriefs for internal-spawn handoff tests.
type fakeBriefs struct {
	brief string
	path  string
}

func (f fakeBriefs) GetBrief(string) string { return f.brief }
func (f fakeBriefs) PlanPath(string) string { return f.path }

// TestWithInternalParentPlanInlinesCompactSummary proves the internal
// delegate handoff inlines the compact Objective + Done when summary from
// the parent brief without advertising a plan file path the child cannot
// read (the plan file lives outside the child workspace).
func TestWithInternalParentPlanInlinesCompactSummary(t *testing.T) {
	got := withInternalParentPlan("do the slice", testBrief)
	if !strings.Contains(got, "do the slice") {
		t.Errorf("original prompt lost:\n%s", got)
	}
	if !strings.Contains(got, "## Objective\nBuild the feature") {
		t.Errorf("handoff missing compact Objective:\n%s", got)
	}
	if !strings.Contains(got, "## Done when\nTests pass") {
		t.Errorf("handoff missing compact Done when:\n%s", got)
	}
	if strings.Contains(got, "Findings") {
		t.Errorf("handoff must stay compact (no Findings):\n%s", got)
	}
	if strings.Contains(got, "Parent plan file (read this first)") {
		t.Errorf("internal handoff must not advertise an unreadable plan path:\n%s", got)
	}
}

// TestWithInternalParentPlanOmitsHandoffWhenNoBrief proves the helper adds
// nothing when the parent has no brief, so the explicit parent prompt stays
// authoritative and uncluttered.
func TestWithInternalParentPlanOmitsHandoffWhenNoBrief(t *testing.T) {
	got := withInternalParentPlan("do the slice", "")
	if got != "do the slice" {
		t.Errorf("empty brief must return the prompt unchanged, got:\n%s", got)
	}
}

// TestSpawnInternalAppendsParentPlanHandoff proves the internal spawn path
// forwards the compact parent plan summary to the headless delegate turn
// when the parent conversation has a brief, and never advertises a plan
// file path the child workspace cannot read.
func TestSpawnInternalAppendsParentPlanHandoff(t *testing.T) {
	var capturedPrompt string
	svc := New(Deps{
		Todos:        fakeBriefs{brief: testBrief, path: "/outside/child/conv_parent.plan.md"},
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string)) (map[string]any, string, error) {
			capturedPrompt = prompt
			return map[string]any{"output": "done"}, "conv_delegate", nil
		},
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"do the slice","agent_id":"internal"}`))
	if err != nil {
		t.Fatalf("spawn internal: %v", err)
	}
	waitForSettled(t, svc, firstSpawnedRunID(t, out))
	if !strings.Contains(capturedPrompt, "do the slice") {
		t.Errorf("delegate prompt lost the parent prompt:\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "## Objective\nBuild the feature") {
		t.Errorf("delegate prompt missing compact Objective handoff:\n%s", capturedPrompt)
	}
	if strings.Contains(capturedPrompt, "Parent plan file (read this first)") {
		t.Errorf("internal delegate must not be pointed at an unreadable plan path:\n%s", capturedPrompt)
	}
}

// TestSpawnInternalOmitsHandoffWhenParentHasNoBrief proves no handoff is
// appended when the parent conversation has no brief.
func TestSpawnInternalOmitsHandoffWhenParentHasNoBrief(t *testing.T) {
	var capturedPrompt string
	svc := New(Deps{
		Todos:        fakeBriefs{},
		ResolveModel: func(string) (string, error) { return "cheap:model", nil },
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string)) (map[string]any, string, error) {
			capturedPrompt = prompt
			return map[string]any{"output": "done"}, "conv_delegate", nil
		},
	})
	out, err := svc.SpawnSubagents(context.Background(), "conv_parent", "call_parent", []byte(`{"prompt":"do the slice","agent_id":"internal"}`))
	if err != nil {
		t.Fatalf("spawn internal: %v", err)
	}
	waitForSettled(t, svc, firstSpawnedRunID(t, out))
	if capturedPrompt != "do the slice" {
		t.Errorf("no brief must leave the prompt unchanged, got:\n%s", capturedPrompt)
	}
}
