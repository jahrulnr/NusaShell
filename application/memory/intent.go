package memory

import (
	"regexp"
	"strings"
)

// recallIntentRe matches phrases in English and Indonesian that signal the
// user is referring to past conversation context or prior work — a recall
// intent. Inspired by OpenClaw's escalation.ts recall triggers.
//
// English: recall, remember, previously, previous, earlier, last time,
// we discussed, yesterday, last week, as mentioned, as we discussed,
// said before, before that.
//
// Indonesian: sebelumnya, sebelum itu, kemarin, kemaren, ingat, pernah,
// waktu itu, itu lalu, tadi.
var recallIntentRe = regexp.MustCompile(`(?i)\b(?:` +
	`recall|remember|previously|previous|earlier|last time|we discussed|` +
	`yesterday|last week|as mentioned|as we discussed|said before|before that|` +
	`sebelumnya|sebelum itu|kemarin|kemaren|ingat|pernah|waktu itu|itu lalu|tadi` +
	`)\b`)

// HasRecallIntent reports whether text contains phrases indicating the user
// is referring to past conversation context or prior work. Used by the async
// semantic lane to decide whether to run a deeper embedding-based search
// even when the BM25 turn-start scan already found hits.
func HasRecallIntent(text string) bool {
	return recallIntentRe.MatchString(strings.ToLower(text))
}
