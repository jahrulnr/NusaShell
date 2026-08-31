package domain

import (
	"regexp"
	"strings"
)

// eventPlaceholder matches a single ${event.<key>} token in a template.
// Key is the dotted attribute path; empty/whitespace and any surrounding
// braces are part of the syntax. A leading dot in <key> is rejected.
var eventPlaceholder = regexp.MustCompile(`\$\{event\.([A-Za-z0-9_.-]+)\}`)

// RenderAgentPrompt resolves ${event.<key>} placeholders against an Event.
//
// A placeholder is replaced by the matching event attribute (looked up via
// Event.lookupAttr, which understands "subject", "type", "source", direct
// keys, and dotted paths inside Attributes). Missing or nil values render as
// the empty string. Non-string values render via fmt.Sprint.
//
// Templates without placeholders are returned untouched (cheap path).
// Backward compatible: prompts written before this feature still render
// identically because the regex never matches.
func RenderAgentPrompt(template string, ev *Event) string {
	if template == "" || !strings.Contains(template, "${event.") {
		return template
	}
	return eventPlaceholder.ReplaceAllStringFunc(template, func(match string) string {
		key := eventPlaceholder.FindStringSubmatch(match)
		if len(key) != 2 {
			return match
		}
		if ev == nil {
			return ""
		}
		return stringify(lookupAttr(*ev, key[1]))
	})
}
