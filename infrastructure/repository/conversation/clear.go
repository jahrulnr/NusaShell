package repoconversation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
)

// Clear empties the room's stored dialogue: every sealed segment goes, the
// live tail is truncated, and the conversation-owned tool output with it.
// meta is rewritten from conv — the caller's post-state — so identity,
// bindings, and accumulated usage survive while the counters start at zero
// and the agent session is dropped.
//
// The files are removed before the record is rewritten. An interrupted clear
// therefore fails the store's archive check instead of serving a room whose
// metadata still promises history that is gone; the remedy is the one every
// interrupted surgery has: stop the app and restore from backup.
func (r *ConversationRepository) Clear(ctx context.Context, conv *aggregate.Conversation) error {
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

	meta, err := r.readMeta(conv.ID())
	if err != nil {
		return err
	}
	dir := r.convDir(conv.ID())
	segs, err := sealedSegments(dir)
	if err != nil {
		return err
	}
	for _, seg := range segs {
		if err := os.Remove(seg); err != nil {
			return fmt.Errorf("clear %s: %w", conv.ID(), err)
		}
	}
	if err := os.RemoveAll(filepath.Join(dir, toolOutputDir)); err != nil {
		return fmt.Errorf("clear %s: tool output: %w", conv.ID(), err)
	}
	if err := writeFileAtomic(filepath.Join(dir, activeFile), nil, 0o600); err != nil {
		return err
	}

	m := metaFor(conv)
	m.CreatedAt = meta.CreatedAt
	m.MessageCount, m.ActiveCount, m.SealedSegments = 0, 0, 0
	return r.writeMeta(conv.ID(), m)
}
