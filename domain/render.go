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

// conversationKeyAllowed strips everything outside a small safe charset.
// Conversation identities are persisted and used in store lookups, titles,
// and diagnostics, so they must stay filesystem- and SQL-friendly.
var conversationKeyAllowed = regexp.MustCompile(`[^A-Za-z0-9._:-]+`)

// maxConversationKeyLen bounds a rendered conversation identity so one long
// event value cannot produce an unbounded key.
const maxConversationKeyLen = 120

// RenderConversationKey resolves ${event.<key>} placeholders (same syntax as
// RenderAgentPrompt) and sanitizes the result into a stable conversation
// identity: characters outside [A-Za-z0-9._:-] collapse to '-', runs of '-'
// collapse, and the result is trimmed to maxConversationKeyLen. A static
// template without placeholders is returned as-is.
//
// When the template contains placeholders but any of them resolves empty
// (missing event, missing attribute, nil/empty value), the whole key renders
// "" — a partial key such as "tg-" would silently merge unrelated resources
// into one conversation. Callers fall back to their default (workflow ID).
func RenderConversationKey(template string, ev *Event) string {
	if template == "" || !strings.Contains(template, "${event.") {
		return sanitizeConversationKey(template)
	}
	missing := false
	resolved := eventPlaceholder.ReplaceAllStringFunc(template, func(match string) string {
		key := eventPlaceholder.FindStringSubmatch(match)
		if len(key) != 2 {
			missing = true
			return match
		}
		if ev == nil {
			missing = true
			return ""
		}
		value := stringify(lookupAttr(*ev, key[1]))
		if value == "" {
			missing = true
		}
		return value
	})
	if missing {
		return ""
	}
	return sanitizeConversationKey(resolved)
}

func sanitizeConversationKey(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	clean := conversationKeyAllowed.ReplaceAllString(raw, "-")
	for strings.Contains(clean, "--") {
		clean = strings.ReplaceAll(clean, "--", "-")
	}
	clean = strings.Trim(clean, "-")
	if len(clean) > maxConversationKeyLen {
		clean = clean[:maxConversationKeyLen]
	}
	return clean
}
