package skill

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

const (
	overlapNote   = 0.5
	overlapReject = 0.8
	repeatWindow  = 30 * 24 * time.Hour
)

// stopWords are shared by nearly every skill description, so they say nothing
// about whether two skills cover the same class.
// sentenceEnd terminates one owner's statement; a quote may match across
// whitespace and punctuation inside a sentence, but never across its end, because
// words spliced from two sentences are not something the owner said.
var sentenceEnd = regexp.MustCompile(`[.!?]+`)

var stopWords = map[string]bool{
	"a": true, "an": true, "and": true, "for": true, "from": true, "in": true, "is": true, "of": true,
	"on": true, "or": true, "the": true, "to": true, "use": true, "when": true, "with": true,
}

// Rule names an application check. Domain invariants keep their own names.
type Rule string

const (
	RuleOp            Rule = "op"
	RuleKind          Rule = "kind"
	RuleOpTarget      Rule = "op-target"
	RuleNameTaken     Rule = "name-taken"
	RuleOrphanRow     Rule = "orphan-row"
	RuleOverlap       Rule = "description-overlap"
	RuleBudget        Rule = "catalog-budget"
	RuleEvidenceCited Rule = "evidence-cited"
)

// Violation is one rule a draft breaks, with the reason the owner reads.
type Violation struct {
	Rule    Rule
	Message string
}

// Draft is what the candidate proposes, as untrusted text. Op and Kind are parsed here.
// Reason is why the candidate thinks the skill is worth the owner's review; it
// persists in the row the owner reads, so the content scan covers it too.
type Draft struct {
	Op          string
	Kind        string
	Name        string
	Description string
	Body        string
	Reason      string
	Target      string
	Cite        Citation
}

// Citation is the evidence the candidate claims, checked against Facts. Tool
// names the tool a call citation is about.
type Citation struct {
	Kind  string
	Quote string
	Call  string
	Tool  string
	Turns []string
}

// Facts is everything this turn and thread show. Only these texts can be cited,
// so page, assistant, file, and other-thread text cannot pass.
type Facts struct {
	OwnerLines []OwnerLine
	Calls      []entity.ToolCall
	Rewrite    bool
}

// OwnerLine is one message the owner wrote in this thread. Turn is the
// message's id — the only handle a citation can name. OpensTurn says the line
// opened its own turn rather than steering one already open: a repeat rests
// on two occasions, and an anchor and its steer are one.
type OwnerLine struct {
	Turn      string
	At        time.Time
	Text      string
	OpensTurn bool
}

// LiveSkill is one skill the library holds in some root.
type LiveSkill struct {
	Root        enum.SkillRoot
	Name        string
	Description string
	Digest      string
	Level       enum.SkillLevel
}

// Check is one draft read against its turn and the live library. Budget is the
// catalog's byte budget, set by the owner; a zero budget is reported.
type Check struct {
	Draft  Draft
	Facts  Facts
	Live   []LiveSkill
	Owned  []string // learned names the loop holds an ownership row for, file or not
	Budget int
}

// Result is the answer. A draft is admitted when Violations is empty. Notes go
// to the reviewer and never reject.
type Result struct {
	Violations []Violation
	Notes      []string
	// admitted and shape are set only by Validate itself, when it ran and found
	// no violation: a Result the caller assembled by hand — the zero value
	// included — can never produce a proposal.
	admitted bool
	shape    aggregate.SkillProposalInput
}

// Validate checks a draft against its turn, the live library, and the domain's
// skill rules, and returns every rule it breaks. Rules that need an op the draft
// does not parse are skipped; the op rule reports it.
// AdmissionScan and AdmissionProveEvidence are the pipe's two teeth: the
// content scan and the evidence proof. They are variables so the eval harness
// — which lives outside this package with the real model — can neutralize one
// at a time in a mutation run and prove the pipe, not the model's mood, is
// what refuses. Production never reassigns them.
var (
	AdmissionScan          = scanContent
	AdmissionProveEvidence = proveEvidence
)

func Validate(c Check) Result {
	var r Result
	report := func(rule Rule, format string, args ...any) {
		r.Violations = append(r.Violations, Violation{Rule: rule, Message: fmt.Sprintf(format, args...)})
	}
	d := c.Draft
	op, opOK := enum.ParseSkillProposalOp(d.Op)
	if !opOK {
		report(RuleOp, "the op is not create or patch")
	}
	kind, kindOK := enum.ParseSkillKind(d.Kind)
	switch {
	case d.Kind == "preference":
		report(RuleKind, "a preference is memory, not a learned skill: write it with memory")
	case !kindOK:
		report(RuleKind, "the kind is not tool or method")
	}
	proven, problem := AdmissionProveEvidence(d.Cite, c.Facts)
	if problem != "" {
		report(RuleEvidenceCited, "%s", problem)
	}
	var target *LiveSkill
	for i := range c.Live {
		if c.Live[i].Root == enum.SkillRootNusashell && c.Live[i].Name == d.Target {
			target = &c.Live[i]
		}
	}
	isPatch := opOK && op == enum.SkillProposalPatch
	if isPatch && target == nil {
		report(RuleOpTarget, "a patch must name the learned skill it rewrites, and its target is not one")
	}
	if opOK && op == enum.SkillProposalCreate {
		taken := false
		for _, s := range c.Live {
			if s.Name == d.Name {
				report(RuleNameTaken, "the name %q is already live in the %s root; rename the proposal or improve that skill by hand", d.Name, s.Root)
				taken = true
				break
			}
		}
		// accept refuses a create over a row that is still there, file or not.
		if !taken && slices.Contains(c.Owned, d.Name) {
			report(RuleOrphanRow, "the name %q is still owned by the loop, but its file is gone; accept refuses a create over a row that is still there", d.Name)
		}
	}
	mine := contentWords(d.Description)
	closest, best := "", 0.0
	total := len(d.Name) + len(d.Description)
	for i := range c.Live {
		// A patch target overlaps itself by construction, so it does not count;
		// identity, not the name — a same-named skill in another root still counts.
		if isPatch && target != nil && &c.Live[i] == target {
			continue
		}
		s := c.Live[i]
		total += len(s.Name) + len(s.Description)
		if sim := jaccard(mine, contentWords(s.Description)); sim >= overlapNote && sim > best {
			closest, best = s.Name, sim
		}
	}
	switch {
	case best >= overlapReject:
		report(RuleOverlap, "the description is the same class as the live skill %q; improve that skill by hand instead", closest)
	case closest != "":
		r.Notes = append(r.Notes, fmt.Sprintf("the description partly overlaps the live skill %q (%.0f%%); the reviewer decides whether it covers the same class", closest, best*100))
	}
	switch {
	case c.Budget <= 0:
		report(RuleBudget, "no catalog budget is set; the owner sets it")
	case total > c.Budget:
		report(RuleBudget, "the catalog would hold %d bytes; the budget is %d", total, c.Budget)
	}
	shape := aggregate.SkillProposalInput{Kind: kind, Tool: d.Cite.Tool, Name: d.Name, Description: d.Description, Body: d.Body, Reason: d.Reason, EvidenceKind: proven, TargetName: d.Target}
	if opOK {
		shape.Op = op
	}
	if target != nil {
		shape.TargetDigest, shape.TargetLevel = target.Digest, target.Level
	}
	switch proven {
	case enum.SkillEvidenceQuote:
		shape.Evidence = d.Cite.Quote
	case enum.SkillEvidenceCall:
		shape.Evidence = d.Cite.Call
	case enum.SkillEvidenceRepeat:
		shape.Evidence = strings.Join(d.Cite.Turns, ", ")
	case enum.SkillEvidenceRewrite:
		shape.Evidence = "the owner's rewrite of this turn"
	}
	for _, v := range aggregate.CheckSkillDraft(shape) {
		// A patch whose target never resolved reports only the missing target:
		// the shape violation it would add has the same cause.
		if isPatch && target == nil && v.Rule == aggregate.SkillRuleOpShape {
			continue
		}
		report(Rule(v.Rule), "%s", v.Message)
	}
	for _, v := range AdmissionScan(d.Name, d.Description, d.Body, d.Reason) {
		r.Violations = append(r.Violations, v)
	}
	if len(r.Violations) == 0 {
		r.admitted, r.shape = true, shape
	}
	if aggregate.HasDateOrTicket(d.Body) {
		r.Notes = append(r.Notes, "the body quotes a date or a ticket number: the reviewer decides whether it is one session's incident")
	}
	return r
}

// Proposal builds the domain proposal one admitted validation carries. The
// admitted flag cannot be set from outside a successful Validate, so forgetting
// to validate cannot produce a proposal — the value it needs does not exist.
func (r Result) Proposal(id string, now time.Time) (*aggregate.SkillProposal, error) {
	if !r.admitted {
		return nil, errors.New("a skill proposal rests on a validation that admitted the draft")
	}
	return aggregate.NewSkillProposal(id, r.shape, now)
}

// proveEvidence checks the citation against the facts and returns the evidence
// kind they prove, or the reason they do not.
func proveEvidence(cite Citation, f Facts) (enum.SkillEvidenceKind, string) {
	kind, ok := enum.ParseSkillEvidenceKind(cite.Kind)
	if !ok {
		return "", "the evidence kind is not quote, rewrite, repeat, or call"
	}
	switch kind {
	case enum.SkillEvidenceQuote:
		quote := aggregate.SkillWords(cite.Quote)
		if len(quote) == 0 {
			return "", "the citation quotes no words"
		}
		for _, l := range f.OwnerLines {
			for _, sentence := range sentenceEnd.Split(l.Text, -1) {
				line := aggregate.SkillWords(sentence)
				for i := 0; i+len(quote) <= len(line); i++ {
					if slices.Equal(line[i:i+len(quote)], quote) {
						return kind, ""
					}
				}
			}
		}
		return "", "the quoted words are not an owner line of this thread"
	case enum.SkillEvidenceRewrite:
		if f.Rewrite {
			return kind, ""
		}
		return "", "no owner rewrite was captured in this turn"
	case enum.SkillEvidenceRepeat:
		if len(cite.Turns) != 2 || cite.Turns[0] == cite.Turns[1] {
			return "", "a repeat cites two distinct owner turns"
		}
		var at [2]time.Time
		for i, turn := range cite.Turns {
			found := false
			for _, l := range f.OwnerLines {
				if l.Turn == turn {
					if !l.OpensTurn {
						return "", "a cited line steered its turn instead of opening one; a repeat rests on two occasions, not two lines of one occasion"
					}
					at[i], found = l.At, true
					break
				}
			}
			if !found {
				return "", "a cited turn has no owner line in this thread"
			}
			if at[i].IsZero() {
				return "", "a cited owner turn carries no time, so its distance cannot be checked"
			}
		}
		if at[0].Before(at[1].Add(-repeatWindow)) || at[0].After(at[1].Add(repeatWindow)) {
			return "", "the cited owner turns are more than 30 days apart"
		}
		return kind, ""
	case enum.SkillEvidenceCall:
		if cite.Call == "" {
			return "", "the cited call is not one of this turn's model calls"
		}
		if cite.Tool == "" {
			return "", "the citation names no tool; name the tool the cited call used"
		}
		for _, tc := range f.Calls {
			if !modelCall(tc) || tc.ID() != cite.Call {
				continue
			}
			switch {
			case tc.Name() != cite.Tool:
				return "", fmt.Sprintf("the cited call is a call to %q, not to the tool the citation names", tc.Name())
			case tc.Failed():
				return "", "the cited call failed; a skill rests only on a call that worked"
			case !tc.HasOutput():
				return "", "the cited call has no recorded result to rest on"
			}
			return kind, ""
		}
		return "", "the cited call is not one of this turn's model calls"
	}
	return "", fmt.Sprintf("the evidence kind %q cannot be proven", kind)
}

// contentWords are the words that tell two descriptions apart.
func contentWords(desc string) map[string]bool {
	set := map[string]bool{}
	for _, w := range aggregate.SkillWords(desc) {
		if !stopWords[w] {
			set[w] = true
		}
	}
	return set
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}
