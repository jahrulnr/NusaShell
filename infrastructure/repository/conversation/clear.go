package repoconversation

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
)

// Clear empties the room's stored dialogue: every sealed segment goes, the
// live tail is emptied, and the conversation-owned tool output with it. meta
// is written from conv — the caller's post-state — so identity, bindings, and
// accumulated usage survive while the counters start at zero and the agent
// session is dropped.
//
// The cleared room is built in the staging area and swapped in, rather than
// edited in place: a crash while the live directory is being emptied would
// leave counters promising history that is already gone, and the store refuses
// such an archive. With the swap, a failure before the publish leaves the live
// room untouched, and a crash between the two renames is repaired by
// recoverRoom on the next read — the room is never left missing or rejected.
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

	// Reading the record first both validates the archive it is about to
	// replace and finishes any earlier swap left behind.
	meta, err := r.readMeta(conv.ID())
	if err != nil {
		return err
	}

	// Build the empty room beside the store's rooms. Nothing about the live
	// directory changes until this one holds a complete record and an empty
	// tail; a stage that never publishes is debris recoverRoom drops.
	stage, err := r.stageRoom(conv.ID())
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	m := metaFor(conv)
	m.CreatedAt = meta.CreatedAt
	m.MessageCount, m.ActiveCount, m.SealedSegments = 0, 0, 0
	if err := writeMetaFile(stage, m); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(stage, activeFile), nil, 0o600); err != nil {
		return err
	}

	// Swap: park the live room, publish the cleared one, drop the parked copy.
	// The park is the point of no return — from here the name is free — so the
	// publish follows at once, and a publish that fails puts the room back
	// before the caller hears about it.
	dir := r.convDir(conv.ID())
	parked, err := r.parkedName(conv.ID())
	if err != nil {
		return err
	}
	if err := renamePath(dir, parked); err != nil {
		return err
	}
	if err := renamePath(stage, dir); err != nil {
		// The clear changed nothing, but only if the room really is back: a
		// rollback that fails must not be swallowed — the parked copy is then
		// the room's only history, and the caller has to hear both failures.
		if rerr := renamePath(parked, dir); rerr != nil {
			return fmt.Errorf("clear %s: publish the cleared room: %w; putting the room back failed too, its contents stay parked and are restored on the next read: %w", conv.ID(), err, rerr)
		}
		return err
	}
	// The cleared room owns the name now. The old contents are debris and go;
	// a failure to remove them costs space, never readability, but it is not
	// silent — recovery drops the leftover on the next read, and the log says
	// which copy stayed behind.
	if err := removeAllPath(parked); err != nil {
		slog.Warn("could not remove the cleared conversation's old copy",
			"conversation", conv.ID(), "err", err)
	}
	return nil
}
