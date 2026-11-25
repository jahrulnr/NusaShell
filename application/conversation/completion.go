package appconversation

import (
	"context"

	appjob "github.com/jahrulnr/nusashell/application/job"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// maxMachineTurns bounds consecutive machine-origin turns: reaching it stops the
// wake, never the record — the completion still lands on the next natural turn.
const maxMachineTurns = 3

// OnCompletion parks one asynchronous finish for a room and, when the room is idle
// and under the cap, spends a turn on it.
//
// The record is durable first: it goes to the room's sidecar before anything else
// happens, so neither a busy room, a spent cap, nor a restart between the exit and
// the turn that reads it can lose it. The wake is the bounded convenience; only the
// turn is ever refused.
func (app *ConversationApp) OnCompletion(convID string, c dservice.Completion) {
	if convID == "" || c.Source == "" || c.ID == "" {
		return
	}
	recorded, err := app.depsSnapshot().Convs.SaveCompletions(context.Background(), convID, []dservice.Completion{c})
	if err != nil || !recorded {
		// A deleted room has nowhere to park it, and a repeat is not news: neither
		// is a reason to spend a turn.
		return
	}
	app.wakeFor(convID)
}

// wakeFor queues one wake turn when the room is idle and under the cap. A busy or
// queued room keeps the record pending without a job of its own: the running turn
// renders it on its next hydration step.
//
// settled says the caller knows the room's turn just ended, so a worker that still
// reports it running (the frame between the job returning and the loop clearing it)
// is not a reason to refuse.
func (app *ConversationApp) wakeFor(convID string) { app.wake(convID, false) }

// wakeIfPending retries the wake at the moment a turn released the room: the record
// that arrived under that turn has had nothing to read it until now.
func (app *ConversationApp) wakeIfPending(convID string) { app.wake(convID, true) }

func (app *ConversationApp) wake(convID string, settled bool) {
	app.mu.Lock()
	machine := app.machineTurns[convID]
	queued := app.wakeQueued[convID]
	app.mu.Unlock()
	// The cap is read before the record: at the cap the wake stops, the record
	// stays where it is.
	if queued || machine >= maxMachineTurns {
		return
	}
	// The worker is the truth: a running job (a turn, a fold, a delete) or a queue
	// with something in it means the room works — the record waits inside that
	// work, and no turn is spent on it.
	app.mu.Lock()
	w := app.workers[convID]
	app.mu.Unlock()
	if w != nil && (w.Depth() > 0 || (!settled && w.Running() != nil)) {
		return
	}
	// A wake with nothing to read would spend one of the three turns the room has
	// on an empty message.
	if !app.hasPending(convID) {
		return
	}
	app.mu.Lock()
	app.machineTurns[convID]++
	app.wakeQueued[convID] = true
	app.mu.Unlock()
	if err := app.post(convID, &appjob.Wake{Ctx: context.Background(), Deps: app.depsSnapshot()}); err != nil {
		// Nothing was queued: the record waits for the next natural turn.
		app.mu.Lock()
		delete(app.wakeQueued, convID)
		app.mu.Unlock()
	}
}

// hasPending reports whether the room has a finish no turn has read yet.
func (app *ConversationApp) hasPending(convID string) bool {
	pending, err := app.depsSnapshot().Convs.PendingCompletions(context.Background(), convID)
	return err == nil && len(pending) > 0
}

// noteOwnerTurn resets the machine-turn streak: an owner, relay, channel, or
// automation turn intervenes, so the next completion may wake again.
func (app *ConversationApp) noteOwnerTurn(convID string) {
	app.mu.Lock()
	if app.machineTurns[convID] != 0 {
		delete(app.machineTurns, convID)
	}
	app.mu.Unlock()
}

// PendingCompletions answers the records a turn has not read yet.
func (app *ConversationApp) PendingCompletions(convID string) []dservice.Completion {
	pending, err := app.depsSnapshot().Convs.PendingCompletions(context.Background(), convID)
	if err != nil {
		return nil
	}
	return pending
}

// ClearCompletions drops the records a turn rendered, identified by their keys.
// The keys stay known in the sidecar: the same exit reported again is still the
// same record.
func (app *ConversationApp) ClearCompletions(convID string, keys []string) {
	if len(keys) == 0 {
		return
	}
	_ = app.depsSnapshot().Convs.ClearCompletions(context.Background(), convID, keys)
}

// WakeDone releases the in-flight mark once a wake turn ended: a failed wake keeps
// its records, and the next turn may retry them.
func (app *ConversationApp) WakeDone(convID string) {
	app.mu.Lock()
	delete(app.wakeQueued, convID)
	app.mu.Unlock()
}

// forgetCompletions drops a deleted room's records with it — the sidecar goes,
// and so do the in-memory marks that only that room used.
func (app *ConversationApp) forgetCompletions(convID string) {
	_ = app.depsSnapshot().Convs.DropCompletions(context.Background(), convID)
	app.mu.Lock()
	delete(app.machineTurns, convID)
	delete(app.wakeQueued, convID)
	app.mu.Unlock()
}
