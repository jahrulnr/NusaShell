package appmemory

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/prompt"
)

// Generator asks the utility model for one plain-text answer.
type Generator func(ctx context.Context, convID, system, user string) (string, error)

// UseGenerator sets the model consolidation asks. The conversation use case
// that owns the utility model is built after memory, so it arrives late;
// until then consolidation does nothing.
func (a *App) UseGenerator(g Generator) {
	a.genMu.Lock()
	a.generate = g
	a.genMu.Unlock()
}

// What consolidation sends and takes: the sealed history it reads is
// bounded, context lines more tightly than the owner's own words, and a
// reply's suggestions past the limit are dropped.
const (
	consolidateInputMax   = 40_000 // runes of conversation
	consolidateContextMax = 600    // runes of one assistant or tool line
	consolidateOwnerMax   = 4_000  // runes of one owner line
	consolidateOpsMax     = 8
)

// SuggestionCooldown is how long a thread is not asked again for a
// suggestion the owner already decided.
const SuggestionCooldown = aggregate.MemoryArchiveRetention

// Consolidate reads what a compaction sealed and stores suggestions for the
// owner: memories to add, rewrite, or mark stale, each quoting the owner's
// own words. It answers how many it stored. It does nothing when memory or
// consolidation is off, the thread keeps memory out, no utility model is
// set, or the owner said nothing in the sealed part.
func (a *App) Consolidate(ctx context.Context, convID string, sealed []entity.Message) (int, error) {
	a.genMu.Lock()
	generate := a.generate
	a.genMu.Unlock()
	if !a.enabled() || a.consolidation() != "suggest" || generate == nil || a.proposals == nil {
		return 0, nil
	}
	p, err := a.place(ctx, convID)
	if err == errThreadOff {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	// The conversation as the model reads it. Only the owner's own
	// messages are evidence; harness notes (checkpoints, injected
	// records) are left out, and assistant text and tool output ride as
	// clipped context. Everything is redacted before it leaves.
	type line struct {
		text  string
		owner bool
	}
	var lines []line
	owners := map[string]string{} // message id → its text as the model saw it
	for _, m := range sealed {
		switch {
		case m.Role() == enum.RoleUser && m.UserType() != enum.LLMUserTypeSystem:
			text := a.scrub(clipRunes(m.Content(), consolidateOwnerMax))
			if strings.TrimSpace(text) == "" {
				continue
			}
			owners[m.ID()] = text
			lines = append(lines, line{text: "[owner #" + m.ID() + "] " + text, owner: true})
		case m.Role() == enum.RoleAssistant:
			if text := strings.TrimSpace(m.Content()); text != "" {
				lines = append(lines, line{text: "[assistant] " + a.scrub(clipRunes(text, consolidateContextMax))})
			}
			for _, call := range m.ToolCalls() {
				if call.Type() == enum.TooltypeInjection || !call.HasOutput() {
					continue
				}
				lines = append(lines, line{text: "[tool " + call.Name() + "] " + a.scrub(clipRunes(call.Output(), consolidateContextMax))})
			}
		}
	}
	if len(owners) == 0 {
		return 0, nil
	}
	// Over the budget, the newest lines stay: the owner's first, then
	// context in what room is left.
	keep := make([]bool, len(lines))
	budget := consolidateInputMax
	for _, owner := range []bool{true, false} {
		for i := len(lines) - 1; i >= 0; i-- {
			if lines[i].owner != owner {
				continue
			}
			if n := len([]rune(lines[i].text)); n <= budget {
				keep[i], budget = true, budget-n
			}
		}
	}
	index, err := a.render(ctx, p)
	if err != nil {
		return 0, err
	}
	var in strings.Builder
	in.WriteString("<memory_index>\n" + index + "\n</memory_index>\n\n<sealed_conversation>\n")
	for i, l := range lines {
		if keep[i] {
			in.WriteString(l.text + "\n")
		}
	}
	in.WriteString("</sealed_conversation>\n")

	raw, err := generate(ctx, convID, prompt.SystemPrompt("memory-consolidate"), in.String())
	if err != nil {
		return 0, err
	}
	var reply struct {
		Ops []suggestion `json:"ops"`
	}
	text := strings.TrimSpace(raw)
	if i, j := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}'); i >= 0 && j > i {
		text = text[i : j+1] // past a code fence or a sentence the model added
	}
	if err := json.Unmarshal([]byte(text), &reply); err != nil {
		return 0, apperror.New(502, "the utility model answered no suggestions document", err)
	}
	if len(reply.Ops) > consolidateOpsMax {
		a.log.Info("memory: dropped suggestions past the limit", "conversation", convID, "dropped", len(reply.Ops)-consolidateOpsMax)
		reply.Ops = reply.Ops[:consolidateOpsMax]
	}
	stored := 0
	for _, s := range reply.Ops {
		prop, why := a.suggest(ctx, p, owners, s)
		if prop == nil {
			a.log.Debug("memory: suggestion skipped", "conversation", convID, "op", s.Op, "name", s.Name, "why", why)
			continue
		}
		if err := a.proposals.Add(ctx, prop); err != nil {
			a.log.Warn("memory: could not store a suggestion", "conversation", convID, "err", err)
			continue
		}
		stored++
	}
	return stored, nil
}

// suggestion is one op as the model wrote it.
type suggestion struct {
	Op          string `json:"op"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Reason      string `json:"reason"`
	Evidence    string `json:"evidence"`
}

// suggest turns one op into a suggestion, or says why not. The model's word
// is checked, never taken: the evidence must be the owner's, the target
// must be one this thread sees, and nothing may hold a credential.
func (a *App) suggest(ctx context.Context, p place, owners map[string]string, s suggestion) (*aggregate.MemoryProposal, string) {
	op, ok := enum.ParseMemoryProposalOp(s.Op)
	if !ok {
		return nil, "unknown op"
	}
	for _, text := range []string{s.Name, s.Description, s.Body, s.Reason, s.Evidence} {
		if a.scrub(text) != text {
			return nil, "holds a credential"
		}
	}
	// The evidence is the owner's words: found, whitespace aside, in one of
	// their messages.
	quote := strings.Join(strings.Fields(s.Evidence), " ")
	if quote == "" {
		return nil, "no evidence"
	}
	messageID := ""
	ids := make([]string, 0, len(owners))
	for id := range owners {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if strings.Contains(strings.Join(strings.Fields(owners[id]), " "), quote) {
			messageID = id
			break
		}
	}
	if messageID == "" {
		return nil, "the evidence is not the owner's words"
	}
	in := aggregate.MemoryProposalInput{
		ConversationID: p.convID, MessageID: messageID, Op: op, Name: strings.TrimSpace(s.Name),
		Description: s.Description, Body: s.Body, Reason: s.Reason, Evidence: quote,
	}
	var target *aggregate.Memory
	switch op {
	case enum.MemoryProposalAdd:
		t, ok := enum.ParseMemoryType(s.Type)
		if !ok {
			return nil, "unknown type"
		}
		scope, err := p.scope(s.Scope, t)
		if err != nil {
			return nil, "unknown scope"
		}
		if _, err := a.repo.FindByName(ctx, scope, in.Name); err == nil {
			return nil, "a memory of that name exists; an update was due"
		}
		in.Type, in.Scope = t, scope
	case enum.MemoryProposalUpdate, enum.MemoryProposalStale:
		// The target is named in one scope: two memories of one name in
		// different scopes must not be confused.
		if s.Scope == "" {
			return nil, "no scope for the target"
		}
		scope, err := p.scope(s.Scope, "")
		if err != nil {
			return nil, "unknown scope"
		}
		m, err := a.repo.FindByName(ctx, scope, in.Name)
		if err != nil {
			return nil, "no such memory in that scope"
		}
		if op == enum.MemoryProposalStale && m.Status() == enum.MemoryStatusStale {
			return nil, "already stale"
		}
		in.Scope, in.Type, in.TargetID, in.TargetUpdatedAt = scope, m.Type(), m.ID(), m.UpdatedAt()
		if t, ok := enum.ParseMemoryType(s.Type); ok && op == enum.MemoryProposalUpdate {
			in.Type = t
		}
		target = m
	}
	seen, err := a.proposals.Seen(ctx, drepo.MemoryProposalKey{Op: op, Scope: in.Scope, Name: in.Name}, p.convID, a.now().Add(-SuggestionCooldown))
	if err != nil || seen {
		return nil, "suggested already"
	}
	prop, err := aggregate.NewMemoryProposal("", in, a.now())
	if err != nil {
		return nil, err.Error()
	}
	if target == nil {
		hits, err := a.search(ctx, p.scopes, prop.Description()+"\n"+prop.Body(), similarLimit)
		if err == nil {
			var similar []string
			for _, h := range hits {
				if h.score >= similarFloor {
					similar = append(similar, h.memory.ID())
				}
			}
			prop.SetSimilar(similar)
		}
	}
	return prop, ""
}

// scrub is the text with credentials masked; text it changes held one.
func (a *App) scrub(s string) string {
	if a.redact == nil {
		return s
	}
	return a.redact(s)
}

func clipRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

// ---- Owner-facing ----

// ProposalView is a suggestion as the owner reviews it: what it would do,
// the memory it would change as that memory stands now, and the memories
// an add overlaps.
type ProposalView struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	MessageID      string    `json:"messageId,omitempty"`
	Op             string    `json:"op"`
	Scope          string    `json:"scope"`
	Type           string    `json:"type,omitempty"`
	Name           string    `json:"name"`
	Description    string    `json:"description,omitempty"`
	Body           string    `json:"body,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Evidence       string    `json:"evidence"`
	Status         string    `json:"status"`
	AcceptedID     string    `json:"acceptedId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	DecidedAt      time.Time `json:"decidedAt,omitzero"`
	Target         *View     `json:"target,omitempty"`
	// Changed says the target was forgotten or changed since the
	// suggestion was read: accepting it is refused.
	Changed bool   `json:"changed,omitempty"`
	Similar []View `json:"similar,omitempty"`
}

func (a *App) proposalView(ctx context.Context, p *aggregate.MemoryProposal) ProposalView {
	v := ProposalView{
		ID: p.ID(), ConversationID: p.ConversationID(), MessageID: p.MessageID(), Op: string(p.Op()), Scope: p.Scope().String(),
		Type: string(p.Type()), Name: p.Name(), Description: p.Description(), Body: p.Body(), Reason: p.Reason(),
		Evidence: p.Evidence(), Status: string(p.Status()), AcceptedID: p.AcceptedID(), CreatedAt: p.CreatedAt(), DecidedAt: p.DecidedAt(),
	}
	if p.TargetID() != "" {
		m, err := a.repo.Get(ctx, p.TargetID())
		if err == nil {
			t := a.view(m)
			v.Target = &t
		}
		v.Changed = p.Status() == enum.MemoryProposalPending && p.Changes(m)
	}
	for _, id := range p.Similar() {
		if m, err := a.repo.Get(ctx, id); err == nil && m.Live() {
			v.Similar = append(v.Similar, a.view(m))
		}
	}
	return v
}

// Proposals answers the suggestions waiting for the owner ("pending") or
// those decided ("decided"), newest first, and how many wait.
func (a *App) Proposals(ctx context.Context, status string, limit int) ([]ProposalView, int, error) {
	if a.proposals == nil {
		return nil, 0, nil
	}
	statuses := []enum.MemoryProposalStatus{enum.MemoryProposalPending}
	switch status {
	case "", "pending":
	case "decided":
		statuses = []enum.MemoryProposalStatus{enum.MemoryProposalAccepted, enum.MemoryProposalDismissed}
	default:
		return nil, 0, apperror.New(400, `status must be "pending" or "decided"`, nil)
	}
	list, err := a.proposals.List(ctx, statuses, limit)
	if err != nil {
		return nil, 0, err
	}
	pending, err := a.proposals.CountPending(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]ProposalView, len(list))
	for i, p := range list {
		out[i] = a.proposalView(ctx, p)
	}
	return out, pending, nil
}

// ProposalEdit is the owner's change to a suggestion as they accept it.
type ProposalEdit struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Reason      string `json:"reason"`
}

// DecideProposal accepts or dismisses a suggestion, once. Accepting writes
// what it says — edited by the owner if they did — through the one write
// path, as a suggestion from its thread. A suggestion about a memory that
// was forgotten or changed since, or an add whose name was taken since,
// is refused: the owner decides on what is there now.
func (a *App) DecideProposal(ctx context.Context, id, action string, edit *ProposalEdit) (ProposalView, error) {
	if a.proposals == nil {
		return ProposalView{}, apperror.New(404, "no such suggestion", nil)
	}
	p, err := a.proposals.Get(ctx, id)
	if err != nil {
		return ProposalView{}, err
	}
	if p.Status() != enum.MemoryProposalPending {
		return ProposalView{}, apperror.New(409, "this suggestion was already decided", nil)
	}
	now := a.now()
	switch action {
	case "dismiss":
		if err := p.Dismiss(now); err != nil {
			return ProposalView{}, apperror.New(409, err.Error(), nil)
		}
	case "accept":
		t, description, body, reason := p.Type(), p.Description(), p.Body(), p.Reason()
		if edit != nil {
			if edit.Type != "" {
				parsed, ok := enum.ParseMemoryType(edit.Type)
				if !ok {
					return ProposalView{}, apperror.New(400, "type must be user, feedback, project, or reference", nil)
				}
				t = parsed
			}
			description = cmp.Or(strings.TrimSpace(edit.Description), description)
			body = cmp.Or(strings.TrimSpace(edit.Body), body)
			reason = cmp.Or(strings.TrimSpace(edit.Reason), reason)
		}
		var target *aggregate.Memory
		if p.TargetID() != "" {
			target, err = a.repo.Get(ctx, p.TargetID())
			if err != nil && apperror.As(err).StatusCode() != 404 {
				return ProposalView{}, err
			}
			if p.Changes(target) {
				return ProposalView{}, apperror.New(409, "the memory was forgotten or changed since this was suggested — dismiss it, or edit the memory itself", nil)
			}
		}
		var memoryID string
		switch p.Op() {
		case enum.MemoryProposalAdd:
			if _, err := a.repo.FindByName(ctx, p.Scope(), p.Name()); err == nil {
				return ProposalView{}, apperror.New(409, "a memory named "+p.Name()+" exists there now — dismiss this, or edit that memory", nil)
			}
			m, _, err := a.write(ctx, p.Scope(), t, p.Name(), description, body, enum.MemorySourceConsolidation, p.ConversationID(), "")
			if err != nil {
				return ProposalView{}, err
			}
			memoryID = m.ID()
		case enum.MemoryProposalUpdate:
			m, _, err := a.write(ctx, target.Scope(), t, target.Name(), description, body, enum.MemorySourceConsolidation, p.ConversationID(), target.ID())
			if err != nil {
				return ProposalView{}, err
			}
			memoryID = m.ID()
		case enum.MemoryProposalStale:
			if a.scrub(reason) != reason {
				return ProposalView{}, apperror.New(400, "the reason looks like it holds a credential", nil)
			}
			if err := target.MarkStale(reason, now); err != nil {
				return ProposalView{}, apperror.New(400, err.Error(), nil)
			}
			if err := a.repo.Save(ctx, target); err != nil {
				return ProposalView{}, err
			}
			memoryID = target.ID()
		}
		if err := p.Accept(memoryID, now); err != nil {
			return ProposalView{}, apperror.New(409, err.Error(), nil)
		}
	default:
		return ProposalView{}, apperror.New(400, `action must be "accept" or "dismiss"`, nil)
	}
	if err := a.proposals.Decide(ctx, p); err != nil {
		return ProposalView{}, err
	}
	return a.proposalView(ctx, p), nil
}
