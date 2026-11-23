package repoconversation

import (
	"context"
	"os"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Save upserts meta.json — creates the conversation directory on first
// write and preserves record-level fields (counters, CreatedAt) on update.
func (r *ConversationRepository) Save(ctx context.Context, conv *aggregate.Conversation) error {
	if err := validID(conv.ID()); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return err
	}
	defer unlock()

	dir := r.convDir(conv.ID())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	m := metaRecord{
		ID:           conv.ID(),
		Title:        conv.Title(),
		ProviderID:   conv.ProviderID(),
		ModelID:      conv.ModelID(),
		Effort:       conv.Effort().String(),
		Upstream:     conv.Upstream(),
		Workspace:    conv.Workspace(),
		Environment:  conv.Environment().String(),
		AgentID:      conv.AgentID(),
		ProjectID:    conv.ProjectID(),
		Pinned:       conv.Pinned(),
		MemoryOff:    conv.MemoryOff(),
		Label:        conv.Label(),
		AgentSession: conv.AgentSession(),
		AgentOptions: conv.AgentOptions(),
		AgentTitle:   conv.AgentTitle(),
		Usage:        encodeUsage(conv.Usage()),
		// SystemPrompt is deliberately absent: the prompt ships embedded
		// in the binary, so it is applied in memory, never persisted.
	}
	if a := conv.AgentContext(); !a.IsZero() {
		amount, currency, _ := a.Cost()
		m.AgentContext = &agentContextRecord{Used: a.Used(), Size: a.Size(), CostAmount: amount, CostCurrency: currency}
	}
	if r := conv.Routing(); r != nil {
		m.Routing = &routingRecord{ReplyToOrigin: r.ReplyToOrigin, MirrorFinal: r.MirrorFinal, NotifyApprovals: r.NotifyApprovals}
	}
	for _, t := range conv.Tools() {
		m.Tools = append(m.Tools, encodeToolSchema(t))
	}
	// Preserve record-level fields on update.
	if prev, err := r.readMeta(conv.ID()); err == nil {
		m.CreatedAt = prev.CreatedAt
		m.MessageCount = prev.MessageCount
		m.ActiveCount = prev.ActiveCount
		m.SealedSegments = prev.SealedSegments
	} else if apperror.As(err).StatusCode() == apperror.StatusNotFound {
		m.CreatedAt = time.Now()
	} else {
		return err
	}
	return r.writeMeta(conv.ID(), m)
}
