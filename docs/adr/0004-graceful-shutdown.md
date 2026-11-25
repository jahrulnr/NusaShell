# ADR-0004: Graceful shutdown and in-place restart

## Status

Accepted — 2026-11-25. The owner decided the budget, the queued-send and
approval rules, that the supervisor stays the primary restart path with
`reexec` as the fallback for foreground runs, and that the promise covers
signals and updates only. This ADR records the ordering that follows from
those decisions; it supersedes nothing and composes with ADR-0003's cadence,
`turn-retry`'s retry rule, and `tool-interrupt`'s vocabulary.

Implementation is partial, and this section is the ledger. Landed with the
barrier: decisions 1–5 and 10–14. **Blocked, not unbuilt:** decision 6 — the
cause is passed but `Interrupt` cancels first, so shutdown still reports the
owner's stop (see Context). **Not implemented yet:** decision 7's cause-aware
short marker, decision 8's approval withdrawal, decision 9's queued-send
persistence, and decision 15's `bootId` and `lastShutdown` — follow-ups in
phases 2–3, not current behavior. A review that needs today's truth must read
the code, not this list.

## Context

The owner restarted NusaShell with conversations open and asked for "pause
everything, save, then exit". The code has most of the pieces and one wrong
order.

**Two exit paths.** A signal reaches `cmd/nusashell/serve.go:159`
(`signal.NotifyContext`) and then runs a fixed sequence: mute alerts, stop
automations, `hub.Shutdown()`, `http.Server.Shutdown` with **10 s**, and — via
deferred calls — `conversations.Shutdown`, then `db.Close` and `logger.Close`.
An update reaches `application/update/update.go:353` after a `restartLead` of
1.2 s; in service mode it asks the service manager (`systemctl restart`), which
sends the same signal, and in process mode it calls `syscall.Exec` from
`infrastructure/system/reexec_unix.go` — **with no shutdown phase at all**.

**The window.** The generated unit (`infrastructure/system/systemd.go:346`)
sets `Restart=on-failure`, `RestartSec=5`, `TimeoutStopSec=20`,
`KillMode=control-group`. The hub waits up to `shutdownTurnGrace = 10 s`
(`hub.go:50`) for turn terminals and `srv.Shutdown` gets another **10 s**:
10 + 10 = 20, the whole window, before the deferred
`eng.conversations.Shutdown` — the step that stops workers, lets them write
their last message and tool result — even begins. That deferred call waits on
`<-w.Done()` **without a deadline**, and `db.Close` runs after it. A slow turn
therefore loses its persistence to systemd's SIGKILL at the moment it starts
writing.

**What already holds.** A turn persists after every step (append + `Save`), so
a kill costs at most one in-flight provider response, never the history. The
interrupt vocabulary exists: `service.ErrInterruptedByUser`,
`ErrInterruptedByAgent`, `ErrServerRestarted` (`infrastructure/domain/service/cancel.go`),
set through `context.WithCancelCause`. The worker loop creates its context
with a cause (`worker.go:307`), and `Terminate` does pass `ErrServerRestarted`
(`worker.go:240`) — **but it calls `running.Interrupt()` first**
(`worker.go:231`), and that cancels the job's context with
`ErrInterruptedByUser` (`job.go:140`). The restart cause therefore lands on an
already-canceled context and is lost: a job terminated by shutdown reports the
owner's stop. That is an open blocker on decision 6, not a settled fact.
`exec` does render `(nusashell restarted after 8.0s — partial output above)`
(`exec.go:185`) when it is handed that cause, and a canceled tool keeps its
head: `turn.go:459` records `res.Text` when the tool returns one under a
cancel.

**Windows has no second phase.** `infrastructure/system/restart_windows.go`
starts a PowerShell helper that waits for this process to exit, swaps
`<path>.new` over the installed binary (60 tries, 500 ms apart), then starts the
task or the process; the server calls `os.Exit(0)` right after. There is no
signal, no grace, and no cleanup after that point — and on Windows an exiting
process does not take its children with it.

**What a restart costs today.** In-flight turns are killed without the restart
text, terminal sessions and browsers become orphans on `exec`, recordings lose
their tail, a queued send is answered 503 with its user line never written, and
a pending approval disappears while its call stays in history as an
unanswered request.

## Decision

1. **One barrier, owned by `application`.** A single bounded sequence —
   `job.Barrier` in the design spec — decides shutdown order and is called by
   `cmd` from both paths: on SIGINT/SIGTERM/SIGHUP, and by the update job
   before a process-mode restart. `cmd` keeps only: receive, call once, exit.
   SIGHUP joins the notified set because closing the terminal is the most
   common way a foreground run dies, and today it is not handled at all.

2. **The barrier is idempotent and single-pass.** A second signal during the
   barrier changes nothing; it neither restarts the budget nor aborts the
   sequence. The only hard stop is systemd's SIGKILL, and the budget is built
   to finish before it.

3. **Budget: 25 s inside a 30 s stop window**, split by phase with a reserve:

   | phase | work | budget |
   |---|---|---|
   | 0 | admission closed (503), alerts muted, automation ticker stopped | 0.5 s |
   | 1 | workers terminated with the restart cause; turns unwind: tool killed, head + suffix written, reply persisted | 12 s |
   | 2 | recordings flushed, shell sessions killed, browsers and agents stopped, MCP servers closed | 5 s |
   | 3 | terminal frames landed, WebSocket connections drained, HTTP listener closed | 3 s |
   | 4 | database and log closed | 1 s |
   | — | reserve | 3.5 s |

   Phases 0–2 finish in 17.5 s, inside even a **stale 20 s unit** — the
   property that keeps history safe on machines that have not reinstalled the
   service yet. The drain and the cleanup are what a shorter window cuts.

4. **Persistence before sockets.** History cannot be replayed after a kill; a
   socket reconnects and resyncs. Phase 1 therefore gets the largest budget
   and runs before the drain, and `srv.Shutdown` no longer owns a separate 10 s
   clock — it shares phase 3's 3 s envelope with the hub, whose turn-terminal
   grace drops from 10 s to at most 2 s. An error from `srv.Shutdown` ends the
   phase, not the barrier.

5. **Every wait is bounded.** A worker that does not stop inside phase 1 is
   abandoned and logged; its room keeps the last persisted step. The
   unbounded `<-w.Done()` in the deferred `conversations.Shutdown` disappears
   into the barrier's deadline.

6. **The restart cause is the only name on this path.** Everything the barrier
   cuts — turns, tools, queued jobs — carries `ErrServerRestarted`. No shutdown
   path writes "interrupted by user": a tool that was killed here says
   `(nusashell restarted after Ns — partial output above)`, and the assistant
   reply is sealed with `FinishReasonInterrupted`. This is the intended order
   and it does not hold yet: `Terminate` interrupts the job before it cancels
   the worker context, so the owner's cause wins (see Context). Fixing that
   ordering is part of landing this decision.

7. **Each cancel cause has exactly two lengths, and no other form.**
   `stampInterrupted` used to write `interrupted by user` for every stop, which
   is a lie on a restart. The rule is about causes, not about wording: three
   causes are live — the owner's stop, a nusashell restart, and a caller that
   simply left — and each renders long when the call had output and short when
   it never printed.

   | cause | long, with output | short, never ran |
   |---|---|---|
   | owner's stop | `(interrupted by user after 12.3s — partial output above)` | `interrupted by user` |
   | restart | `(nusashell restarted after 8.0s — partial output above)` | `interrupted by a nusashell restart` |
   | caller gone | `(canceled after 4.1s — partial output above)` | `(canceled — the caller went away)` |

   What is forbidden is a fourth *form* — a variant that names someone the
   cause did not name, or a second short phrasing for the same cause — not a
   second length. A cancel with no cause at all keeps the neutral long form and
   is a defect outside the caller-gone path. The agent's per-call abort
   (`service.ErrInterruptedByAgent`) is carried through the layers and reserved
   a row, but it has no short form yet: that mechanism is not landed, and a
   shape nothing can produce cannot be tested. This amends the `tool-interrupt`
   invariant that allowed only one short form; the owner approved the amendment
   when deciding the approval rule below.

8. **A pending approval is withdrawn, and the call records it.** The broker is
   in memory, so the request cannot survive; the barrier resolves it as
   withdrawn, and the call's result becomes `(withdrawn: nusashell restarted)`
   with the call marked failed. A resumed turn then sees why it has no answer
   instead of an unanswered request that would make the resume wait forever.
   **Not implemented (phase 2):** the broker has no shutdown hook and no code
   writes that text today, so a pending approval is still lost with its call
   unanswered.

9. **A queued send persists its user message, then is refused.** Server
   storage stays open through phase 1, so a `Send` job that runs while the
   barrier is in progress appends the user line, publishes the room change and
   only then replies 503 with `UserMessagePersisted` true. The owner finds the
   message in the room after the restart instead of losing what they typed.
   **Not implemented (phase 2):** a queued job still answers 503 without
   writing the user line (`TestQueuedJobAnswers503OnShutdown` pins today's
   behavior, not this decision).

10. **Children die inside the barrier.** Shell sessions (`TerminateAll`),
    browser processes, ACP agents and MCP stdio servers are stopped in phase 2
    — before `syscall.Exec` and before `os.Exit`. On Unix `exec` replaces the
    image and keeps the process tree; on Windows the exit leaves it; in both
    cases an unkilled child is an orphan holding ports, profile locks or a
    half-written log.

11. **Restart path: supervisor first, `reexec` as fallback.** `Restarter.Mode()`
    already picks `service` when the manager owns this process, and that path
    needs nothing new beyond a correct signal path: `systemctl restart` sends
    SIGTERM, the barrier runs, the process exits 0, systemd starts it again.
    A foreground run (including developing NusaShell itself) keeps the update
    button through `reexec`: barrier → `syscall.Exec`. The update's progress
    frame is sent before the barrier, so the owner sees "restarting" and then a
    reconnect, not a frozen screen.

12. **`exec` failure ends the process; it never reopens the gate.** If
    `syscall.Exec` fails after the barrier, the process exits 1 with a message.
    A supervisor restarts it (`Restart=on-failure`); a foreground run tells the
    owner to start it again. Re-opening admission after half a shutdown would
    create a second, untested state.

13. **Windows: the barrier finishes before the helper starts.** The helper's
    60 × 500 ms swap window must not be spent on a hanging shutdown, and no
    phase may run after `os.Exit`. The barrier therefore completes entirely
    first, children included, then the helper starts, then `os.Exit(0)`.

14. **The stop window becomes 30 s, and that is an installation change.** The
    unit template in `infrastructure/system/systemd.go` writes
    `TimeoutStopSec=20`; it becomes 30. Existing installs keep 20 until the
    service is reinstalled, which is why decision 3 keeps the history phases
    inside 17.5 s: a stale unit loses the drain, never the record. The ADR
    states this rather than assuming a reinstalled machine.

15. **The owner can tell a restart from a reconnect.** A process `bootId` and
    `startedAt` ride a `server.hello` frame and a `/api/status` endpoint, and
    phase 4 writes a best-effort `lastShutdown` marker (`turnsCut`,
    `toolsCut`, `queuedPersisted`, `reason`). The WebUI shows one banner when a
    reconnect carries a different `bootId`: "NusaShell restarted — N turns
    saved". The marker is a hint, not a ledger: an OOM kill leaves it stale,
    and a boot-time comparison is what makes it trustworthy. **Not implemented
    (phase 3):** there is no `bootId`, no `server.hello`, no `/api/status`, and
    no marker file; a client cannot yet tell a restart from a reconnect.

## Consequences

### Positive

- A restart — from a signal, a terminal close, or the update button — keeps
  what already ran: the assistant partial, the tool head plus the restart
  suffix. A queued user line survives only once decision 9 lands.
- The order is stated once, in one place, for two paths. Adding an updater
  path later cannot silently skip persistence.
- A stale 20 s unit still saves history, because the budget is front-loaded.
- Orphans stop leaking: shells, browsers, agents and MCP servers are killed by
  the barrier instead of by the kernel after our exit, or not at all.
- The vocabulary a model reads matches the event: a restart says restart.

### Negative

- A restart now takes up to 25 s when a turn is in flight, where it used to be
  instant (and lossy). An update therefore has a visible pause.
- The barrier kills work in progress by design. A turn that would have finished
  in 20 s is cut at phase 1; the owner resumes it by hand.
- A stale `lastShutdown` marker can mislead the banner after an OOM kill; the
  boot-id comparison limits it, and the marker is deliberately not a ledger.
- `TimeoutStopSec=30` lengthens the worst case for `systemctl stop` on a
  machine with a wedged worker, up to the phase deadline.
- The Windows helper still owns the swap; this ADR only guarantees it starts
  after the barrier. A failed swap remains a helper-log diagnosis.

## Non-goals

- Durability against power loss or SIGKILL (OOM, `kill -9`, expired stop
  window). Per-step persistence is the existing answer; fsync policy is a
  repository concern, not a shutdown one.
- Preventing sleep or hibernation, or resuming in-flight provider streams after
  a restart.
- Catching up automations that were missed while the server was down.
- Keeping ACP agent sessions, terminal sessions, or browser profiles alive
  across a restart. They are processes; they die with it, deliberately.
- Recording a server-side ledger of every shutdown. The marker is a hint.

## Alternatives considered

- **Keep the current order** (drain sockets first, persist through defers):
  it spends the entire stop window on cosmetics and leaves persistence to an
  unbounded wait that systemd kills. This is the defect, not an option.
- **Rely on per-step persistence alone**: a kill already costs at most one
  provider response, so a barrier could be dropped. Rejected because the
  losses are exactly the ones the owner noticed — the tool head, the
  interrupted reply, the queued user line — and none of them are per-step
  (the third still unbuilt, per decision 9).
- **Put the barrier in `cmd`**: the two paths would each keep their own order,
  which is how the `exec` hole appeared. The order is a use-case decision.
- **Always require a service manager** (drop `reexec`): simpler and safer, but
  it removes the update button from every foreground run, including the one the
  owner uses while developing NusaShell. Rejected; the supervisor stays
  primary and `reexec` keeps the barrier requirement.
- **A background journal that survives SIGKILL**: heavier than the promise
  needs; per-step persistence already bounds the loss.
- **Leave a restarted approval pending** so the resume re-asks: rejected
  because the broker is gone, and a call with no result blocks the resume path.
- **Let the barrier keep waiting for a wedged worker**: the wait is what
  systemd punishes; a named abandonment is honest and bounded.
