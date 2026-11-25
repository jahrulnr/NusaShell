package appconversation

import (
	"context"
	"fmt"
	"strings"

	appjob "github.com/jahrulnr/nusashell/application/job"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Delete removes the conversation and everything it stored — meta, the
// live tail, every sealed segment, and conversation-owned tool output.
// A running job is interrupted before
// the repository call, so an in-flight tool or provider request dies with
// the room; queued jobs get 410. Delete returns only once the retired
// worker's loop has exited and its map entry is gone — the id is free
// again, so a new Send with the same id mints a fresh room.
//
// The repository delete runs under a background context: a caller who
// abandons Delete mid-flight must not half-delete a room.
func (app *ConversationApp) Delete(ctx context.Context, convID string) error {
	res := make(chan error, 1)
	j := &appjob.Delete{}
	j.Result = res
	app.post(convID, j)
	select {
	case err := <-res:
		if err != nil {
			return err
		}
		// The job's reply outruns the retirement it triggers: finish
		// answers before the worker's loop exits, so a post landing in
		// that window could still Submit to the corpse and be drained
		// to a 410. Done closes only after OnTerminate dropped the map
		// entry — wait for it and the next post mints a fresh worker.
		if w := j.Worker(); w != nil {
			select {
			case <-w.Done():
			case <-ctx.Done():
				return apperror.New(408, "delete request canceled", ctx.Err())
			}
		}
		// The thread's view goes with it; a stale one only wastes a row.
		if views, err := app.threadViews(); err == nil {
			_ = views.Delete(context.WithoutCancel(ctx), convID)
		}
		// The descriptions the room's files produced are that room's text:
		// they leave with it rather than waiting for a delete-all.
		app.mu.Lock()
		descriptions := app.mediaDescriptions
		app.mu.Unlock()
		if descriptions != nil {
			_ = descriptions.Purge(convID)
		}
		return nil
	case <-ctx.Done():
		return apperror.New(408, "delete request canceled", ctx.Err())
	}
}

// DeleteAll removes every stored conversation, then the artifacts that
// only threads own — uploads, picks, recordings, and the saved views.
// Rooms delete one at a time: each Delete preempts a running turn, so a
// busy thread is cancelled, not skipped. A room already gone between
// List and its turn (404) still counts as deleted — the end state is the
// same.
//
// The global purge is all-or-nothing: when any room fails to delete, its
// uploads and views must survive, so DeleteAll reports the deleted count
// plus the ids that failed and leaves every artifact store untouched.
func (app *ConversationApp) DeleteAll(ctx context.Context) (int, error) {
	deps := app.depsSnapshot()
	infos, err := deps.Convs.List(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	var failed []string
	for _, info := range infos {
		err := app.Delete(ctx, info.ID)
		if err == nil || apperror.As(err).StatusCode() == 404 {
			deleted++
			continue
		}
		failed = append(failed, info.ID)
	}
	if len(failed) > 0 {
		return deleted, apperror.New(500,
			"failed to delete conversations: "+strings.Join(failed, ", "),
			fmt.Errorf("delete-all stopped with %d failures", len(failed)))
	}
	// Every thread is gone — now the shared stores empty too. A nil
	// store means the surface was never wired; skip it like the
	// per-room paths do.
	if app.uploads != nil {
		if err := app.uploads.Purge(ctx); err != nil {
			return deleted, err
		}
	}
	app.mu.Lock()
	descriptions := app.mediaDescriptions
	app.mu.Unlock()
	if descriptions != nil {
		if err := descriptions.PurgeAll(); err != nil {
			return deleted, err
		}
	}
	if app.picks != nil {
		if err := app.picks.Purge(ctx); err != nil {
			return deleted, err
		}
	}
	if app.recordings != nil {
		if err := app.recordings.Purge(ctx); err != nil {
			return deleted, err
		}
	}
	if views, err := app.threadViews(); err == nil {
		if err := views.Purge(ctx); err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
