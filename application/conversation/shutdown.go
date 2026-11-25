package appconversation

import (
	"context"
	"log/slog"
	"time"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/worker"
)

// Shutdown stops the process's work in the barrier's turn phase: admission
// closes, every room worker terminates with the restart cause, queued jobs
// answer 503, and each worker's stop is waited for — bounded, because a
// wedged job must not spend the stop window the history phases need. A worker
// that will not stop is abandoned and named; its room keeps the last
// persisted step.
//
// A canceled ctx stops only this caller's wait; the shared shutdown continues
// for concurrent and later callers.
func (app *ConversationApp) Shutdown(ctx context.Context) error {
	app.mu.Lock()
	if app.shutdownDone == nil {
		app.shuttingDown = true
		done := make(chan struct{})
		app.shutdownDone = done
		type room struct {
			id string
			w  *worker.Worker
		}
		rooms := make([]room, 0, len(app.workers))
		for id, w := range app.workers {
			rooms = append(rooms, room{id: id, w: w})
		}
		app.workers = make(map[string]*worker.Worker)
		grace := app.stopGrace
		go func() {
			defer close(done)
			stopped := apperror.New(503, "conversation application is shutting down", nil)
			for _, r := range rooms {
				for _, j := range r.w.Terminate() {
					if aj, ok := j.(queuedJob); ok {
						aj.Reply(stopped)
					}
				}
			}
			// One deadline for the whole set: a healthy worker unwinds in
			// milliseconds, and the wedged one leaves with the process
			// instead of holding it.
			deadline := time.Now().Add(grace)
			for _, r := range rooms {
				select {
				case <-r.w.Done():
				case <-time.After(time.Until(deadline)):
					slog.Warn("shutdown abandoned a room worker",
						"conversation", r.id, "waited", grace)
				}
			}
			waitDone := make(chan struct{})
			go func() {
				app.workerWG.Wait()
				close(waitDone)
			}()
			select {
			case <-waitDone:
			case <-time.After(time.Until(deadline)):
				slog.Warn("shutdown abandoned workers still unwinding", "waited", grace)
			}
			// Best effort, and idempotent with the barrier's writers phase:
			// an early exit that never reaches the barrier (a listener
			// failure, a signal before composition finished) must still not
			// leave shells and agents behind.
			if err := app.TerminateChildren(context.Background()); err != nil {
				slog.Warn("shutdown could not stop every child process", "err", err)
			}
		}()
	}
	done := app.shutdownDone
	app.mu.Unlock()

	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return apperror.New(408, "conversation shutdown canceled", ctx.Err())
	}
}

// CloseAdmission stops taking work without stopping what runs: new posts
// answer 503 and no worker is started. It is the barrier's first phase —
// nothing may begin once shutdown does.
func (app *ConversationApp) CloseAdmission() {
	app.mu.Lock()
	app.shuttingDown = true
	app.mu.Unlock()
}

// TerminateChildren kills every child process the conversations own — shell
// sessions and agent runtimes — in the barrier's writers phase. They are not
// a turn's work: they hold pty fds, ports and agent sessions, and neither
// syscall.Exec nor a Windows exit takes them down.
func (app *ConversationApp) TerminateChildren(ctx context.Context) error {
	deps := app.depsSnapshot()
	if deps.Terminals != nil {
		deps.Terminals.TerminateAll()
	}
	if deps.AgentRuntime != nil {
		if err := deps.AgentRuntime.Shutdown(ctx); err != nil {
			return err
		}
	}
	return nil
}
