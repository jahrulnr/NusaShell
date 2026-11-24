package repoconversation

import (
	"context"
	"errors"
	"os"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Delete removes the whole conversation directory — meta, live tail,
// sealed segments, and conversation-owned tool output. A copy the store staged
// or parked for the room goes with it: a delete must not leave something a
// later recovery could bring back.
func (r *ConversationRepository) Delete(ctx context.Context, convID string) error {
	if err := validID(convID); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return err
	}
	defer unlock()

	if err := r.dropStaging(convID); err != nil {
		return err
	}
	if _, err := os.Stat(r.convDir(convID)); errors.Is(err, os.ErrNotExist) {
		return apperror.NewNotFoundError("conversation "+convID, err)
	}
	return os.RemoveAll(r.convDir(convID))
}
