package aggregate

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// SkillRule names one invariant a learned skill draft must hold.
type SkillRule string

const (
	SkillRuleNameClass           SkillRule = "name-class"
	SkillRuleNameIncident        SkillRule = "name-incident"
	SkillRuleDescriptionLength   SkillRule = "description-length"
	SkillRuleDescriptionTrigger  SkillRule = "description-trigger"
	SkillRuleDescriptionIncident SkillRule = "description-incident"
	SkillRuleBodySize            SkillRule = "body-size"
	SkillRuleBodyShape           SkillRule = "body-shape"
	SkillRuleEvidenceKind        SkillRule = "evidence-kind"
	SkillRuleQuoteWords          SkillRule = "quote-words"
	SkillRuleOpShape             SkillRule = "op-shape"
	SkillRuleToolName            SkillRule = "tool-name"
)

// SkillRuleViolation is one rule a draft breaks, with the reason an owner reads.
type SkillRuleViolation struct {
	Rule    SkillRule
	Message string
}

// Limits of a learned skill.
const (
	SkillNameMax        = 64
	SkillDescriptionMax = 160
	SkillBodyMax        = 100 << 10
)

var (
	classLevelName = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	isoDate        = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	ticketRef      = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-\d+\b|#\d+\b`)
	errorCodeName  = regexp.MustCompile(`(^|-)(exit|errno|err|status|code)-\d+(-|$)`)
	useWhen        = regexp.MustCompile(`(?i)\buse when\b`)
	headingLine    = regexp.MustCompile(`(?m)^#{2,3} `)
	numberedStep   = regexp.MustCompile(`(?m)^\d+[.)] `)
)

// SkillQuoteMinWords is how many words a quoted owner line must carry to count as evidence.
const SkillQuoteMinWords = 3

// SkillWords splits text into lowercase words: runs of letters, digits, and their
// combining marks. It is the one definition of a word for quotes and descriptions.
func SkillWords(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
	})
}

// HasDateOrTicket reports whether text carries an absolute date, a ticket key
// such as AB-1234, or an issue reference such as #1234. Lowercase words with
// digits, such as sha-256 or node-22, are version names and do not count.
func HasDateOrTicket(text string) bool {
	return isoDate.MatchString(text) || ticketRef.MatchString(text)
}

// CheckSkillDraft returns every rule the draft breaks, so the owner sees all of
// them at once. A rule that needs a value the draft does not supply, such as the
// kind, op, or evidence kind when it does not parse, is skipped; the caller
// reports that value itself.
func CheckSkillDraft(in SkillProposalInput) []SkillRuleViolation {
	var out []SkillRuleViolation
	add := func(rule SkillRule, format string, args ...any) {
		out = append(out, SkillRuleViolation{Rule: rule, Message: fmt.Sprintf(format, args...)})
	}
	if len(in.Name) > SkillNameMax || !classLevelName.MatchString(in.Name) {
		add(SkillRuleNameClass, "the name is not class level: lowercase words joined by hyphens, at most %d characters", SkillNameMax)
	}
	if HasDateOrTicket(in.Name) || errorCodeName.MatchString(in.Name) {
		add(SkillRuleNameIncident, "the name carries a date, a ticket number, or an error code: a name is a class, not one incident")
	}
	if n := utf8.RuneCountInString(in.Description); n > SkillDescriptionMax {
		add(SkillRuleDescriptionLength, "the description has %d characters; at most %d", n, SkillDescriptionMax)
	}
	if !useWhen.MatchString(in.Description) {
		add(SkillRuleDescriptionTrigger, `the description names no trigger: say when to use the skill with "use when"`)
	}
	if HasDateOrTicket(in.Description) {
		add(SkillRuleDescriptionIncident, "the description carries a date or a ticket number: a skill is a class, not one incident")
	}
	if len(in.Body) > SkillBodyMax {
		add(SkillRuleBodySize, "the body is %d bytes; at most %d", len(in.Body), SkillBodyMax)
	}
	if len(headingLine.FindAllString(in.Body, -1)) < 2 && len(numberedStep.FindAllString(in.Body, -1)) < 2 {
		add(SkillRuleBodyShape, "the body needs two sections or two numbered steps")
	}
	if op, ok := enum.ParseSkillProposalOp(string(in.Op)); ok {
		switch op {
		case enum.SkillProposalCreate:
			if in.TargetName != "" || in.TargetDigest != "" || in.TargetLevel != "" {
				add(SkillRuleOpShape, "a create names no target")
			}
		case enum.SkillProposalPatch:
			switch {
			case in.TargetName == "" || in.TargetDigest == "":
				add(SkillRuleOpShape, "a patch names the skill it rewrites and that skill's digest")
			case in.Name != in.TargetName:
				add(SkillRuleOpShape, "a patch rewrites the skill it names, and its name is not its target's")
			case in.TargetLevel != enum.SkillLevelAgent:
				add(SkillRuleOpShape, "a patch may rewrite only an agent-level skill, and this target is not one")
			}
		}
	}
	kind, kindOK := enum.ParseSkillKind(string(in.Kind))
	evidence, evidenceOK := enum.ParseSkillEvidenceKind(string(in.EvidenceKind))
	if kindOK && evidenceOK {
		switch {
		case kind == enum.SkillKindTool && evidence != enum.SkillEvidenceCall:
			add(SkillRuleEvidenceKind, "a tool skill must rest on a call that worked, not on %s", evidence)
		case kind == enum.SkillKindMethod && evidence == enum.SkillEvidenceCall:
			add(SkillRuleEvidenceKind, "a method has no call to prove: it rests on the owner's words, a rewrite, or two owner turns")
		}
	}
	if evidenceOK && strings.TrimSpace(in.Evidence) == "" {
		add(SkillRuleEvidenceKind, "a skill proposal cites its evidence")
	} else if evidenceOK && evidence == enum.SkillEvidenceQuote {
		// A blank citation is only the missing-evidence violation above, not also a short quote.
		if n := len(SkillWords(in.Evidence)); n < SkillQuoteMinWords {
			add(SkillRuleQuoteWords, "a quote needs at least %d words to be evidence; this one has %d", SkillQuoteMinWords, n)
		}
	}
	if kindOK && kind == enum.SkillKindTool && strings.TrimSpace(in.Tool) == "" {
		add(SkillRuleToolName, "a tool skill names the tool it is about")
	}
	return out
}
