package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinimumDurableBodyRunes is the smallest body the learner may commit as a
// structured memory record. Shorter texts are almost always fragments,
// grunts, or echo of a tool name, not durable knowledge.
const MinimumDurableBodyRunes = 10

// minimumSimilarityTokens is the smallest token-set size for which the
// overlap coefficient is a trustworthy signal. Very short texts can only
// be matched exactly; a single token is never enough to judge overlap.
const minimumSimilarityTokens = 2

// MemorySimilarity returns the overlap coefficient between the word sets of
// two texts: |A∩B| / min(|A|,|B|). It is case-insensitive and ignores
// punctuation, so near-duplicate phrasings ("Untuk integrasi Cursor di
// 9router, ..." vs "Untuk integrasi Cursor di 9router: ...") score close to
// 1 while unrelated topics score 0. Bodies with fewer than
// minimumSimilarityTokens words return 0 (too short to judge by overlap).
func MemorySimilarity(a, b string) float64 {
	ta := memoryTokens(a)
	tb := memoryTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	if min(len(ta), len(tb)) < minimumSimilarityTokens {
		return 0
	}
	set := make(map[string]struct{}, len(ta))
	for _, t := range ta {
		set[t] = struct{}{}
	}
	inter := 0
	seen := make(map[string]struct{}, len(tb))
	for _, t := range tb {
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		if _, ok := set[t]; ok {
			inter++
		}
	}
	denom := min(len(ta), len(tb))
	if denom == 0 {
		return 0
	}
	return float64(inter) / float64(denom)
}

func memoryTokens(s string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			tokens = append(tokens, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsSpace(r), unicode.IsPunct(r):
			flush()
		default:
			// Non-ASCII letters (Bahasa with diacritics, arabic, etc.) are
			// kept as tokens so similarity still works across languages.
			b.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// BodyLooksInterrogative reports whether the text is phrased as a question
// or a rhetorical remark. Questions are never durable memory: they express
// uncertainty, not a rule to apply going forward.
func BodyLooksInterrogative(body string) bool {
	return strings.Contains(strings.TrimSpace(body), "?")
}

// EchoesUserText reports whether a proposed body is (near-)verbatim copy of
// one of the given user texts. The learner must distil user input, never
// replay it as a "preference".
func EchoesUserText(body string, userTexts []string) bool {
	for _, t := range userTexts {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if MemorySimilarity(body, t) >= 0.9 {
			return true
		}
	}
	return false
}

// ValidDurableMemoryBody is the minimum quality bar every typed memory
// upsert must pass, regardless of whether it came from the LLM path or the
// deterministic fallback. It rejects trivia, questions, and verbatim echoes
// of user messages.
func ValidDurableMemoryBody(body string, userTexts []string) bool {
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) < MinimumDurableBodyRunes {
		return false
	}
	if len(memoryTokens(body)) < 3 {
		// A durable statement needs a subject, a predicate, and a
		// consequence ("Tuan suka Go" = 3 tokens). One- or two-token
		// fragments are grunts, tool names, or partial echoes.
		return false
	}
	if BodyLooksInterrogative(body) {
		return false
	}
	if EchoesUserText(body, userTexts) {
		return false
	}
	return true
}

// UserTextsForExperience returns the raw user utterances of an experience:
// the non-steer goal and every steer correction. Bodies must be distilled
// from, not copied from, these texts.
func UserTextsForExperience(exp *Experience) []string {
	if exp == nil {
		return nil
	}
	out := make([]string, 0, 1+len(exp.Corrections))
	if g := strings.TrimSpace(exp.Goal); g != "" {
		out = append(out, g)
	}
	for _, c := range exp.Corrections {
		if t := strings.TrimSpace(c.UserSaid); t != "" {
			out = append(out, t)
		}
	}
	return out
}
