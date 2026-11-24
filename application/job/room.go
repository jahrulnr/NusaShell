package job

import (
	"context"
	"fmt"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Clear empties a room's dialogue while keeping what the room *is*: its
// identity, bindings, workspace, project, label, routing, pin, memory
// setting, and accumulated usage survive; its messages, sealed segments, and
// tool output do not. It queues like a send — never preempts — so it lands
// after any turn already running, and it drops the agent's own session so the
// next turn starts a fresh one instead of remembering cleared turns.
type Clear struct {
	Base
	Deps Deps
	Ctx  context.Context
}

// Execute runs the clear on the worker's goroutine. The aggregate's
// ClearHistory is the definition of the post-state; the repository persists
// it together with the removed files, so a clear cannot leave the record
// promising a session its history no longer backs.
func (j *Clear) Execute(execCtx context.Context) (err error) {
	defer j.finish(&err)
	ctx := j.Cancellable(execCtx, j.Ctx)
	d := j.Deps
	convID := j.ConvID()
	err = func() error {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "clear canceled", err)
		}
		conv, err := d.Convs.Load(ctx, convID)
		if err != nil {
			return err // 404 — no room, nothing to clear
		}
		agentID, session := conv.AgentID(), conv.AgentSession()
		conv.ClearHistory()
		if err := d.Convs.Clear(ctx, conv); err != nil {
			return err
		}
		d.discardAgentSession(convID, agentID, session)
		return nil
	}()
	return err
}

// Fork copies a room's stored history into a new room. dst — the destination
// aggregate, with its fresh id, title, and bindings — comes from the domain's
// Fork rule, so the copy keeps what a copy should keep; the job exists to run
// the copy on the room's worker, where no turn can write while it reads.
type Fork struct {
	Base
	Deps Deps
	Ctx  context.Context
	Dst  *aggregate.Conversation
}

// Execute performs the copy on the worker's goroutine.
func (j *Fork) Execute(execCtx context.Context) (err error) {
	defer j.finish(&err)
	ctx := j.Cancellable(execCtx, j.Ctx)
	err = func() error {
		if err := ctx.Err(); err != nil {
			return apperror.New(408, "fork canceled", err)
		}
		if j.Dst == nil {
			return apperror.NewInternalError("fork has no destination", fmt.Errorf("job.Fork without Dst"))
		}
		return j.Deps.Convs.Fork(ctx, j.ConvID(), j.Dst)
	}()
	return err
}
