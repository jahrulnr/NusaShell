package domain

import (
	"strings"
	"testing"
)

func TestNormalizeAcpRunTitle(t *testing.T) {
	if got := NormalizeAcpRunTitle("  Fix  auth  "); got != "Fix auth" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeAcpRunTitle("   "); got != "" {
		t.Fatalf("empty = %q", got)
	}
	long := strings.Repeat("あ", MaxAcpRunTitleLen+5)
	if got := NormalizeAcpRunTitle(long); len([]rune(got)) != MaxAcpRunTitleLen {
		t.Fatalf("len = %d, want %d (%q)", len([]rune(got)), MaxAcpRunTitleLen, got)
	}
}

func TestAcpRunLabelPrefersTitle(t *testing.T) {
	run := &AcpRun{AgentName: "Codex", Title: "Inspect pets"}
	if got := AcpRunLabel(run); got != "Inspect pets" {
		t.Fatalf("label = %q", got)
	}
	run.Title = ""
	if got := AcpRunLabel(run); got != "Codex" {
		t.Fatalf("fallback = %q", got)
	}
}
