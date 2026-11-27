// Package job holds the conversation use-cases as executable commands —
// each job is self-contained: Execute carries the whole flow (load state,
// orchestrate domain ports, persist, emit events) and replies on its own
// result channel. The application layer submits them to a worker.Worker;
// the worker.Job contract (Execute, Interrupt, ErrTerminate) is the only
// mailbox knowledge — queue topology stays invisible.
package job

import (
	"context"
	"errors"
	"fmt"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/worker"
)

// Deps is everything a job may reach — the domain ports plus the
// conversation config, injected at construction. Jobs never see the app
// or the worker.
// TurnSurface opens the live view of a turn that reached execution without a
// client. A job without a socket still has an audience — its room — so a turn
// shaped job (send, edit, resume) asks for a handler when it has none.
//
// It fills a surface that does not exist yet: it never replaces or duplicates
// one. A turn a socket admitted already has its journal, and its handler rides
// the job; opening another here would give one turn two journals. A refusal
// answers a nil handler, and the turn runs unjournaled as before — the journal is
// observability, never a gate on work.
type TurnSurface interface {
	Open(convID, kind string, origin valueobject.TurnOrigin) (event.TurnEventHandler, func(persisted bool, err error))
}

// TurnGate hears a room's turn begin and end.
type TurnGate interface {
	BeginTurn(convID string)
	EndTurn(convID string)
	// ConsumePending records the parked pick this turn applied, so a projection
	// stops offering it while the turn runs. EndTurn releases the mark: a clear
	// that failed leaves the file, and the released mark makes it visible again.
	ConsumePending(convID string, acceptedAt time.Time)
}

// EditEvidence hears one owner rewrite: the message it replaced, the text that
// message held before, and the messages the rewrite drops. The tail is a copy
// the receiver may keep.
type EditEvidence interface {
	RecordEdit(convID, msgID, previous string, tail []entity.Message)
	DiscardEdit(convID, msgID string)
}

type Deps struct {
	// TurnSurface journals a turn no socket admitted. Nil runs it blind.
	TurnSurface TurnSurface
	// Completions is the pending-completion store a wake reads and clears. Nil
	// disables wakes; the records still land on a natural turn.
	Completions Completions
	// FollowUp hands a line the room already wrote to a following turn: the
	// agent's session never reads our history, so a relayed prompt an agent
	// turn could not take must be delivered again — using the line it already
	// has, never a second one. Nil (the CLI) drops the follow-up.
	FollowUp func(convID string, rec entity.Message) error
	// NextTurn hands text a turn accepted but never reached to the room's
	// queue: an input still waiting when the final answer lands is the next
	// turn, not a draft the caller has to decide about. origin is the turn's
	// own, so the next turn reads in the room the same way. Nil (the CLI) has
	// no queue behind it and drops it.
	NextTurn func(convID string, in valueobject.UserInput, origin valueobject.TurnOrigin) error
	// Turns hears a room's turn begin and end. It is what answers "is a turn
	// running here" — the worker's own "running" is a moment too long (it
	// clears after the job returns, while the caller already has the result).
	Turns        TurnGate
	Convs        drepo.Conversation
	Providers    drepo.Provider
	Environments map[enum.Environment]EnvironmentDriver
	Config       config.ConversationConfig
	// ThreadsEffort is Settings → Models → "New threads" reasoning effort:
	// what a room's first provider turn starts on when the caller names none
	// and the room stores none — a cross-thread spawn, an automation run, a
	// CLI send. None of those has a picker, so this is their only source for
	// the value; WebUI and channel turns send their own. Unset keeps the
	// model's configured effort.
	ThreadsEffort enum.Effort
	// Terminals owns conversation-keyed terminal sessions — reaped here
	// because sessions outlive the jobs that opened them.
	Terminals dservice.TerminalSessions
	// SummaryTool is the compaction checkpoint contract — the scratch
	// dialogue offers only this schema and the job reads the checkpoint
	// back out of the model's call. Injected by composition; it is never
	// part of a room's agent-facing catalog.
	SummaryTool dservice.Tool
	// Agents and AgentRuntime serve rooms bound to an external agent;
	// Approvals holds the permission requests those agents raise. All
	// three may be nil when the composition registers no agent runtime.
	Agents       drepo.Agent
	AgentRuntime dservice.AgentRuntime
	// AgentCatalog is the persisted per-agent offered surface — what a pick
	// can be validated against when no live session answers.
	AgentCatalog drepo.AgentCatalogStore
	Approvals    *Approvals
	// Projects resolves the folders of rooms that belong to a project;
	// nil means projects are not wired and project bindings are ignored.
	Projects drepo.Project
	// OnDeleted runs after a room is deleted, for state other use cases
	// keep about it (chat links). Nil means nothing else to clean.
	OnDeleted func(convID string)
	// Log reports a job's own trouble — a rescue that could not hand a line
	// over, say. Nil stays quiet; nothing here logs message text.
	Log *slog.Logger
	// OnCompacted runs after a compaction is saved, with the messages it
	// sealed. It must not block: the turn is waiting.
	OnCompacted func(convID string, sealed []entity.Message)
	// EditEvidence hears each owner rewrite. Nil — skills.improvement off —
	// means Edit captures nothing; the mode is read at boot.
	EditEvidence EditEvidence
	// MCPServers are the tool servers an agent gets in a room; nil means
	// none.
	MCPServers func(convID string) []dservice.MCPServer
	// ApprovalRules remember rooms' "always allow" answers; nil keeps
	// none.
	ApprovalRules drepo.ApprovalRule
	// Elicitations holds agents' requests for input; nil declines them.
	Elicitations *Elicitations
}

// Base is the shared machinery for room jobs: the bound conversation id,
// the caller's result channel, and interruption as ctx cancellation.
// Embedding it satisfies the worker.Job Interrupt contract plus the
// Reply/Bind plumbing the application consumes.
type Base struct {
	Result chan error // buffered 1 — Reply never blocks

	convID      string
	interrupted atomic.Bool
	cancel      atomic.Value                  // context.CancelFunc of the live Execute
	bound       atomic.Pointer[worker.Worker] // executor the broker last bound
}

// Bind pins the job to a room and the worker serving it — idempotent;
// the broker calls it at post time and again on every repost across
// workers, so Worker() names the executor the job was last handed to.
func (j *Base) Bind(convID string, w *worker.Worker) {
	j.convID = convID
	j.bound.Store(w)
}

// ConvID is the room this job runs against.
func (j *Base) ConvID() string { return j.convID }

// Worker is the executor the job was last bound to — the worker that
// runs it — or nil until the first Bind. Its Done channel closes only
// after the loop's OnTerminate hook ran, so waiting on it observes a
// fully retired worker.
func (j *Base) Worker() *worker.Worker { return j.bound.Load() }

// Reply delivers the terminal error to whoever submitted the job. A nil
// Result — a hand-built job — is a no-op rather than a blocking send
// that would wedge the worker goroutine forever.
func (j *Base) Reply(err error) {
	if j.Result != nil {
		j.Result <- err
	}
}

// IsPreempt reports whether the job cuts the queue line — ordinary jobs
// inherit false; control jobs (edit, delete) override it. The broker
// reads it at post time, so a job reposted across workers keeps its
// preemption.
func (j *Base) IsPreempt() bool { return false }

// finish is the deferred tail of every Execute: a job panic becomes a
// 500 instead of propagating through the worker, and the caller's result
// channel is always served — a wedged waiter is worse than an error.
// worker.ErrTerminate is a control signal, not an op result — the caller
// gets the success it earned, not the sentinel.
func (j *Base) finish(err *error) {
	if r := recover(); r != nil {
		*err = apperror.New(500, "job failed", fmt.Errorf("panic: %v", r))
	}
	if errors.Is(*err, worker.ErrTerminate) {
		j.Reply(nil)
		return
	}
	j.Reply(*err)
}

// Interrupt cancels the Execute ctx — the default stop for room jobs. The
// flag is sticky and stored before the cancel is loaded, pairing with
// Cancellable's store-then-check so a racing Interrupt can't slip through.
// The cause names the owner: a tool whose process this killed says
// "interrupted by user" instead of reporting an unexplained SIGKILL.
// IsTurn reports whether this job is a turn. A background job does not fix a
// runtime, so a pick may still be applied while it runs; a turn does, so the pick
// waits.
func (j *Base) IsTurn() bool { return false }

func (j *Base) Interrupt() {
	j.interrupted.Store(true)
	if c, ok := j.cancel.Load().(context.CancelCauseFunc); ok {
		c(dservice.ErrInterruptedByUser)
	}
}

// Cancellable derives the execution ctx: it dies on Interrupt, on the
// worker's own ctx (Terminate's hard stop), or on the caller's ctx.
// Cancellable gives the job a ctx that dies with either the worker's
// execCtx or the caller's own ctx, and returns it already canceled when
// the caller was gone before the job started. The direct check matters:
// AfterFunc's cancel runs on its own goroutine, so a job whose caller
// died while it sat in the queue could pass its first ctx checks and
// persist against a 408 that already told the caller nothing did.
func (j *Base) Cancellable(execCtx, callerCtx context.Context) context.Context {
	if callerCtx == nil {
		callerCtx = context.Background()
	}
	ctx, cancel := context.WithCancelCause(execCtx)
	j.cancel.Store(cancel)
	// The caller's own cause travels with its cancellation: a hub stopping on
	// a restart says "nusashell restarted", an owner's Stop says "user", and a
	// tab that just closed says nothing — the neutral canceled, never a name
	// nobody earned.
	context.AfterFunc(callerCtx, func() { cancel(context.Cause(callerCtx)) })
	if callerCtx.Err() != nil {
		cancel(context.Cause(callerCtx))
	} else if j.interrupted.Load() {
		cancel(dservice.ErrInterruptedByUser)
	}
	return ctx
}
