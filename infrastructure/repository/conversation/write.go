package repoconversation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Save upserts meta.json — creates the conversation directory on first write
// and preserves record-level fields (counters, CreatedAt) on update.
//
// A room's handle is decided here, under the store lock that writes the
// record: a new room with no label is given the first animal no other room
// holds, and a label another room holds is refused (409). Allocating in the
// caller let two rooms created at the same moment read the same set of used
// labels before either had written its own and pick the same handle.
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

	// A room whose clear was interrupted is parked: put it back before the
	// directory is created, or an empty directory would take the name and the
	// old history would look like debris.
	if err := r.recoverRoom(conv.ID()); err != nil {
		return err
	}
	dir := r.convDir(conv.ID())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	m := metaFor(conv)
	// Preserve record-level fields on update.
	prev, prevErr := r.readMeta(conv.ID())
	fresh := prevErr != nil
	switch {
	case prevErr == nil:
		m.CreatedAt = prev.CreatedAt
		m.MessageCount = prev.MessageCount
		m.ActiveCount = prev.ActiveCount
		m.SealedSegments = prev.SealedSegments
	case apperror.As(prevErr).StatusCode() == apperror.StatusNotFound:
		m.CreatedAt = time.Now()
	default:
		return prevErr
	}
	if err := r.assignLabel(conv, &m, fresh, prev); err != nil {
		return err
	}
	return r.writeMeta(conv.ID(), m)
}

// assignLabel gives a fresh room its handle or refuses one another room owns.
// Callers hold r.mu and the store lock, so the set read here cannot change
// before the record that claims a label is written.
func (r *ConversationRepository) assignLabel(conv *aggregate.Conversation, m *metaRecord, fresh bool, prev metaRecord) error {
	if m.Label == "" {
		if !fresh {
			return nil // the owner cleared the handle; an update keeps it cleared
		}
		used, err := r.labelsInUseLocked("")
		if err != nil {
			return err
		}
		labels := make(map[string]bool, len(used))
		for label := range used {
			labels[label] = true
		}
		label := valueobject.PickLabel(labels)
		if err := conv.SetLabel(label); err != nil {
			return err
		}
		m.Label = label
		return nil
	}
	if !fresh && prev.Label == m.Label {
		return nil // every later save carries the room's own handle
	}
	used, err := r.labelsInUseLocked(conv.ID())
	if err != nil {
		return err
	}
	if owner, taken := used[m.Label]; taken {
		return apperror.New(409, "another thread already uses @"+m.Label,
			fmt.Errorf("label %q belongs to conversation %s", m.Label, owner))
	}
	return nil
}

// labelsInUseLocked maps every stored handle to the room holding it, skipping
// exceptID. A room whose record cannot be read holds no resolvable handle
// either — List refuses such a store — so it is left out.
func (r *ConversationRepository) labelsInUseLocked(exceptID string) (map[string]string, error) {
	entries, err := os.ReadDir(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	used := make(map[string]string, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == exceptID {
			continue
		}
		meta, err := r.readMetaUnvalidated(entry.Name())
		if err != nil || meta.Label == "" {
			continue
		}
		used[meta.Label] = meta.ID
	}
	return used, nil
}
