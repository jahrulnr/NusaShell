// Package application — learning extraction.
//
// The extractor implements the write path from the learning-sources audit:
// text → rule-based extraction → (LLM fallback) → resolve → store.
//
// Rule-based extraction is zero-cost (no API calls) and catches the most
// common learning signals: explicit decisions, preferences, errors, and
// tool usage patterns. LLM fallback is reserved for Phase 2b.
package application

import (
	"regexp"
	"strings"
	"time"

	"nusashell/domain"
)

// LearningSignal classifies what kind of learning was extracted.
type LearningSignal string

const (
	SignalDecision   LearningSignal = "decision"   // user made a choice
	SignalPreference LearningSignal = "preference" // user stated a preference
	SignalError      LearningSignal = "error"      // error + fix pattern
	SignalFact       LearningSignal = "fact"       // reusable fact/knowledge
)

// ExtractedObservation is a single learning signal pulled from a turn.
type ExtractedObservation struct {
	Signal  LearningSignal
	Content string  // normalized summary text
	Source  string  // "user" | "agent"
	Weight  float64 // initial confidence [0,1]
	Tags    []string
}

// ExtractObservations runs rule-based extraction over a turn's messages.
// It scans the user message and assistant response for patterns that
// indicate a reusable learning signal. Returns nil if no signals found.
//
// Rules are intentionally conservative — false negatives are acceptable
// (the LLM fallback can catch what rules miss), but false positives create
// noise in the memory store.
func ExtractObservations(userMsg, assistantMsg string) []ExtractedObservation {
	var out []ExtractedObservation
	userMsg = strings.TrimSpace(userMsg)
	assistantMsg = strings.TrimSpace(assistantMsg)

	// 1. Decisions: user explicitly chooses between options.
	if obs := extractDecision(userMsg); obs != nil {
		out = append(out, *obs)
	}

	// 2. Preferences: user states a preference pattern.
	for _, obs := range extractPreferences(userMsg) {
		out = append(out, obs)
	}

	// 3. Errors + fixes: assistant message contains error→resolution pattern.
	if obs := extractErrorFix(assistantMsg); obs != nil {
		out = append(out, *obs)
	}

	// 4. Facts: user message contains "remember that" / "note that".
	if obs := extractFact(userMsg); obs != nil {
		out = append(out, *obs)
	}

	return out
}

// --- Rule patterns ---

var (
	// "let's use X" / "let's go with X" / "I'll use X" / "we should use X"
	decisionRe = regexp.MustCompile(`(?i)\b(?:let'?s|we should|i'?ll|i will|let me|going to|gonna)\s+(?:use|go with|pick|choose|switch to|try)\s+(\S.{2,80})`)
	// "I prefer X" / "i like X better" / "always use X" / "never use X"
	preferRe = regexp.MustCompile(`(?i)\b(?:i prefer|i like \w+ better|always use|never use|don'?t use|avoid using|stick with)\s+(\S.{2,80})`)
	// "remember that X" / "note that X" / "keep in mind X"
	factRe = regexp.MustCompile(`(?i)\b(?:remember that|note that|keep in mind|don'?t forget that|for future reference)\s+(\S.{5,200})`)
	// Error patterns in assistant output: "Error: X" followed by "Fix: Y" or "Solution: Y"
	errorFixRe = regexp.MustCompile(`(?is)(?:error|failed|failure|panic)[:]\s*(.{5,200}?)\n+(?:fix|solution|resolved|workaround)[:]\s*(.{5,200}?)`)
)

func extractDecision(msg string) *ExtractedObservation {
	m := decisionRe.FindStringSubmatch(msg)
	if m == nil {
		return nil
	}
	choice := cleanMatch(m[1])
	if choice == "" {
		return nil
	}
	return &ExtractedObservation{
		Signal:  SignalDecision,
		Content: "Decision: use " + choice,
		Source:  "user",
		Weight:  0.6,
		Tags:    []string{"decision"},
	}
}

func extractPreferences(msg string) []ExtractedObservation {
	matches := preferRe.FindAllStringSubmatch(msg, -1)
	var out []ExtractedObservation
	for _, m := range matches {
		pref := cleanMatch(m[1])
		if pref == "" {
			continue
		}
		out = append(out, ExtractedObservation{
			Signal:  SignalPreference,
			Content: "Preference: " + pref,
			Source:  "user",
			Weight:  0.5,
			Tags:    []string{"preference"},
		})
	}
	return out
}

func extractErrorFix(msg string) *ExtractedObservation {
	m := errorFixRe.FindStringSubmatch(msg)
	if m == nil {
		return nil
	}
	errText := cleanMatch(m[1])
	fixText := cleanMatch(m[2])
	if errText == "" || fixText == "" {
		return nil
	}
	return &ExtractedObservation{
		Signal:  SignalError,
		Content: "Error: " + errText + " → Fix: " + fixText,
		Source:  "agent",
		Weight:  0.7,
		Tags:    []string{"error", "fix"},
	}
}

func extractFact(msg string) *ExtractedObservation {
	m := factRe.FindStringSubmatch(msg)
	if m == nil {
		return nil
	}
	fact := cleanMatch(m[1])
	if fact == "" {
		return nil
	}
	return &ExtractedObservation{
		Signal:  SignalFact,
		Content: fact,
		Source:  "user",
		Weight:  0.8,
		Tags:    []string{"fact"},
	}
}

// cleanMatch trims, collapses whitespace, and strips trailing punctuation
// from a regex match to produce a clean observation content string.
func cleanMatch(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ".,;!?")
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return s
}

// ObservationToMemory converts an extracted observation into a domain
// MemoryEntry ready for storage.
func ObservationToMemory(obs ExtractedObservation) *domain.MemoryEntry {
	return &domain.MemoryEntry{
		ID:        domain.NewULID("mem"),
		Content:   obs.Content,
		Tags:      obs.Tags,
		Source:    obs.Source,
		CreatedAt: time.Now().UTC(),
	}
}
