package repoprovider

import (
	"context"
	"errors"
	"os"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Delete removes <id>.json, then clears its auto-stored secret. The
// record file is the commit point: it is removed first, so a failed
// removal leaves the record with its secret intact — fully usable and
// retryable. Once the file is gone the delete is committed; a vault
// outage after that point leaves an orphaned
// "secret:provider/<id>/api_key" entry, which is inert garbage rather
// than a broken record, and reporting failure for a deleted record
// would only invite a retry that can answer 404.
// Conversations referencing the provider keep a dangling id — resolution
// failure is the application's fallback.
func (r *ProviderRepository) Delete(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, err := os.Stat(r.file(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return apperror.NewNotFoundError("provider "+id, err)
		}
		return err
	}
	if err := os.Remove(r.file(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return apperror.NewNotFoundError("provider "+id, err)
		}
		return err
	}
	// Best-effort cleanup of the committed delete: the secret key is
	// garbage once nothing references it, and its removal failure is
	// not a delete failure.
	if r.secrets != nil {
		_ = r.secrets.Delete(ctx, drepo.ProviderSecretKey(id))
	}
	return nil
}
