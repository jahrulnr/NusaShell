package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// File is inside the workspace — no fallback summary needed.
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
	// Outside the workspace: sandbox may refuse the path, so a summary
	// fallback must travel with the prompt.
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
