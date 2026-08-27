package domain

import (
	"encoding/json"
	"strings"
)

// TodoStatus is the lifecycle state of a conversation todo item.
type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// validTodoStatuses is the set of allowed todo status values. Used by both
// the tool handler (model input validation) and the RPC handler (UI input
// validation) so the rule has a single source of truth.
var validTodoStatuses = map[TodoStatus]bool{
	TodoPending:    true,
	TodoInProgress: true,
	TodoCompleted:  true,
}

// IsValidTodoStatus reports whether s is one of the allowed todo status
// values (pending, in_progress, completed).
func IsValidTodoStatus(s TodoStatus) bool {
	return validTodoStatuses[s]
}

// TodoItem is a single agent-owned checklist entry. The ID is stable across
// updates so the UI can track items as the model rewrites the full list.
type TodoItem struct {
	ID      string     `json:"id"`
	Content string     `json:"content"`
	Status  TodoStatus `json:"status"`
}

// ConversationTodos is the full todo state for a conversation: the brief
// (a living planning document, max ~10k tokens) plus the multi-step item
// list. The brief is set at the start of a task and survives compaction —
// it is re-injected into every turn's hydration so the agent does not drift
// from the original intent after context summarization. The brief is
// updateable: the agent refines it as findings emerge and the approach
// solidifies.
//
// JSON backward compat: legacy `goal` field is unmarshaled into Brief.
type ConversationTodos struct {
	Brief string     `json:"brief,omitempty"`
	Items []TodoItem `json:"items"`
}

// UnmarshalJSON implements backward-compatible unmarshaling: legacy
// persisted state used the field name `goal`; it is read into Brief so
// existing todos.json files keep working without migration.
func (c *ConversationTodos) UnmarshalJSON(data []byte) error {
	type alias ConversationTodos
	var raw struct {
		alias
		Goal string `json:"goal,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.Brief = raw.Brief
	if c.Brief == "" && raw.Goal != "" {
		c.Brief = raw.Goal
	}
	c.Items = raw.Items
	return nil
}

// BriefSectionRequired lists the section headings that must be present in
// a non-empty brief. The brief is a living planning document structured as
// markdown sections; Objective and Done when are mandatory so the agent
// always states what the user asked for and what "finished" looks like.
// Findings and Approach are optional and grow as the task progresses.
var briefRequiredSections = []string{"## Objective", "## Done when"}

// ValidateBrief checks that a non-empty brief contains the required
// markdown sections (## Objective and ## Done when). An empty brief is
// valid (means "no brief set yet"). Returns nil if valid, an error
// describing the first missing section otherwise.
func ValidateBrief(brief string) error {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return nil
	}
	for _, section := range briefRequiredSections {
		if !strings.Contains(brief, section) {
			return errBriefMissingSection(section)
		}
	}
	return nil
}

// errBriefMissingSection returns a descriptive error for a missing section.
func errBriefMissingSection(section string) error {
	return &briefValidationError{Missing: section}
}

// BriefSummarySections lists the sections included in a compact brief
// summary (used when the full brief is too large to inline, e.g. ACP
// subagent spawn prompts where the plan file may be unreadable).
var briefSummarySections = []string{"## Objective", "## Done when"}

// SummarizeBrief extracts the Objective and Done when sections of a brief
// as a compact summary. Sections are returned verbatim (heading included),
// in order, separated by blank lines. Sections missing from the brief are
// skipped; a brief without any summary section returns "".
func SummarizeBrief(brief string) string {
	lines := strings.Split(brief, "\n")
	var out []string
	for _, section := range briefSummarySections {
		var body []string
		collecting := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if collecting && strings.HasPrefix(trimmed, "## ") {
				break
			}
			if !collecting && trimmed == section {
				collecting = true
			}
			if collecting {
				body = append(body, line)
			}
		}
		if len(body) > 0 {
			out = append(out, strings.TrimSpace(strings.Join(body, "\n")))
		}
	}
	return strings.Join(out, "\n\n")
}

type briefValidationError struct{ Missing string }

func (e *briefValidationError) Error() string {
	return "brief missing required section: " + e.Missing
}

// TodoSummary holds aggregate counts derived from a todo list. Used by the
// tool result and the RPC result.
type TodoSummary struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
}

// SummarizeTodos computes aggregate counts for a todo list.
func SummarizeTodos(items []TodoItem) TodoSummary {
	var s TodoSummary
	for _, item := range items {
		s.Total++
		switch item.Status {
		case TodoPending:
			s.Pending++
		case TodoInProgress:
			s.InProgress++
		case TodoCompleted:
			s.Completed++
		}
	}
	return s
}

// CountOpenTodos returns the number of items that are not yet completed
// (pending or in_progress).
func CountOpenTodos(items []TodoItem) int {
	n := 0
	for _, item := range items {
		if item.Status != TodoCompleted {
			n++
		}
	}
	return n
}
