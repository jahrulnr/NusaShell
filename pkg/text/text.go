// Package text provides pure string utilities for truncation and
// whitespace handling used by the agent turn loop, compaction, and
// headless pipeline steps.
//
// This is a shared leaf package at the module root (under pkg/) so both
// application/ and infrastructure/ can import it without violating Go's
// internal package rule or the Clean Architecture dependency rule. It
// depends only on the standard library.
package text

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Truncate keeps the first n bytes of s and appends an ellipsis when
// content was dropped. When n is zero or negative the input is returned
// unchanged. Truncation is byte-based; callers that need rune-safe
// truncation should use TruncateRunes.
func Truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// TruncateRunes keeps the first n runes of s and appends an omission
// marker with the number of characters dropped, so a downstream model
// can tell the payload was cut.
func TruncateRunes(s string, n int) string {
	omitted := len(s) - n
	head := []rune(s)
	if len(head) > n {
		head = head[:n]
		return string(head) + fmt.Sprintf("\n\n[truncated: %d chars omitted]", omitted)
	}
	return s
}

// TruncateWithNote keeps the first n bytes of s and appends a note
// describing how many characters were dropped. Used for error messages
// that may embed large base64 payloads and would pollute token budgets.
func TruncateWithNote(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated: original was " + strconv.Itoa(len(s)) + " chars]"
}

// Visible returns the assistant text worth persisting or sending. Models
// such as Qwen3.8 emit a blank paragraph ("\n\n") as the first or last
// content tokens after thinking; storing that makes empty rounds look
// like real turns.
func Visible(s string) string {
	return strings.TrimSpace(s)
}

// Persistable returns the content stored for a streamed round.
// Whitespace-only rounds are dropped entirely (Visible's job), but real
// content keeps its trailing space: a stream cut mid-sentence ends on a
// word boundary that the continuation round needs so the two halves do
// not run together ("here.And"). Leading whitespace (including blank
// paragraphs) carries no meaning and is trimmed; on the trailing side
// only newlines are stripped.
func Persistable(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	left := strings.TrimLeftFunc(s, unicode.IsSpace)
	return strings.TrimRight(left, "\r\n")
}
