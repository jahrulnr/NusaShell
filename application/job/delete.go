package job

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/worker"
)

// Delete removes the room — meta, live tail, sealed segments, tool output — and
// retires the worker: returning worker.ErrTerminate after a successful
// delete makes the worker drain its queue (410 for everything queued)
// and exit. It preempts: a running job is interrupted first.
type Delete struct {
	Base
	Deps Deps
}

// IsPreempt marks delete as a control job — it cuts the queue and
// interrupts whatever is running.
func (j *Delete) IsPreempt() bool { return true }

// Execute deletes on a background ctx — an abandoned caller must not
// half-delete a room. finish translates ErrTerminate into a nil reply:
// the caller gets the op result, not the sentinel. The reply precedes
// the worker's exit, so a caller needing the retirement barrier waits
// on the bound worker's Done.
func (j *Delete) Execute(context.Context) (err error) {
	defer j.finish(&err)
	// What the agent holds for the room is read before the room is gone.
	var agentID, agentSession string
	if info, ierr := j.Deps.Convs.Info(context.Background(), j.ConvID()); ierr == nil {
		agentID, agentSession = info.AgentID, info.AgentSession
	}
	if err = j.Deps.Convs.Delete(context.Background(), j.ConvID()); err != nil {
		return err
	}
	// The room is really gone now, so reap its terminal sessions — they
	// outlive jobs, so only this lifecycle hook knows the room is gone
	// for good. A failed delete above leaves the room live on disk; its
	// sessions must survive a retry.
	if j.Deps.Terminals != nil {
		j.Deps.Terminals.TerminateConversation(j.ConvID())
	}
	// The agent-side session belongs to the room too. Removing it can mean
	// starting the agent, so it does not hold up the reply.
	j.Deps.discardAgentSession(j.ConvID(), agentID, agentSession)
	if j.Deps.OnDeleted != nil {
		j.Deps.OnDeleted(j.ConvID())
	}
	return worker.ErrTerminate
}

// discardAgentSession removes the session an agent kept for a room that is
// being cleared or deleted. Removing it can mean starting the agent, so it
// runs detached: neither a clear nor a delete waits for that. An agent that
// was removed meanwhile has nothing to discard, so its live session, if any,
// is only released.
func (d Deps) discardAgentSession(convID, agentID, sessionID string) {
	if d.AgentRuntime == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		discarded := false
		if agentID != "" && d.Agents != nil {
			if agent, err := d.Agents.Load(ctx, agentID); err == nil {
				d.AgentRuntime.Discard(ctx, agent, convID, sessionID)
				discarded = true
			}
		}
		if !discarded {
			d.AgentRuntime.Release(ctx, convID)
		}
		d.AgentRuntime.DeleteCommands(ctx, convID)
	}()
}
