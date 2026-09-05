package domain

import (
	"fmt"
	"strings"
)

const ApplyBlockTokenCap = 400

// ApplyLine is one instruction the context constructor injects so retrieval
// becomes utilization (Know and Act).
type ApplyLine struct {
	Kind      string // preference | constraint | convention
	Scope     string
	Text      string
	DoNotWhen string
}

// BuildApplyBlock renders top-K retrievable records as an APPLY instruction
// block. Retired/superseded records are skipped. Token budget is approximate
// (chars/4).
func BuildApplyBlock(records []*MemoryRecord, maxTokens int) string {
	if maxTokens <= 0 {
		maxTokens = ApplyBlockTokenCap
	}
	var b strings.Builder
	b.WriteString("APPLY these records unless a narrower scope overrides them:\n")
	used := EstimateTokens(b.String())
	n := 0
	for _, m := range records {
		if m == nil || !m.Retrievable() {
			continue
		}
		line := formatApplyLine(m)
		need := EstimateTokens(line) + 1
		if used+need > maxTokens && n > 0 {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
		used += need
		n++
	}
	if n == 0 {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatApplyLine(m *MemoryRecord) string {
	scope := m.Scope.Level
	if m.Scope.Project != "" {
		scope = m.Scope.Level + ":" + m.Scope.Project
	} else if m.Scope.Domain != "" {
		scope = m.Scope.Level + ":" + m.Scope.Domain
	}
	text := strings.TrimSpace(m.Body)
	if text == "" {
		text = strings.TrimSpace(strings.Join([]string{m.Subject, m.Predicate, m.Object}, " "))
	}
	return fmt.Sprintf("- [%s / %s] %s", m.Type, scope, text)
}
