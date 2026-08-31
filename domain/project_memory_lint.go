package domain

import (
	"fmt"
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
	if e == nil || len(e.Problems) == 0 {
		return "memory-lint: clean"
	}
	return fmt.Sprintf("memory-lint: %d issue(s) found", len(e.Problems))
}

// ProjectMemoryAdmitResult is the outcome of a successful admit.
type ProjectMemoryAdmitResult struct {
	ID          string
	Kind        string
	PatternNote string
}

// LintProjectMemory ports memory-lint.sh over in-memory file blobs.
func LintProjectMemory(files []ProjectMemoryFileBlob, threshold int) []ProjectMemoryLintProblem {
	if threshold <= 0 {
		threshold = ProjectPatternThreshold
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
					Message: fmt.Sprintf("%d live INDEX entries; expected at most one project snapshot/router", indexCount),
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
						Message: e.ID + " has no PATTERN_KEY",
					})
					continue
				}
				n := 0
				_, err := fmt.Sscanf(occ, "%d", &n)
				if err != nil || n < threshold {
					problems = append(problems, ProjectMemoryLintProblem{
						File:    f.Rel,
						Message: fmt.Sprintf("%s has OCCURRENCES=%s (threshold %d)", e.ID, occ, threshold),
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
				Message: fmt.Sprintf("%d live lines (cap %d)", lines, cap),
			})
		}
		problems = append(problems, lintOrphans(f.Rel, f.Raw)...)
	}
	for _, f := range files {
		if !f.Archived {
			continue
		}
		for _, e := range ParseProjectMemoryEntries(f.Raw, f.Rel, f.Kind, true) {
			st := strings.TrimSpace(e.Fields["STATUS"])
			if st == "ACTIVE" || st == "TRACKING" {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    f.Rel,
					Message: fmt.Sprintf("%s filed as archived but still marked live (STATUS: %s)", e.ID, st),
				})
			}
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
				Message: fmt.Sprintf("%s has %d TOPICS; maximum is 3", e.ID, len(e.Topics)),
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
					Message: fmt.Sprintf("%s topic %q must be lowercase kebab-case", e.ID, topic),
				})
			}
			if seen[topic] {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s repeats topic %q", e.ID, topic),
				})
			}
			seen[topic] = true
		}
	}
	return problems
}

func lintLinks(file string, ents []ProjectMemoryEntry, known map[string]bool) []ProjectMemoryLintProblem {
	var problems []ProjectMemoryLintProblem
	for _, e := range ents {
		if len(e.Links) == 0 {
			continue
		}
		seen := map[string]bool{}
		for _, l := range e.Links {
			item := l.Relation + ":" + l.Target
			if l.Relation == "" || l.Target == "" {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s link %q must be relation:TARGET_ID", e.ID, item),
				})
				continue
			}
			if !ProjectLinkRelations[l.Relation] {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s uses unknown link relation %q", e.ID, l.Relation),
				})
			}
			if l.Target == e.ID {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: e.ID + " links to itself",
				})
			} else if !known[l.Target] {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s link target %q does not exist in live memory or archive", e.ID, l.Target),
				})
			}
			if seen[item] {
				problems = append(problems, ProjectMemoryLintProblem{
					File:    file,
					Message: fmt.Sprintf("%s repeats link %q", e.ID, item),
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
				Message: fmt.Sprintf("DEV_ACCESS id %q must use DEV-lowercase-kebab", e.ID),
			})
		}
		if fileKind != ProjectKindDevAccess {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " KIND=DEV_ACCESS must live in dev-access.md",
			})
		}
		if strings.TrimSpace(e.Fields["STATUS"]) != "ACTIVE" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " STATUS must be ACTIVE; archive retired fixtures",
			})
		}
		if strings.TrimSpace(e.Scope) == "" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " requires a specific fixture SCOPE",
			})
		}
		if strings.TrimSpace(e.Fields["ENVIRONMENT"]) != "local-development" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " ENVIRONMENT must be local-development",
			})
		}
		switch strings.TrimSpace(e.Fields["MATERIAL_TYPE"]) {
		case "username-password", "cookie", "token", "api-key", "other":
		default:
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: fmt.Sprintf("%s has unsupported MATERIAL_TYPE %q", e.ID, e.Fields["MATERIAL_TYPE"]),
			})
		}
		if strings.TrimSpace(e.Fields["ACCESS"]) == "" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " requires ACCESS or an acquisition command",
			})
		}
		if strings.TrimSpace(e.Fields["SAFE_TO_DISCLOSE"]) != "true" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " SAFE_TO_DISCLOSE must be true",
			})
		}
		if strings.TrimSpace(e.Fields["PRODUCTION_REUSE"]) != "forbidden" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " PRODUCTION_REUSE must be forbidden",
			})
		}
		source := strings.TrimSpace(e.Fields["SOURCE"])
		okSource := strings.HasPrefix(source, "checked-in:") && len(source) > len("checked-in:") ||
			strings.HasPrefix(source, "user-attested:") && len(source) > len("user-attested:")
		if !okSource {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " SOURCE must be checked-in:<repo-relative path> or user-attested:<attestation>",
			})
		}
		if strings.HasPrefix(source, "checked-in:/") {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " checked-in SOURCE must use a repo-relative path",
			})
		}
		if strings.TrimSpace(e.Fields["VERIFY"]) == "" {
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: e.ID + " requires VERIFY for the local-only boundary",
			})
		}
	}
	if fileKind == ProjectKindDevAccess && nonDev > 0 {
		problems = append(problems, ProjectMemoryLintProblem{
			File:    file,
			Message: "every entry must use KIND: DEV_ACCESS",
		})
	}
	return problems
}

func lintDuplicateScope(file string, ents []ProjectMemoryEntry) []ProjectMemoryLintProblem {
	type row struct {
		id, status string
	}
	groups := map[string][]row{}
	for _, e := range ents {
		scope := strings.ToLower(strings.Join(strings.Fields(e.Scope), " "))
		if scope == "" {
			continue
		}
		groups[scope] = append(groups[scope], row{id: e.ID, status: strings.TrimSpace(e.Fields["STATUS"])})
	}
	var problems []ProjectMemoryLintProblem
	for scope, rows := range groups {
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
			problems = append(problems, ProjectMemoryLintProblem{
				File:    file,
				Message: fmt.Sprintf("unresolved duplicate SCOPE %q", scope),
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
		orphans = append(orphans, fmt.Sprintf("%d: %s", i+1, line))
	}
	if len(orphans) == 0 {
		return nil
	}
	return []ProjectMemoryLintProblem{{
		File:    file,
		Message: "text found outside any anchored entry (invisible to anchor-based reads)",
	}}
}
