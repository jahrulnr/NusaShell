package skill

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/redact"
)

// Rules of the content scan: both refuse, because a skill carrying either one
// becomes a standing instruction or a stored secret in every later turn.
const (
	RuleContentCredential Rule = "content-credential"
	RuleContentDangerous  Rule = "content-dangerous"
)

// Dangerous instructions are a verb on a target, never a bare keyword: naming a
// secret store is not an instruction to read it. Each regex is one class.
var dangerClasses = []struct {
	name string
	re   *regexp.Regexp
}{
	{"a credential read", regexp.MustCompile(`(?i)\bsecurity\s+find-generic-password\b|\b(cat|less|head|tail|read|show|source|grep|cp|scp|curl|printenv|export)\b.{0,60}(\.ssh/id_|\.aws/credentials|\.netrc|\.pgpass|/etc/shadow|cookies|login\s+data)`)},
	{"an agent-config write", regexp.MustCompile(`(?i)>{1,2}\s*\S*(agents\.md|claude\.md|\.claude/|\.cursor/|\.mcp\.json|\.config/nusashell|\.agents/)|\b(tee|write|append|overwrite|patch|edit|update)\b.{0,60}(agents\.md|claude\.md|\.claude/|\.cursor/|\.mcp\.json|\.config/nusashell|\.agents/)`)},
	{"a piped download", regexp.MustCompile(`(?i)\b(curl|wget)\b.{0,120}\|\s*(sudo\s+(-[A-Za-z]+\s+)*)?(env\s+)?(\S+/)?(sh|bash|zsh|fish|python3?|node|ruby|perl)\b`)},
	{"an instruction override", regexp.MustCompile(`(?i)\b(ignore|disregard)\s+(all\s+(of\s+)?)?(the\s+|your\s+)?(previous|prior|above|earlier)\s+(instructions?|rules?|prompt)\b|\byou are now\b|\bsystem prompt\b`)},
}

// A match is excused when a negator precedes it in the same sentence — a skill
// warning against a command is not issuing it — unless a clause boundary stands
// between the negator and the command: "avoid the queue; instead run …" still
// issues it.
var (
	negator        = regexp.MustCompile(`(?i)\b(never|don't|do not|avoid|must not|should not|no)\b`)
	clauseBoundary = regexp.MustCompile(`(?i);|,\s*then|\sbut\s`)
)

// Accepted heuristic limits, so the next reader does not "fix" them blind:
// a URL ending in ? or ! still splits as a sentence end — prose uses the same
// characters and they cannot be told apart; a double negation ("no reason not
// to") is excused like any negation; and plain-comma or "and then" transitions
// are not clause boundaries. Above all: do not replace the splitter with a
// cleverer sentence detector — splitting on bare periods shreds the targets
// these patterns exist to find (.netrc, AGENTS.md, .mcp.json) and every class
// goes silent at once.
//
// scanContent checks one skill's fields for credential-shaped strings and
// dangerous standing instructions. Messages name the class and the field, never
// the matched text: a violation reason is persistent, and a credential must not
// end up inside it.
func scanContent(name, description, body, reason string) []Violation {
	var out []Violation
	for _, f := range []struct{ label, text string }{
		{"the name", name}, {"the description", description}, {"the body", body}, {"the reason", reason},
	} {
		joined := strings.ReplaceAll(f.text, "\\\n", "")
		if redact.CredentialShaped(f.text) || redact.CredentialShaped(joined) {
			out = append(out, Violation{RuleContentCredential, fmt.Sprintf("%s carries a credential-shaped string; a skill never embeds secrets", f.label)})
		}
		for _, class := range dangerClasses {
			if instructed(f.text, class.re) || instructed(joined, class.re) {
				out = append(out, Violation{RuleContentDangerous, fmt.Sprintf("%s carries a dangerous instruction: %s", f.label, class.name)})
			}
		}
	}
	return out
}

// statementEnd ends one statement for the negation scan: sentence-ending
// punctuation followed by whitespace, or a line break. A bare period is not one —
// the targets of these commands are full of them (.ssh/id_, .netrc, AGENTS.md),
// and splitting there would shred the patterns before they can match. Quotes use
// the stricter sentenceEnd instead, because a splice is a fabrication even
// without the trailing space.
var statementEnd = regexp.MustCompile(`[.!?]+["')*\]]*\s+|\n+`)

// instructed reports whether text issues the command, looking one statement at a
// time so a warning in one does not excuse the command in the next.
func instructed(text string, re *regexp.Regexp) bool {
	for _, sentence := range statementEnd.Split(text, -1) {
		matches := re.FindAllStringIndex(sentence, -1)
		if len(matches) == 0 {
			continue
		}
		// Negation positions are precomputed once per statement: rescanned per
		// match, the check is quadratic on adversarial repetition.
		negs := negator.FindAllStringIndex(sentence, -1)
		bounds := clauseBoundary.FindAllStringIndex(sentence, -1)
		for _, m := range matches {
			if !excused(negs, bounds, m[0]) {
				return true
			}
		}
	}
	return false
}

// excused reports whether the nearest preceding negation reaches the match at
// start: it does when no clause boundary stands between the negator's end and
// the match. If even the nearest negation does not reach, no earlier one can.
func excused(negs, bounds [][]int, start int) bool {
	i := sort.Search(len(negs), func(i int) bool { return negs[i][1] > start }) - 1
	if i < 0 {
		return false
	}
	j := sort.Search(len(bounds), func(j int) bool { return bounds[j][0] >= negs[i][1] })
	return j >= len(bounds) || bounds[j][0] >= start
}
