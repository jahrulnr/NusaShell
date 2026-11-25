package repoconversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	repo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// pendingFile parks a room's runtime and session-option picks beside its record.
// A turn saves the whole conversation when it ends, so a pick kept inside the
// record would be overwritten by the turn that was already running when the
// owner made it — the file has no such owner and survives until a turn takes it.
const pendingFile = "pending-selection.json"

// SavePendingSelection writes the merged selection: what already waits plus what
// the caller just picked, last pick winning per field. The read and the write
// share the store lock, so two picks arriving together cannot drop one another.
func (r *ConversationRepository) SavePendingSelection(ctx context.Context, convID string, sel repo.PendingSelection) error {
	if err := validID(convID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return err
	}
	defer unlock()

	dir := r.convDir(convID)
	if _, err := os.Stat(filepath.Join(dir, metaFile)); errors.Is(err, os.ErrNotExist) {
		return apperror.NewNotFoundError("conversation "+convID, err)
	} else if err != nil {
		return err
	}
	stored, _, err := readPending(dir)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(mergeSelection(stored, sel))
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, pendingFile), raw, 0o600)
}

// PendingSelection reads the parked selection, false when the room waits for
// nothing.
func (r *ConversationRepository) PendingSelection(ctx context.Context, convID string) (repo.PendingSelection, bool, error) {
	if err := validID(convID); err != nil {
		return repo.PendingSelection{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return repo.PendingSelection{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sel, ok, err := readPending(r.convDir(convID))
	if err != nil {
		if apperror.As(err).StatusCode() == apperror.StatusNotFound {
			return repo.PendingSelection{}, false, nil
		}
		return repo.PendingSelection{}, false, err
	}
	return sel, ok, nil
}

// ClearPendingSelection drops the parked selection. A room with none is not an
// error: the caller reports that a turn took it.
func (r *ConversationRepository) ClearPendingSelection(ctx context.Context, convID string) error {
	if err := validID(convID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.Remove(filepath.Join(r.convDir(convID), pendingFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// readPending decodes one room directory's parked selection. A missing file is
// not an error; a corrupt one is — a pick nobody can read must be said out loud,
// never silently dropped.
func readPending(dir string) (repo.PendingSelection, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, pendingFile))
	if errors.Is(err, os.ErrNotExist) {
		return repo.PendingSelection{}, false, nil
	}
	if err != nil {
		return repo.PendingSelection{}, false, err
	}
	var sel repo.PendingSelection
	if err := json.Unmarshal(raw, &sel); err != nil {
		return repo.PendingSelection{}, false, fmt.Errorf("pending selection corrupt: %w", err)
	}
	return sel, true, nil
}

// mergeSelection folds a new pick into what already waits: one runtime wins
// whole (an agent clears the provider pair and the reverse — a room runs one or
// the other), effort and upstream replace their own value, and options merge by
// id so a later pick for the same option wins.
func mergeSelection(stored, next repo.PendingSelection) repo.PendingSelection {
	out := stored
	if !next.AcceptedAt.IsZero() {
		out.AcceptedAt = next.AcceptedAt
	}
	switch {
	case next.AgentID != "":
		out.AgentID, out.AgentModel = next.AgentID, next.AgentModel
		out.ProviderID, out.ModelID = "", ""
	case next.ProviderID != "" || next.ModelID != "":
		out.ProviderID, out.ModelID = next.ProviderID, next.ModelID
		out.AgentID, out.AgentModel = "", ""
	}
	if next.Effort != nil {
		out.Effort = next.Effort
	}
	if next.Upstream != nil {
		out.Upstream = next.Upstream
	}
	if len(next.Options) > 0 {
		merged := make(map[string]string, len(out.Options)+len(next.Options))
		for id, v := range out.Options {
			merged[id] = v
		}
		for id, v := range next.Options {
			merged[id] = v
		}
		out.Options = merged
	}
	return out
}
