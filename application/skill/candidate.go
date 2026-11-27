package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

const (
	// maxOpenProposals gates the pass: three or more proposals waiting for the
	// owner means the queue is full, and adding to it asks the owner twice.
	maxOpenProposals = 3
	// earlierOwnerLines is how many owner messages of the thread before this
	// turn ride the digest, so a repetition claim can cite them.
	earlierOwnerLines = 3
	// candidateCooldown is how long a decided proposal's name is not proposed
	// again. It mirrors the repeat window: inside a month, the owner's
	// decision still speaks. A retirement the pass wrote, and a close of a
	// proposal that had already gone stale, are not refusals and hold nothing.
	// The same window bounds the refusals the prompt shows.
	candidateCooldown = 30 * 24 * time.Hour
	// maxDeclined bounds the refusals the prompt shows, newest first. A sixth
	// refusal pushes the oldest out of the block.
	maxDeclined = 5
	// declinedClip bounds one refusal's reason in the prompt. It is the owner's
	// own text, so the model reads its gist, not a paragraph of it.
	declinedClip = 200
	// digestCapBytes bounds the turn digest — the conversation content the
	// owner pays the utility model to read. Assistant and tool text is never
	// admissible evidence, so paying for all of it would be waste, not cost.
	digestCapBytes = 32_000
	// catalogDescClip defensively bounds one catalog description in the
	// prompt. Loop-written skills validate to 160; skills of other roots
	// never passed that gate, and the prompt's cost is the owner's.
	catalogDescClip = 160
)

// Generator asks the utility model for one plain-text answer — the same seam
// consolidation uses, declared here because the candidate owns its own call.
type Generator func(ctx context.Context, convID, system, user string) (string, error)

// CandidateDeps are the pass's collaborators. Generate is the utility model and
// Decisions is the store that retires a superseded proposal; a nil one of either
// means the pass is unavailable, never a panic.
type CandidateDeps struct {
	Catalog   drepo.Skills
	Proposals drepo.SkillProposals
	Ownership drepo.SkillOwnership
	Decisions drepo.SkillDecisions
	Generate  Generator
	Budget    int
	Log       *slog.Logger
	// Folders lists the other workspace folders a learned name must be free
	// in. Nil means this workspace is the only one.
	Folders SkillFolders
	// Writer renders a pending create's document, so the pass can tell the
	// loop's own leftover from a collision. Nil means no file is read that way.
	Writer drepo.SkillWriter
}

// CandidateInput is one finished turn the gate judged worth a pass. Messages
// is the turn's snapshot, cloned by the gate before the pass starts — the pass
// runs async and the live conversation keeps moving under it, so the aggregate
// itself never crosses this boundary. Rewrote says whether the evidence store
// captured the owner's rewrite of this turn's anchor.
type CandidateInput struct {
	ConvID    string
	Workspace string
	Messages  []entity.Message
	Rewrote   bool
	Now       time.Time
}

// CandidateOutcome is how one pass ended, for the pass log and the caller.
type CandidateOutcome string

const (
	CandidateStored        CandidateOutcome = "stored"
	CandidateNone          CandidateOutcome = "none"
	CandidateRejected      CandidateOutcome = "rejected"
	CandidateSkipped       CandidateOutcome = "skipped"
	CandidateCooldown      CandidateOutcome = "cooldown"
	CandidateUnavailable   CandidateOutcome = "unavailable"
	CandidateTargetChanged CandidateOutcome = "target-changed"
)

// Candidate reads one finished turn and, when the evidence holds, stores one
// learned-skill proposal for the owner. The model writes the candidate and
// the deterministic validator judges it — neither writer nor judge is the
// same actor. A pending proposal of the same name that the owner can no longer
// accept is retired first, with its cause, so it cannot hold the name for
// nothing. The target is read once more just before the store: a proposal
// whose target moved while the model answered is not stored, and the outcome
// names the move. A failure anywhere means no new row and one log line, never
// a retried pass: the next gated turn tries again.
func Candidate(ctx context.Context, deps CandidateDeps, in CandidateInput) CandidateOutcome {
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	outcome := func(o CandidateOutcome, kv ...any) CandidateOutcome {
		args := append([]any{"conv", in.ConvID, "outcome", string(o)}, kv...)
		log.Info("skill_candidate", args...)
		return o
	}

	w, ok := TurnWindow(in.Messages)
	if !ok {
		return outcome(CandidateSkipped, "why", "no owner line opens the turn")
	}
	// A dead-end turn — a tool failure that never recovered, which the owner
	// never answered — has no working procedure to teach and no owner
	// judgment to cite. That is a fact about the turn, so it is enforced
	// here, before a single store read or model cent — not in the prompt.
	// An owner line after the failure means the turn was steered, and the
	// owner's words are the strongest evidence this feature has.
	if Detect(w, in.Rewrote).DeadEnd {
		return outcome(CandidateSkipped, "why", "the turn ends in an unanswered tool failure")
	}
	pending, err := deps.Proposals.Pending(ctx)
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error())
	}
	// Only a proposal the owner can still accept holds the queue. A stale one
	// stays pending and counted on its skill's row, but accept refuses it, so it
	// must not stop the pass from proposing anything else.
	if len(pending) >= maxOpenProposals {
		open, err := openProposals(ctx, deps, in.Workspace, pending)
		if err != nil {
			return outcome(CandidateUnavailable, "err", err.Error())
		}
		if open >= maxOpenProposals {
			return outcome(CandidateSkipped, "why", "the owner's queue is full", "pending", len(pending), "open", open)
		}
	}
	catalog, err := deps.Catalog.List(ctx, in.Workspace)
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error())
	}
	// The model sees this workspace's effective catalog. Whether a proposal is
	// still acceptable is read again, across every workspace folder, just
	// before anything is stored: a name written into the nusashell root is
	// visible from all of them. The validator judges this snapshot. The model
	// call in between can be long, and movedSince reads the world again.
	rows, err := deps.Ownership.List(ctx)
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error())
	}
	if deps.Generate == nil {
		return outcome(CandidateUnavailable, "why", "no utility model is wired")
	}
	if deps.Decisions == nil {
		return outcome(CandidateUnavailable, "why", "no decision store is wired")
	}
	if deps.Budget <= 0 {
		// Validation reports an unset budget for every draft, so the model
		// call could never produce a row — and the call is the owner's money.
		return outcome(CandidateSkipped, "why", "no catalog budget is set")
	}

	earlier := earlierOwners(in.Messages, w)
	turn, shown, calls := candidateDigest(w, earlier, in.Rewrote)
	declined, err := deps.Proposals.Declined(ctx, in.Now.Add(-candidateCooldown), maxDeclined)
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error())
	}
	user := candidatePrompt(turn, shown, pending, declined, catalog.Skills, deps.Budget)
	raw, err := deps.Generate(ctx, in.ConvID, prompt.SystemPrompt("skill-candidate"), user)
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error())
	}
	draft, found, malformed := parseCandidateReply(raw)
	switch {
	case malformed:
		return outcome(CandidateUnavailable, "why", "the utility model answered no parseable document")
	case !found:
		return outcome(CandidateNone)
	}

	facts := candidateFacts(w, shown, calls, in.Rewrote)
	liveRows, err := candidateLive(ctx, deps, catalog.Skills)
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error())
	}
	res := Validate(Check{
		Draft:  draft,
		Facts:  facts,
		Live:   liveRows,
		Owned:  learnedNames(rows),
		Budget: deps.Budget,
	})
	if len(res.Violations) > 0 {
		rules := make([]string, len(res.Violations))
		for i, v := range res.Violations {
			rules[i] = string(v.Rule)
		}
		return outcome(CandidateRejected, "rules", strings.Join(rules, ","))
	}
	p, err := res.Proposal("", in.Now)
	if err != nil {
		return outcome(CandidateRejected, "why", err.Error())
	}
	// Only a candidate the validator passed moves the store: the retirement is
	// for the name this pass is about to propose, never for a refused draft.
	retired, err := retireSuperseded(ctx, deps, in.Workspace, draft.Name, in.Now)
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error())
	}
	// The cooldown is the candidate's own gate, not the validator's: a name the
	// owner decided inside the window — or one already pending and still open —
	// is not asked about twice, however the model phrased it. It runs after the
	// retirement, so what it finds pending is what the owner can still decide.
	op, _ := enum.ParseSkillProposalOp(draft.Op)
	seen, err := deps.Proposals.Seen(ctx, draft.Name, op, in.Now.Add(-candidateCooldown))
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error())
	}
	if seen {
		return outcome(CandidateCooldown, "retired", retired)
	}
	why, err := movedSince(ctx, deps, in.Workspace, p)
	if err != nil {
		return outcome(CandidateUnavailable, "err", err.Error(), "retired", retired)
	}
	if why != "" {
		return outcome(CandidateTargetChanged, "why", why, "retired", retired)
	}
	if err := deps.Proposals.Add(ctx, p); err != nil {
		// Another pass storing the same name first is the only refusal left —
		// validation already answered everything else.
		return outcome(CandidateRejected, "why", "the store refused it", "retired", retired)
	}
	return outcome(CandidateStored, "name", draft.Name, "op", draft.Op, "retired", retired)
}

// retireSuperseded retires the pending proposal that holds name, when the owner
// can no longer accept it: the state it was read against has moved, so accept
// would refuse it, and it would hold the name for nothing. A name holds at most
// one pending row, so there is at most one to retire. It returns the retired id,
// or "" when nothing was retired. A proposal the owner can still accept is left
// alone: it holds the name, and the cooldown says so. The catalog is read here,
// not taken from the start of the pass: the model call in between can be long,
// and a retirement judges the skill as it stands now.
func retireSuperseded(ctx context.Context, deps CandidateDeps, workspace, name string, now time.Time) (string, error) {
	pending, err := deps.Proposals.Pending(ctx)
	if err != nil {
		return "", err
	}
	var held *aggregate.SkillProposal
	for _, p := range pending {
		if p.Name() == name {
			held = p
			break
		}
	}
	if held == nil {
		return "", nil
	}
	st, err := learnedNow(ctx, deps.Catalog, deps.Folders, deps.Ownership, deps.Writer, workspace, []*aggregate.SkillProposal{held})
	if err != nil {
		return "", err
	}
	if st.open(held) {
		return "", nil
	}
	if err := held.Supersede(st.supersedeCause(held), now); err != nil {
		return "", err
	}
	if err := releaseOwnFile(ctx, deps.Writer, st, held); err != nil {
		return "", err
	}
	if err := deps.Decisions.Commit(ctx, held, nil); err != nil {
		return "", err
	}
	return held.ID(), nil
}

// movedSince reads a proposal's target once more, just before the store. The
// draft was read against the catalog the model saw, and the model call in
// between can be long. A proposal whose target has moved is one accept would
// refuse, so it is not stored. The reason names the move; "" means nothing
// moved. A create moves when its name is taken on disk or claimed by a row; a
// patch moves when the target no longer reads as a skill, its digest changed, or
// its row no longer describes the file at agent level (SkillProposal.Changes).
func movedSince(ctx context.Context, deps CandidateDeps, workspace string, p *aggregate.SkillProposal) (string, error) {
	catalog, taken, err := catalogView(ctx, deps.Catalog, deps.Folders, workspace)
	if err != nil {
		return "", err
	}
	rows, err := deps.Ownership.List(ctx)
	if err != nil {
		return "", err
	}
	st := newLearnedState(catalog.Skills, rows, taken)
	if p.Op() == enum.SkillProposalCreate {
		if root, ok := st.taken[p.Name()]; ok {
			return "the name is live in the " + root + " root while the model answered", nil
		}
		if st.rows[p.Name()] != nil {
			return "the name was claimed while the model answered", nil
		}
		return "", nil
	}
	digest, live := st.onDisk[p.TargetName()]
	switch {
	case !live:
		return "the target no longer reads as a skill while the model answered", nil
	case digest != p.TargetDigest():
		return "the target changed while the model answered", nil
	case p.Changes(st.rows[p.TargetName()]):
		return "the target's ownership row changed while the model answered", nil
	}
	return "", nil
}

// learnedNames are the names the loop holds an ownership row for, file on disk
// or not.
func learnedNames(rows []*aggregate.SkillOwnership) []string {
	var names []string
	for _, r := range rows {
		if r.Root() == enum.SkillRootNusashell {
			names = append(names, r.Name())
		}
	}
	return names
}

// parseCandidateReply reads the model's document. The first op is the
// candidate; anything past it is dropped, because one turn stores at most one.
func parseCandidateReply(raw string) (Draft, bool, bool) {
	var reply struct {
		Ops []struct {
			Op          string `json:"op"`
			Kind        string `json:"kind"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Body        string `json:"body"`
			Reason      string `json:"reason"`
			Target      string `json:"target"`
			Evidence    struct {
				Kind  string   `json:"kind"`
				Quote string   `json:"quote"`
				Call  string   `json:"call"`
				Tool  string   `json:"tool"`
				Turns []string `json:"turns"`
			} `json:"evidence"`
		} `json:"ops"`
	}
	text := strings.TrimSpace(raw)
	if text == "" {
		return Draft{}, false, false
	}
	if i, j := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}'); i >= 0 && j > i {
		text = text[i : j+1] // past a code fence or a sentence the model added
	}
	if err := json.Unmarshal([]byte(text), &reply); err != nil {
		return Draft{}, false, true
	}
	if len(reply.Ops) == 0 {
		return Draft{}, false, false
	}
	o := reply.Ops[0]
	return Draft{
		Op: o.Op, Kind: o.Kind, Name: o.Name, Description: o.Description, Body: o.Body,
		Reason: o.Reason, Target: o.Target,
		Cite: Citation{Kind: o.Evidence.Kind, Quote: o.Evidence.Quote, Call: o.Evidence.Call, Tool: o.Evidence.Tool, Turns: o.Evidence.Turns},
	}, true, false
}

// earlierOwners collects up to earlierOwnerLines owner messages of the thread
// before the window's anchor, oldest first.
func earlierOwners(msgs []entity.Message, w Window) []entity.Message {
	var before []entity.Message
	for _, m := range msgs {
		if m.ID() == w.AnchorID {
			break
		}
		if m.Editable() {
			before = append(before, m)
		}
	}
	if len(before) > earlierOwnerLines {
		before = before[len(before)-earlierOwnerLines:]
	}
	return before
}

// digestItem is one block of the turn digest. Owner lines are never dropped;
// every other message is one droppable round, and its calls leave the facts
// with it — a round the model never saw can never be its evidence.
type digestItem struct {
	owner bool
	text  string
	calls []entity.ToolCall
}

// turnItems renders the window as digest blocks: the opening owner line first,
// then the turn's messages — owner lines verbatim, the rest clipped. Owner and
// call ids are visible because they are all a citation may rest on. A call
// without recorded output says so: it can never be evidence, and the model
// reading "ok" would cite it.
func turnItems(w Window) []digestItem {
	items := []digestItem{{owner: true, text: fmt.Sprintf("owner #%s: %s\n", w.AnchorID, w.AnchorText)}}
	for _, m := range w.Msgs {
		if m.Editable() {
			items = append(items, digestItem{owner: true, text: fmt.Sprintf("owner #%s: %s\n", m.ID(), m.Content())})
			continue
		}
		var b strings.Builder
		if m.Role() == enum.RoleAssistant {
			fmt.Fprintf(&b, "assistant: %s\n", clip(m.Content(), digestClip))
		} else {
			fmt.Fprintf(&b, "other: %s\n", clip(m.Content(), digestClip))
		}
		var calls []entity.ToolCall
		for _, tc := range m.ToolCalls() {
			if !modelCall(tc) {
				continue
			}
			state := "ok"
			switch {
			case tc.Failed():
				state = "failed"
			case !tc.HasOutput():
				state = "no-output"
			}
			fmt.Fprintf(&b, "tool %s #%s %s: %s\n", tc.Name(), tc.ID(), state, clip(tc.Output(), digestClip))
			calls = append(calls, tc)
		}
		items = append(items, digestItem{text: b.String(), calls: calls})
	}
	return items
}

func earlierLineBytes(m entity.Message) int {
	return len(m.Content()) + len(m.ID()) + 16 // the date and markup of its rendered line
}

// candidateDigest builds the turn section under the digest cap, and everything
// the facts may carry. The cap never trims an owner line of the turn — the
// anchor and its steers are the evidence the pass exists for — so trimming
// drops the oldest assistant/tool rounds first, then the oldest earlier owner
// lines, and stops as soon as the digest fits. What was dropped is said in the
// digest itself, and the same survivors feed the facts: one source, so a line
// or call the model never saw can never validate. When the owner lines alone
// exceed the cap they win — trimming them would read the owner's words out of
// the owner's own evidence.
func candidateDigest(w Window, earlier []entity.Message, rewrote bool) (string, []entity.Message, []entity.ToolCall) {
	items := turnItems(w)
	if rewrote {
		// The model may cite a rewrite only when one happened — and it cannot
		// cite what it cannot see, so the digest says it at the anchor.
		items[0].text += "the owner rewrote this line after seeing the first answer\n"
	}
	size := 0
	for _, it := range items {
		size += len(it.text)
	}
	earlierSize := 0
	for _, m := range earlier {
		earlierSize += earlierLineBytes(m)
	}

	dropped := 0
	for size+earlierSize > digestCapBytes {
		oldest := -1
		for i := range items {
			if !items[i].owner && items[i].text != "" {
				oldest = i
				break
			}
		}
		if oldest < 0 {
			break
		}
		size -= len(items[oldest].text)
		items[oldest].text, items[oldest].calls = "", nil
		dropped++
	}
	shown := earlier
	for size+earlierSize > digestCapBytes && len(shown) > 0 {
		earlierSize -= earlierLineBytes(shown[0])
		shown = shown[1:]
	}

	var turn strings.Builder
	for i, it := range items {
		if i == 1 && dropped > 0 {
			fmt.Fprintf(&turn, "the oldest %d rounds of this turn were dropped for size\n", dropped)
		}
		turn.WriteString(it.text)
	}
	var calls []entity.ToolCall
	for _, it := range items {
		calls = append(calls, it.calls...)
	}
	return turn.String(), shown, calls
}

// candidatePrompt renders the pass's input: the turn, the surviving earlier
// owner lines, the clipped catalog with its byte accounting, the proposals
// pending now, and the owner's recent refusals with their reasons.
func candidatePrompt(turn string, shown []entity.Message, pending, declined []*aggregate.SkillProposal, catalog []drepo.Skill, budget int) string {
	var b strings.Builder
	if len(shown) > 0 {
		b.WriteString("<earlier_owner_lines>\n")
		for _, m := range shown {
			fmt.Fprintf(&b, "owner #%s (%s): %s\n", m.ID(), m.CreatedAt().Format("2006-01-02"), m.Content())
		}
		b.WriteString("</earlier_owner_lines>\n\n")
	}
	b.WriteString("<turn>\n" + turn + "</turn>\n\n<skill_index>\n")
	used := 0
	for _, s := range catalog {
		used += len(s.Name) + len(s.Description)
		fmt.Fprintf(&b, "%s (%s) — %s\n", s.Name, s.Source, clip(s.Description, catalogDescClip))
	}
	fmt.Fprintf(&b, "catalog bytes %d of budget %d\n", used, budget)
	b.WriteString("</skill_index>\n\n<pending_proposals>\n")
	for _, p := range pending {
		fmt.Fprintf(&b, "%s (%s) — %s\n", p.Name(), p.Op(), clip(p.Description(), catalogDescClip))
	}
	b.WriteString("</pending_proposals>\n\n<declined>\n")
	fmt.Fprintf(&b, "the owner's refusals from the last %d days, newest first, at most %d; a reason is the owner's decision, not evidence\n",
		int(candidateCooldown.Hours()/24), maxDeclined)
	for _, p := range declined {
		fmt.Fprintf(&b, "%s (%s) — %s\n", p.Name(), p.Op(), clip(p.DismissReason(), declinedClip))
	}
	b.WriteString("</declined>\n")
	return b.String()
}

// candidateFacts collects what the validator may count as evidence — exactly
// what the digest shows: this turn's owner lines and calls, the earlier owner
// lines, and whether the owner rewrote this turn.
func candidateFacts(w Window, earlier []entity.Message, calls []entity.ToolCall, rewrote bool) Facts {
	f := Facts{Rewrite: rewrote, TurnAt: w.AnchorAt}
	seen := map[string]bool{}
	// The opening owner line is not in Msgs — the window starts after it —
	// and it is the line most citations rest on.
	f.OwnerLines = append(f.OwnerLines, OwnerLine{Turn: w.AnchorID, At: w.AnchorAt, Text: w.AnchorText, OpensTurn: true})
	seen[w.AnchorID] = true
	for _, m := range w.Msgs {
		if m.Editable() && !seen[m.ID()] {
			seen[m.ID()] = true
			// A line inside the window steered this turn; the window's one
			// occasion is the anchor's.
			f.OwnerLines = append(f.OwnerLines, OwnerLine{Turn: m.ID(), At: m.CreatedAt(), Text: m.Content(), OpensTurn: false})
		}
	}
	f.Calls = calls
	for _, m := range earlier {
		if !seen[m.ID()] {
			f.OwnerLines = append(f.OwnerLines, OwnerLine{Turn: m.ID(), At: m.CreatedAt(), Text: m.Content(), OpensTurn: m.InputID() == ""})
		}
	}
	return f
}

// candidateLive joins the catalog with the ownership rows and reads each
// learned skill's level through aggregate.SkillStandingOf — the same rule the
// Skills page reads. A row whose digest no longer matches the file on disk says
// nothing about that file, so a hand-edited skill is the owner's here, and the
// validator refuses a patch of it before a proposal exists. Only a missing row
// takes the owner default; a store that cannot answer fails the pass instead of
// misclassifying an agent-level target as the owner's.
func candidateLive(ctx context.Context, deps CandidateDeps, catalog []drepo.Skill) ([]LiveSkill, error) {
	live := make([]LiveSkill, 0, len(catalog))
	for _, s := range catalog {
		l := LiveSkill{
			Root: enum.SkillRoot(s.Source), Name: s.Name,
			Description: s.Description, Digest: s.Digest,
		}
		var row *aggregate.SkillOwnership
		if l.Root == enum.SkillRootNusashell {
			found, err := ownershipRow(ctx, deps, s.Name)
			if err != nil {
				return nil, err
			}
			row = found
		}
		l.Level = aggregate.SkillStandingOf(row, s.Digest).Level
		live = append(live, l)
	}
	return live, nil
}

// learnedState is what the open rule reads about learned skills: the digest of
// each nusashell skill's file on disk, each one's ownership row, and the root
// of every skill name in any root. Only nusashell is the loop's to write, so
// only its entries are in onDisk and rows. A create's name must be free in
// every root, except that the loop's own leftover in nusashell is not a
// collision (see own).
type learnedState struct {
	onDisk map[string]string
	rows   map[string]*aggregate.SkillOwnership
	taken  map[string]string
	// elsewhere names every skill held outside the nusashell root: the focus
	// catalog's other roots and the other workspace folders. A create whose name
	// is here is refused, whatever the nusashell root holds.
	elsewhere map[string]string
	// own holds the ids of the proposals whose nusashell file is the loop's own
	// leftover from a failed commit: the file holds the document the proposal
	// would write, and no row records that document. A create's name has no row;
	// a patch's row still describes the file the patch read. Closing a create
	// archives its leftover. A patch's stays, and its stale cause says the loop
	// wrote it.
	own map[string]bool
}

// newLearnedState takes the catalog and ownership reads the page and the gate
// already hold. own is empty until withOwnDocs reads the documents.
func newLearnedState(skills []drepo.Skill, rows []*aggregate.SkillOwnership, also map[string]string) learnedState {
	st := learnedState{
		onDisk:    map[string]string{},
		rows:      map[string]*aggregate.SkillOwnership{},
		taken:     map[string]string{},
		elsewhere: map[string]string{},
		own:       map[string]bool{},
	}
	for _, s := range skills {
		if _, ok := st.taken[s.Name]; !ok {
			st.taken[s.Name] = s.Source
		}
		if s.Source == string(enum.SkillRootNusashell) {
			st.onDisk[s.Name] = s.Digest
		} else if _, ok := st.elsewhere[s.Name]; !ok {
			st.elsewhere[s.Name] = s.Source
		}
	}
	// also is the catalog port's taken: every name and the root that holds it,
	// a holder outside nusashell preferred over a learned one. Any holder outside
	// nusashell makes the name elsewhere, whatever nusashell holds under it.
	for name, root := range also {
		if cur, ok := st.taken[name]; !ok || (cur == string(enum.SkillRootNusashell) && root != string(enum.SkillRootNusashell)) {
			st.taken[name] = root
		}
		if _, ok := st.elsewhere[name]; !ok && root != string(enum.SkillRootNusashell) {
			st.elsewhere[name] = root
		}
	}
	for _, r := range rows {
		if r.Root() == enum.SkillRootNusashell {
			st.rows[r.Name()] = r
		}
	}
	return st
}

// open reports whether the owner can still accept a pending proposal, with the
// checks accept makes before it writes (application/skill/decide.go): a patch
// needs its target to be a live learned skill whose file still holds the digest
// the patch was read against, and whose row still describes that file at agent
// level (SkillProposal.Changes); a create needs its name free in every root and
// no ownership row, or else the loop's own leftover in nusashell. Budget is left
// out: it is the owner's to make room for, not a fact about the proposal. A
// proposal that is not open is not deleted or hidden. It stays pending and
// labelled stale on its skill's row, and only stops holding the queue.
func (st learnedState) open(p *aggregate.SkillProposal) bool {
	if p.Op() == enum.SkillProposalPatch {
		digest, live := st.onDisk[p.TargetName()]
		return live && digest == p.TargetDigest() && !p.Changes(st.rows[p.TargetName()])
	}
	if _, held := st.elsewhere[p.Name()]; held || st.rows[p.Name()] != nil {
		return false
	}
	if _, onDisk := st.onDisk[p.Name()]; onDisk {
		return st.own[p.ID()]
	}
	return true
}

// withOwnDocs marks the pending proposals whose nusashell file is the loop's own
// leftover (see own). A create needs no row for its name. A patch needs the row
// it was read against still describing that file, and the file moved to other
// bytes: a row that already holds those bytes is a decision that was recorded,
// so nothing is left to attribute. The writer renders the document, so this is
// the same byte comparison Write makes. A nil writer marks nothing.
func (st learnedState) withOwnDocs(writer drepo.SkillWriter, pending []*aggregate.SkillProposal) (learnedState, error) {
	if writer == nil {
		return st, nil
	}
	for _, p := range pending {
		name := p.Name()
		if p.Op() == enum.SkillProposalPatch {
			name = p.TargetName()
		}
		onDisk, ok := st.onDisk[name]
		if !ok {
			continue
		}
		if p.Op() == enum.SkillProposalCreate {
			if st.rows[name] != nil {
				continue
			}
		} else if row := st.rows[name]; row == nil || row.Digest() != p.TargetDigest() || onDisk == p.TargetDigest() {
			continue
		}
		digest, err := writer.Digest(drepo.SkillDocument{Name: p.Name(), Description: p.Description(), Body: p.Body()})
		if err != nil {
			return st, err
		}
		st.own[p.ID()] = digest == onDisk
	}
	return st, nil
}

// supersedeCause names the state that makes a proposal open reports as not open,
// in the owner's terms: the root that holds the name, a target that no longer
// reads as a skill, a file that differs from the one the patch read, the owner's
// move of the target to Only you, or a row that no longer describes the file. A
// retired proposal is dismissed with this cause, so it states the fact, not the
// kind of change. movedSince reads the same states at the reread, in its own
// words, because the reread happens while the model answers.
func (st learnedState) supersedeCause(p *aggregate.SkillProposal) string {
	if p.Op() == enum.SkillProposalCreate {
		if root, held := st.elsewhere[p.Name()]; held {
			return "the name is live in the " + root + " root since it was proposed"
		}
		if _, onDisk := st.onDisk[p.Name()]; onDisk && !st.own[p.ID()] {
			return "the name is live in the " + string(enum.SkillRootNusashell) + " root since it was proposed"
		}
		return "the name is still owned by the loop, but its file no longer reads as a skill since it was proposed"
	}
	digest, live := st.onDisk[p.TargetName()]
	switch {
	case !live:
		return "the target no longer reads as a skill since it was proposed"
	case digest != p.TargetDigest():
		// The loop's own failed commit is named as the loop's write, so the owner
		// is not left to think they edited it. Any other change to the file gets
		// the neutral sentence.
		if st.own[p.ID()] {
			return "the loop wrote this skill's file, but the decision was not recorded"
		}
		return "the skill's file changed since this proposal read it"
	}
	// The file is still the one the patch was read against. A row that holds
	// that digest and sits at user level is the owner's own move to Only you:
	// the row describes the file correctly, only the level changed. A row whose
	// digest differs describes an older file, and that is the text below.
	if row := st.rows[p.TargetName()]; row != nil && row.Digest() == p.TargetDigest() && row.Level() != enum.SkillLevelAgent {
		return "the owner moved it to Only you since it was proposed"
	}
	return "the target's ownership row no longer describes it since it was proposed"
}

// openProposals counts the pending proposals the owner can still accept — the
// queue gate's count, read by the one rule the page labels by.
func openProposals(ctx context.Context, deps CandidateDeps, workspace string, pending []*aggregate.SkillProposal) (int, error) {
	st, err := learnedNow(ctx, deps.Catalog, deps.Folders, deps.Ownership, deps.Writer, workspace, pending)
	if err != nil {
		return 0, err
	}
	open := 0
	for _, p := range pending {
		if st.open(p) {
			open++
		}
	}
	return open, nil
}

// ownershipRow reads a learned skill's ownership row, nil when it has none.
func ownershipRow(ctx context.Context, deps CandidateDeps, name string) (*aggregate.SkillOwnership, error) {
	row, err := deps.Ownership.Get(ctx, enum.SkillRootNusashell, name)
	switch {
	case err == nil:
		return row, nil
	case apperror.As(err).StatusCode() == 404:
		return nil, nil
	default:
		return nil, err
	}
}
