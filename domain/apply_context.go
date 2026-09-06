package domain

import (
	"sort"
	"strings"
	"unicode/utf8"
)

const ApplyBlockTokenCap = 400

// ApplyLineMaxBodyRunes caps one record's body inside the APPLY block.
// Long research notes and multi-sentence records would otherwise consume
// the whole token budget for a single line.
const ApplyLineMaxBodyRunes = 180

// ApplyDuplicateBodySimilarity is the overlap threshold above which two
// records are considered the same fact for the APPLY block. Near-duplicate
// clusters (for example five records about one provider rate-limit outage)
// collapse to the strongest representative line instead of burning the
// budget on retellings.
const ApplyDuplicateBodySimilarity = 0.85

// ApplyLine is one instruction the context constructor injects so retrieval
// becomes utilization (Know and Act).
type ApplyLine struct {
	Kind      string // preference | constraint | convention
	Scope     string
	Text      string
	DoNotWhen string
}

// applyTypePriority orders record kinds for the APPLY block. Constraints
// (how work must be done here) and preferences (how the user wants to work)
// outrank facts and beliefs, which rarely change behavior in the next turn.
func applyTypePriority(typ string) int {
	switch typ {
	case MemoryTypeConstraint:
		return 0
	case MemoryTypePreference:
		return 1
	case MemoryTypeProjectConvention:
		return 2
	case MemoryTypeFact:
		return 3
	default:
		return 4
	}
}

// BuildApplyBlock renders top-K retrievable records as an APPLY instruction
// block. Retired/superseded records are skipped. Records are ranked by
// kind (constraint/preference first), then evidence count, then most
// recent confirmation, so the strongest guidance wins the token budget.
// Bodies are trimmed and near-duplicate bodies collapse into one line.
// Token budget is approximate (chars/4).
func BuildApplyBlock(records []*MemoryRecord, maxTokens int) string {
	if maxTokens <= 0 {
		maxTokens = ApplyBlockTokenCap
	}
	ranked := make([]*MemoryRecord, 0, len(records))
	for _, m := range records {
		if m == nil || !m.Retrievable() {
			continue
		}
		ranked = append(ranked, m)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		pi, pj := applyTypePriority(ranked[i].Type), applyTypePriority(ranked[j].Type)
		if pi != pj {
			return pi < pj
		}
		if ranked[i].EvidenceCount != ranked[j].EvidenceCount {
			return ranked[i].EvidenceCount > ranked[j].EvidenceCount
		}
		return ranked[i].LastConfirmed.After(ranked[j].LastConfirmed)
	})

	var b strings.Builder
	b.WriteString("APPLY these records unless a narrower scope overrides them:\n")
	used := EstimateTokens(b.String())
	n := 0
	var includedBodies []string
	for _, m := range ranked {
		line := formatApplyLine(m)
		if !applyLineDistinct(line, includedBodies) {
			continue
		}
		need := EstimateTokens(line) + 1
		if used+need > maxTokens && n > 0 {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
		used += need
		n++
		includedBodies = append(includedBodies, line)
	}
	if n == 0 {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}

// applyLineDistinct reports whether a rendered line repeats a fact already
// included in the block (near-duplicate body, or one body contained in
// another). It keeps the first, strongest occurrence of each fact cluster.
func applyLineDistinct(line string, included []string) bool {
	body := strings.TrimSpace(line)
	if body == "" {
		return false
	}
	for _, prev := range included {
		p := strings.TrimSpace(prev)
		if p == "" {
			continue
		}
		if strings.Contains(p, body) || strings.Contains(body, p) {
			return false
		}
		if MemorySimilarity(body, p) >= ApplyDuplicateBodySimilarity {
			return false
		}
	}
	return true
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
	text = clipApplyBody(text)
	return "- [" + m.Type + " / " + scope + "] " + text
}

// clipApplyBody shortens a record body for the APPLY line without chopping
// mid-word into an unreadable fragment: it cuts at the last word boundary
// before the cap and marks the cut with " …" so the agent knows the record
// is longer than the line shows.
func clipApplyBody(text string) string {
	if utf8.RuneCountInString(text) <= ApplyLineMaxBodyRunes {
		return text
	}
	runes := []rune(text)
	cut := runes[:ApplyLineMaxBodyRunes]
	lastSpace := -1
	for i := len(cut) - 1; i >= 0; i-- {
		if cut[i] == ' ' || cut[i] == '\n' {
			lastSpace = i
			break
		}
	}
	if lastSpace >= ApplyLineMaxBodyRunes/2 {
		cut = cut[:lastSpace]
	}
	return strings.TrimSpace(string(cut)) + " …"
}
