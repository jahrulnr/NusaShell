package domain

import (
	"fmt"
	"strings"
)

// MemoryAuditFile is one live kind file row in memory-audit.sh.
type MemoryAuditFile struct {
	Name    string
	Lines   int
	Entries int
}

// MemoryAuditInput is the filesystem snapshot memory-audit.sh prints.
type MemoryAuditInput struct {
	Base                string
	Key                 string
	Present             bool
	Files               []MemoryAuditFile
	HasGuardrails       bool
	GuardrailsLines     int
	GuardrailsActive    int
	DuplicateScopeLines []string
	TopicRows           []string // already indented, uniq -c style
	EntriesWithLinks    int
	DevAccessCount      int // -1 when dev-access.md is absent
	HasDebug            bool
	LintProblems        []ProjectMemoryLintProblem
}

// FormatMemoryAudit is memory-audit.sh stdout.
func FormatMemoryAudit(in MemoryAuditInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "memory_base: %s\n", in.Base)
	fmt.Fprintf(&b, "sanitized_key: %s\n", in.Key)
	b.WriteByte('\n')
	if !in.Present {
		b.WriteString("status: missing\n")
		return b.String()
	}
	b.WriteString("status: present\n\nfiles:\n")
	for _, f := range in.Files {
		fmt.Fprintf(&b, "%s\tlines=%d\tentries=%d\n", f.Name, f.Lines, f.Entries)
	}
	b.WriteByte('\n')
	if in.HasGuardrails {
		fmt.Fprintf(&b, "guardrails: lines=%d active_count=%d\n", in.GuardrailsLines, in.GuardrailsActive)
		if in.GuardrailsLines > 150 {
			b.WriteString("warning: guardrails.md exceeds 150 lines — move RETIRED entries to archive/guardrails.md\n")
		}
	} else {
		b.WriteString("warning: no guardrails.md — negative-flow facts (do_not_repeat equivalents) have no dedicated home yet\n")
	}
	b.WriteString("\npossible duplicate SCOPE (same file, appears more than once):\n")
	for _, line := range in.DuplicateScopeLines {
		b.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nretrieval metadata:\n")
	if len(in.TopicRows) == 0 {
		b.WriteString("  no TOPICS yet\n")
	} else {
		for _, row := range in.TopicRows {
			b.WriteString(row)
			if !strings.HasSuffix(row, "\n") {
				b.WriteByte('\n')
			}
		}
	}
	fmt.Fprintf(&b, "  entries_with_links: %d\n", in.EntriesWithLinks)
	if in.DevAccessCount >= 0 {
		fmt.Fprintf(&b, "  dev_access_fixtures: %d (intentionally displayable only when lint is clean)\n", in.DevAccessCount)
	}
	b.WriteString("\ndebug promotion:\n")
	if in.HasDebug {
		b.WriteString("  informational — promote only lasting imperatives/procedures; not every bug needs a guardrail\n")
	}
	b.WriteByte('\n')
	if len(in.LintProblems) == 0 {
		b.WriteString(FormatLintReport(nil))
		b.WriteByte('\n')
		b.WriteString("audit_status: clean\n")
	} else {
		b.WriteString(FormatLintReport(in.LintProblems))
		b.WriteByte('\n')
		b.WriteString("audit_status: needs-curation\n")
	}
	return b.String()
}

// FormatUniqCountRow matches GNU `uniq -c` then `sed 's/^/  /'`.
func FormatUniqCountRow(count int, topic string) string {
	return fmt.Sprintf("  %7d %s", count, topic)
}
