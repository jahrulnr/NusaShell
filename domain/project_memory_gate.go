package domain

import (
	"fmt"
	"strings"
)

// MemoryGateInput is the mechanical state memory-gate.sh evaluates.
type MemoryGateInput struct {
	LintProblems   []ProjectMemoryLintProblem
	PatternNotes   string
	SkipGate       bool
	ChangedFiles   []string
	MemoryTouched  bool
	TouchMapRaw    string
	NoUpdateReason string
}

// MemoryGateResult is memory-gate.sh stdout plus whether the process would exit 0.
type MemoryGateResult struct {
	OK      bool
	Message string
}

// EvaluateMemoryGate ports memory-gate.sh after lint and optional pattern-track.
func EvaluateMemoryGate(in MemoryGateInput) MemoryGateResult {
	var b strings.Builder
	if notes := strings.TrimRight(in.PatternNotes, "\n"); notes != "" {
		b.WriteString(notes)
		b.WriteByte('\n')
	}
	b.WriteString(FormatLintReport(in.LintProblems))
	b.WriteByte('\n')
	if len(in.LintProblems) > 0 {
		b.WriteString("memory-gate: FAIL (memory-lint found unresolved issues above).")
		return MemoryGateResult{OK: false, Message: b.String()}
	}
	if in.SkipGate {
		b.WriteString("memory-gate: admission check skipped (SKIP_MEMORY_GATE=1); lint still passed")
		return MemoryGateResult{OK: true, Message: b.String()}
	}
	if len(in.ChangedFiles) == 0 {
		b.WriteString("memory-gate: no working-tree changes to evaluate; lint passed")
		return MemoryGateResult{OK: true, Message: b.String()}
	}
	durable := len(in.ChangedFiles) >= 3
	if !durable && in.TouchMapRaw != "" {
		for _, f := range in.ChangedFiles {
			if f != "" && strings.Contains(in.TouchMapRaw, f) {
				durable = true
				break
			}
		}
	}
	if durable && !in.MemoryTouched {
		if in.NoUpdateReason != "" {
			b.WriteString("memory-gate: pass — no memory update admitted: " + in.NoUpdateReason)
			return MemoryGateResult{OK: true, Message: b.String()}
		}
		fmt.Fprintf(&b, "memory-gate: FAIL\n  %d file(s) changed and no project memory was updated.\n  Admit only durable cross-task knowledge; do not add a delivery-log entry.\n  If durable knowledge exists, curate the smallest relevant existing entry.\n  Otherwise record the negative admission decision for this run only:\n    memory_project(op=\"gate\", reason=\"<why no reusable knowledge emerged>\")", len(in.ChangedFiles))
		return MemoryGateResult{OK: false, Message: b.String()}
	}
	if !durable && in.NoUpdateReason != "" {
		b.WriteString("memory-gate: pass — no durable signal; no-update reason accepted: " + in.NoUpdateReason)
		return MemoryGateResult{OK: true, Message: b.String()}
	}
	b.WriteString("memory-gate: pass")
	return MemoryGateResult{OK: true, Message: b.String()}
}
