package domain

import (
	"fmt"
	"sort"
	"strings"
)

// ProjectMemoryFileBlob is one kind file (live or archive) for linting.
type ProjectMemoryFileBlob struct {
	Rel      string // debug.md or archive/debug.md
	Kind     string // filename stem
	Raw      string
	Archived bool
}

// ProjectMemoryLintError is returned when admit/lint finds problems.
type ProjectMemoryLintError struct {
	Problems []ProjectMemoryLintProblem
}

func (e *ProjectMemoryLintError) Error() string {
	if e == nil {
		return "memory-lint: clean"
	}
	return FormatLintReport(e.Problems)
}

// FormatLintReport is memory-lint.sh stdout (and its non-zero exit summary).
func FormatLintReport(problems []ProjectMemoryLintProblem) string {
	if len(problems) == 0 {
		return "memory-lint: clean"
	}
	var b strings.Builder
	for _, p := range problems {
		b.WriteString("LINT FAIL [")
		b.WriteString(p.File)
		b.WriteString("]: ")
		b.WriteString(p.Message)
		b.WriteByte('\n')
		for _, d := range p.Details {
			b.WriteString("  ")
			b.WriteString(d)
			b.WriteByte('\n')
		}
		if p.Hint != "" {
			b.WriteString("  -> ")
			b.WriteString(p.Hint)
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "memory-lint: %d issue(s) found.", len(problems))
	return b.String()
}

// ProjectMemoryAdmitResult is the outcome of a successful admit.
type ProjectMemoryAdmitResult struct {
	ID          string
	Kind        string
	PatternNote string
}

// LintProjectMemory ports memory-lint.sh over in-memory file blobs.
// kinds, when set, limits live-file checks the way `memory-lint.sh <path> [kind ...]`
// does; known IDs and archive STATUS checks always scan every blob.
func LintProjectMemory(files []ProjectMemoryFileBlob, threshold int, kinds ...string) []ProjectMemoryLintProblem {
	if threshold <= 0 {
		threshold = ProjectPatternThreshold
	}
	wantKind := map[string]bool{}
	for _, k := range kinds {
		if n := NormalizeProjectKindFile(k); n != "" {
			wantKind[n] = true
		}
	}
	known := map[string]bool{}
	parsed := make([][]ProjectMemoryEntry, len(files))
	for i, f := range files {
		ents := ParseProjectMemoryEntries(f.Raw, f.Rel, f.Kind, f.Archived)
		parsed[i] = ents
		for _, e := range ents {
			if e.ID != "" {
				known[e.ID] = true
			}
		}
	}

	var problems []ProjectMemoryLintProblem
	for i, f := range files {
		if f.Archived {
			continue
		}
		if len(wantKind) > 0 && !wantKind[f.Kind] {
			continue
		}
		ents := parsed[i]
		if f.Kind == ProjectKindIndex {
			indexCount := 0
			for _, line := range strings.Split(f.Raw, "\n") {
				if strings.HasPrefix(line, "### BEGIN_ENTRY: IDX-") {
					indexCount++
				}
			}
			if indexCount > 1 {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    f.Rel,
					Message: fmt.Sprintf("%d live INDEX entries; expected at most one project snapshot/router.", indexCount),
					Hint:    "merge current state into one IDX-project entry; move delivery history out of index.",
				})
			}
		}
		if f.Kind == ProjectKindPatterns {
			for _, e := range ents {
				key := strings.TrimSpace(e.Fields["PATTERN_KEY"])
				occ := strings.TrimSpace(e.Fields["OCCURRENCES"])
				if key == "" {
					problems = append(problems, ProjectMemoryLintProblem{
						File:    f.Rel,
						Message: e.ID + " has no PATTERN_KEY.",
						Hint:    "remove legacy body-hash patterns; only explicit recurring workflows belong here.",
					})
					continue
				}
				n := 0
				_, err := fmt.Sscanf(occ, "%d", &n)
				if occ == "" {
					occ = "missing"
				}
				if err != nil || n < threshold {
					problems = append(problems, ProjectMemoryLintProblem{
						File:    f.Rel,
						Message: fmt.Sprintf("%s has OCCURRENCES=%s (threshold %d).", e.ID, occ, threshold),
						Hint:    "do not store singleton observations; let memory-pattern-track.sh promote keyed recurrences.",
					})
				}
			}
		}
		problems = append(problems, lintTopics(f.Rel, ents)...)
		problems = append(problems, lintLinks(f.Rel, ents, known)...)
		problems = append(problems, lintDevAccess(f.Kind, f.Rel, ents)...)
		problems = append(problems, lintDuplicateScope(f.Rel, ents)...)
		lines := strings.Count(f.Raw, "\n")
		cap := ProjectLineCap(f.Kind)
		if lines > cap {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    f.Rel,
				Message: fmt.Sprintf("%d live lines (cap %d).", lines, cap),
				Hint:    "merge overlapping knowledge and move stale/retired entries to archive/" + f.Kind + ".md.",
			})
		}
		problems = append(problems, lintOrphans(f.Rel, f.Raw)...)
	}
	for _, f := range files {
		if !f.Archived {
			continue
		}
		var details []string
		for _, e := range ParseProjectMemoryEntries(f.Raw, f.Rel, f.Kind, true) {
			st := strings.TrimSpace(e.Fields["STATUS"])
			if st == "ACTIVE" || st == "TRACKING" {
				details = append(details, e.ID+" (STATUS: "+st+")")
			}
		}
		if len(details) > 0 {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    f.Rel,
				Message: "entry filed as archived but still marked live:",
				Details: details,
				Hint:    "move it back to the live kind file; archive is for RETIRED/SUPERSEDED/DONE entries only.",
			})
		}
	}
	return problems
}

func lintTopics(file string, ents []ProjectMemoryEntry) []ProjectMemoryLintProblem {
	var problems []ProjectMemoryLintProblem
	for _, e := range ents {
		if len(e.Topics) == 0 {
			continue
		}
		if len(e.Topics) > 3 {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: fmt.Sprintf("%s has %d TOPICS; maximum is 3.", e.ID, len(e.Topics)),
			})
		}
		seen := map[string]bool{}
		for _, topic := range e.Topics {
			if topic == "" {
				continue
			}
			if !IsKebabTopic(topic) {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s topic '%s' must be lowercase kebab-case.", e.ID, topic),
				})
			}
			if seen[topic] {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s repeats topic '%s'.", e.ID, topic),
				})
			}
			seen[topic] = true
		}
	}
	return problems
}

func linkToken(l ProjectMemoryLink) string {
	if l.Raw != "" {
		return l.Raw
	}
	return l.Relation + ":" + l.Target
}

func lintLinks(file string, ents []ProjectMemoryEntry, known map[string]bool) []ProjectMemoryLintProblem {
	var problems []ProjectMemoryLintProblem
	for _, e := range ents {
		if len(e.Links) == 0 {
			continue
		}
		seen := map[string]bool{}
		for _, l := range e.Links {
			item := linkToken(l)
			if l.Target == "" || !strings.Contains(item, ":") {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s link '%s' must be relation:TARGET_ID.", e.ID, item),
				})
				continue
			}
			if !ProjectLinkRelations[l.Relation] {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s uses unknown link relation '%s'.", e.ID, l.Relation),
				})
			}
			if l.Target == e.ID {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: e.ID + " links to itself.",
				})
			} else if !known[l.Target] {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s link target '%s' does not exist in live memory or archive.", e.ID, l.Target),
				})
			}
			if seen[item] {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s repeats link '%s'.", e.ID, item),
				})
			}
			seen[item] = true
		}
	}
	return problems
}

func lintDevAccess(fileKind, file string, ents []ProjectMemoryEntry) []ProjectMemoryLintProblem {
	var problems []ProjectMemoryLintProblem
	nonDev := 0
	for _, e := range ents {
		if strings.ToUpper(e.Kind) != "DEV_ACCESS" {
			if fileKind == ProjectKindDevAccess {
				nonDev++
			}
			continue
		}
		if !IsDevAccessID(e.ID) {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: fmt.Sprintf("DEV_ACCESS id '%s' must use DEV-lowercase-kebab.", e.ID),
			})
		}
		if fileKind != ProjectKindDevAccess {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " KIND=DEV_ACCESS must live in dev-access.md.",
			})
		}
		if strings.TrimSpace(e.Fields["STATUS"]) != "ACTIVE" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " STATUS must be ACTIVE; archive retired fixtures.",
			})
		}
		if strings.TrimSpace(e.Scope) == "" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " requires a specific fixture SCOPE.",
			})
		}
		if strings.TrimSpace(e.Fields["ENVIRONMENT"]) != "local-development" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " ENVIRONMENT must be local-development.",
			})
		}
		material := strings.TrimSpace(e.Fields["MATERIAL_TYPE"])
		switch material {
		case "username-password", "cookie", "token", "api-key", "other":
		default:
			if material == "" {
				material = "missing"
			}
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: fmt.Sprintf("%s has unsupported MATERIAL_TYPE '%s'.", e.ID, material),
			})
		}
		if strings.TrimSpace(e.Fields["ACCESS"]) == "" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " requires ACCESS or an acquisition command.",
			})
		}
		if strings.TrimSpace(e.Fields["SAFE_TO_DISCLOSE"]) != "true" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " SAFE_TO_DISCLOSE must be true.",
			})
		}
		if strings.TrimSpace(e.Fields["PRODUCTION_REUSE"]) != "forbidden" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " PRODUCTION_REUSE must be forbidden.",
			})
		}
		source := strings.TrimSpace(e.Fields["SOURCE"])
		okSource := strings.HasPrefix(source, "checked-in:") && len(source) > len("checked-in:") ||
			strings.HasPrefix(source, "user-attested:") && len(source) > len("user-attested:")
		if !okSource {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " SOURCE must be checked-in:<repo-relative path> or user-attested:<attestation>.",
			})
		}
		if strings.HasPrefix(source, "checked-in:/") {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " checked-in SOURCE must use a repo-relative path.",
			})
		}
		if strings.TrimSpace(e.Fields["VERIFY"]) == "" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " requires VERIFY for the local-only boundary.",
			})
		}
	}
	if fileKind == ProjectKindDevAccess && nonDev > 0 {
		problems = append(problems, ProjectMemoryLintProblem{
			File:    file,
			Message: "every entry must use KIND: DEV_ACCESS.",
		})
	}
	return problems
}

func lintDuplicateScope(file string, ents []ProjectMemoryEntry) []ProjectMemoryLintProblem {
	type row struct {
		id, status string
	}
	groups := map[string][]row{}
	var order []string
	for _, e := range ents {
		scope := strings.ToLower(strings.Join(strings.Fields(e.Scope), " "))
		if scope == "" {
			continue
		}
		if _, ok := groups[scope]; !ok {
			order = append(order, scope)
		}
		groups[scope] = append(groups[scope], row{id: e.ID, status: strings.TrimSpace(e.Fields["STATUS"])})
	}
	sort.Strings(order)
	var problems []ProjectMemoryLintProblem
	for _, scope := range order {
		rows := groups[scope]
		if len(rows) < 2 {
			continue
		}
		unresolved := 0
		for _, r := range rows {
			if r.status != "SUPERSEDED" && r.status != "RETIRED" {
				unresolved++
			}
		}
		if unresolved > 1 {
			details := make([]string, 0, len(rows))
			for _, r := range rows {
				details = append(details, "ID="+r.id+" STATUS="+r.status)
			}
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: fmt.Sprintf("unresolved duplicate SCOPE %q:", scope),
				Details: details,
				Hint:    "merge into one entry, mark the older one SUPERSEDED/RETIRED, set SUPERSEDES.",
			})
		}
	}
	return problems
}

func lintOrphans(file, raw string) []ProjectMemoryLintProblem {
	depth := 0
	var orphans []string
	for i, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "### BEGIN_ENTRY: ") {
			depth++
			continue
		}
		if strings.HasPrefix(line, "### END_ENTRY: ") {
			depth--
			continue
		}
		if depth > 0 {
			continue
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		orphans = append(orphans, fmt.Sprintf("line %d: %s", i+1, line))
	}
	if len(orphans) == 0 {
		return nil
	}
	return []ProjectMemoryLintProblem{{
		File:    file,
		Message: "text found outside any anchored entry (invisible to anchor-based reads):",
		Details: orphans,
		Hint:    "wrap the fact in its own BEGIN_ENTRY/END_ENTRY block, or delete it if it duplicates an entry.",
	}}
}
