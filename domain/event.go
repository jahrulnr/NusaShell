package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Event is the normalized envelope every event source publishes.
type Event struct {
	ID         string
	Type       string
	Source     string
	Time       time.Time
	Subject    string
	Attributes map[string]any
	Data       json.RawMessage
}

// Match reports whether the event satisfies a when-trigger filter.
// The matcher never requires the raw Data payload.
func (e Event) Match(eventType string, where map[string]any) bool {
	if eventType != "" && e.Type != eventType {
		return false
	}
	return MatchWhere(e, where)
}

// MatchWhere evaluates equality and the small set of *_contains keys.
func MatchWhere(e Event, where map[string]any) bool {
	if len(where) == 0 {
		return true
	}
	for key, want := range where {
		if !matchWhereKey(e, key, want) {
			return false
		}
	}
	return true
}

func matchWhereKey(e Event, key string, want any) bool {
	switch {
	case key == "subject_contains":
		return strings.Contains(strings.ToLower(e.Subject), strings.ToLower(fmt.Sprint(want)))
	case strings.HasSuffix(key, "_contains"):
		field := strings.TrimSuffix(key, "_contains")
		got := lookupAttr(e, field)
		return strings.Contains(strings.ToLower(fmt.Sprint(got)), strings.ToLower(fmt.Sprint(want)))
	default:
		got := lookupAttr(e, key)
		return stringify(got) == stringify(want)
	}
}

func lookupAttr(e Event, key string) any {
	if key == "subject" {
		return e.Subject
	}
	if key == "type" {
		return e.Type
	}
	if key == "source" {
		return e.Source
	}
	if e.Attributes == nil {
		return nil
	}
	if v, ok := e.Attributes[key]; ok {
		return v
	}
	// dotted path: mailbox.folder
	cur := any(e.Attributes)
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[part]
		if !ok {
			return nil
		}
	}
	return cur
}

func stringify(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// DeliveryKey is the idempotency tuple for at-least-once event handling.
func DeliveryKey(eventID, triggerID, workflowID string) string {
	return eventID + "\x1f" + triggerID + "\x1f" + workflowID
}
