package repoconversation

import (
	"context"
	"fmt"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// Load rebuilds meta + the active segment only — the live context the
// model sees. Sealed archives are reached via LoadAt.
func (r *ConversationRepository) Load(ctx context.Context, convID string) (*aggregate.Conversation, error) {
	if err := validID(convID); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := r.readMeta(convID)
	if err != nil {
		return nil, err
	}
	c, err := aggregate.NewConversation(meta.ID)
	if err != nil {
		return nil, err
	}
	c.SetTitle(meta.Title)
	c.RestoreAgentTitle(meta.AgentTitle)
	c.SetProviderID(meta.ProviderID)
	c.SetModelID(meta.ModelID)
	effort, ok := enum.ParseEffort(meta.Effort)
	if !ok && meta.Effort != "" {
		return nil, fmt.Errorf("conversation %s: unknown effort %q", meta.ID, meta.Effort)
	}
	c.SetEffort(effort)
	c.SetUpstream(meta.Upstream)
	c.SetWorkspace(meta.Workspace)
	if err := c.SetEnvironment(enum.Environment(meta.Environment)); err != nil {
		return nil, err
	}
	if err := c.SetProject(meta.ProjectID); err != nil {
		return nil, err
	}
	c.SetPinned(meta.Pinned)
	c.SetMemoryOff(meta.MemoryOff)
	if err := c.SetLabel(meta.Label); err != nil {
		return nil, err
	}
	if r := meta.Routing; r != nil {
		c.SetRouting(&valueobject.Routing{ReplyToOrigin: r.ReplyToOrigin, MirrorFinal: r.MirrorFinal, NotifyApprovals: r.NotifyApprovals})
	}
	if meta.AgentID != "" {
		if err := c.BindAgent(meta.AgentID); err != nil {
			return nil, err
		}
		c.SetAgentSession(meta.AgentSession)
		for id, v := range meta.AgentOptions {
			_ = c.SetAgentOption(id, v)
		}
		if r := meta.AgentContext; r != nil {
			a, err := valueobject.NewAgentContext(r.Used, r.Size, r.CostAmount, r.CostCurrency)
			if err != nil {
				return nil, fmt.Errorf("conversation %s: agent context: %w", meta.ID, err)
			}
			c.ReportAgentContext(a)
		}
	}
	// systemPrompt is not restored: it is code (embedded templates), and
	// older metas carrying a stamped copy are ignored so every room gets
	// the current template.
	u, err := decodeUsage(meta.Usage)
	if err != nil {
		return nil, err
	}
	c.AddUsage(u)
	var schemas []valueobject.ToolSchema
	for _, t := range meta.Tools {
		s, err := decodeToolSchema(t)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, s)
	}
	c.SetTools(schemas)

	msgs, err := r.loadMessages(convID, 0)
	if err != nil {
		return nil, err
	}
	// A tail file that still exists but holds no messages — truncated to
	// whitespace, or only orphan patches — is the same loss as a missing
	// file: metadata claims a live history the stream no longer has.
	if meta.ActiveCount > 0 && len(msgs) == 0 {
		return nil, fmt.Errorf("conversation %s: active.jsonl empty while metadata records %d tail messages; stop the app and restore from backup", convID, meta.ActiveCount)
	}
	for _, m := range msgs {
		c.Append(m)
	}
	return c, nil
}

// LoadAt slices the global logical history — sealed segments first, then
// active — with clamped indexes and negative-from-end semantics.
func (r *ConversationRepository) LoadAt(ctx context.Context, convID string, start, end int) ([]entity.Message, error) {
	if err := validID(convID); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := r.readMeta(convID)
	if err != nil {
		return nil, err
	}
	msgs, err := r.loadMessages(convID, meta.SealedSegments)
	if err != nil {
		return nil, err
	}
	n := len(msgs)
	if start < 0 {
		start += n
	}
	if end <= 0 {
		end = n
	}
	if start < 0 {
		start = 0
	}
	if end > n {
		end = n
	}
	if start >= end {
		return nil, nil
	}
	return msgs[start:end], nil
}
