# ADR-0005: Resuming interrupted turns

## Status

Accepted — 2026-11-25. The seven decisions this draft left open were answered
by the coordinating agent under the owner's delegation: global default `off`
(decision 2), one attempt inside 60 minutes (decision 7), the `harness` nudge
with a bracketed head (decision 4), the persisted cause as a prerequisite
(decision 9), the token cost accepted under the same bounds, a manual retry
cancelling a pending automatic attempt (decision 10), and no automatic failover
(decision 11). One question is **not** decided here and is carried to the
owner: whether the `off` default should also cover rooms an agent spawns. It
supersedes nothing and depends on ADR-0004 for the restart path and on
`turn-retry` for what a retry may not do.

## Context

A turn can stop without finishing for four different reasons, and today all
four look the same to the owner: an interrupted reply and a Retry button.

- **A planned restart** cuts the turn with `ErrServerRestarted`; ADR-0004's
  barrier persists the partial reply and every tool result, so the history ends
  with an interrupted assistant message whose calls all have answers.
- **A crash** (`kill -9`, power loss) runs no barrier. The history can end with
  a user message that never got a reply, or with a tool call that has no
  result at all.
- **A network drop** ends the stream; the per-chunk idle timer (90 s,
  `ServiceConfig.IdleTimeout`) or the transport raises an error, the partial is
  persisted the same way, and `turn-retry` forbids retrying the attempt because
  output already reached the client.
- **Sleep or hibernation** is the same case one step later: the process freezes,
  and after wake the idle timer fires within 90 s. There is no separate sleep
  state to detect.

What already exists:

- `Resume` (`application/job/resume.go`) continues from the room's current
  history: unanswered origin calls finish first, then the model is called
  again. It is a **preemptive** job — it cuts the queue — and it never
  regenerates or truncates what the history holds.
- A partial reply is persisted as an interrupted assistant message, and a cut
  tool call carries the suffix that names its cause (ADR-0004 §7).
- `enum.LLMUserType` already separates real owner input (`origin`) from
  system-generated payloads carried in a user message (`system`, used by
  compaction), and `EditableMessage` already refuses to treat a `system`
  message as an owner line.

What does not exist: a persisted reason per interruption (`finish=interrupted`
does not distinguish an owner's Stop from a restart from a dropped socket), any
permission for the harness to continue on its own, and any way for the client
to see either.

The owner's model is Claude Code's: when it cannot continue on its own, it
shows one card offering **Auto continue**, the owner allows it **per room**,
and the harness continues by sending a message into the room. This ADR adapts
that shape: the permission model, the card, and the nudge — with one rule the
owner stated as firm, that the transcript must never look as if the owner wrote
the continuation.

## Decision

1. **Auto-continue is a resume, not a resumed stream.** No provider offers a
   way to continue an interrupted stream, so every automatic continuation is a
   new provider call over the room's history, exactly like a manual Retry. The
   vocabulary of this feature must not suggest otherwise: it "continues the
   turn", it never "reconnects".

2. **Two layers of permission: a global default and a per-room override, the
   room being tri-state.** The global setting lives with the other Settings and
   stores the default for rooms that never expressed one. The room stores
   `inherit | on | off`; `inherit` means the global answer applies, and an
   explicit `on`/`off` wins for that room forever, including after the global
   default changes. Switching a room back to `inherit` re-adopts whatever the
   global says at that moment — that is the only way a global change reaches a
   room, and it is deliberate: an owner who turned it on for one room must not
   find it off because they changed the default for everything else.

   The global default is **`off`**: a room opts in. A room whose owner walked
   away is not a room being worked in, and an automatic continuation re-sends
   its whole context. With `off`, the banner and the **Continue now** button
   still work, so the default costs nothing except the automation nobody asked
   for.

3. **The permission is given from the interruption card, and it persists.** The
   card sits in the banner of the room whose turn was cut, names the cause
   ("interrupted by a restart", "interrupted by a network drop"), carries one
   checkbox **Auto continue**, and a **Continue now** button for a single
   attempt. Ticking the checkbox writes the room's setting to `on`; it does not
   apply to that one interruption only. Leaving it unticked leaves the room
   unchanged, and the button still allows one manual continuation.

4. **The nudge is a user-role message with harness provenance, never an owner
   line.** When the harness continues a turn by itself, it appends a message
   the model reads as input and the client reads as a notice:

   - **Wire**: a `user`-role message — the only role every adapter can carry
     mid-conversation — with the same `LLMUserType` mechanism compaction
     already uses.
   - **Attribution**: a new `LLMUserType` value (`harness`), so the store, the
     UI, and every count can tell it apart from both real owner input and a
     compaction note. Four places move from "not `system`" to "`origin`
     only": `entity.EditableMessage` (so a nudge is not editable),
     `Conversation.TitleIsDefault` and the title model's first-message scan
     (`title.go`, so a nudge never becomes the room's name), and memory
     consolidation's message walk. Asking for `origin` is behaviour-preserving
     today — compaction notes are `system` either way — and it makes the new
     value safe by construction rather than by a list someone must remember to
     extend.
   - **Text**: one sentence that names the cause and forbids restarting the
     work, for example `[Continued by NusaShell after a restart — the turn was
     cut mid-answer. Continue from where you stopped; do not start over.]`.
     The brackets mark it as a harness head, the same way a relay head already
     marks cross-thread text.
   - **Client**: rendered as a harness notice in the transcript, not as a user
     bubble: no avatar for the owner, no edit affordance, and it never becomes
     the room's title.
   - **Position versus count**: the nudge takes a sequence number like any
     message — hiding it from the index would break `afterSeq` continuity for
     every reader — but it is never counted as an owner message. Position and
     count are different things, and only the count is excluded.

   The transcript therefore never claims the owner wrote it, while the model
   still receives the push it needs.

5. **The nudge is used only when the model could believe it finished.** After
   an interrupted *assistant* reply, the history ends with the model's own
   truncated text; continuing without a nudge invites it to treat the answer as
   complete. After a **user message with no reply at all**, the model has
   nothing to conclude from — a plain resume is correct and a nudge would be
   noise. After a **tool call with no result**, automatic continuation is
   forbidden outright (decision 8), so the question does not arise.

6. **Eligibility is a predicate, not a mood.** An automatic continuation runs
   only when all of these hold:

   | condition | why |
   |---|---|
   | the room is a provider room | an agent keeps its own dialogue; `Resume` already refuses a partial agent turn |
   | the tail is an interrupted assistant reply, or a user message with no reply | anything else is not an interruption |
   | no origin tool call is left without a result | `Resume` would run it again |
   | the interruption cause is `restart` or a transient transport failure | an owner's Stop is final |
   | the room's worker queue is empty | `Resume` is preemptive and would cut an accepted cross-thread message |
   | the interruption is inside the window and under the attempt budget | see decision 7 |
   | the room permission resolves to `on` | decision 2 |

7. **Bounds: one attempt, sixty minutes, and the permission never loosens
   them.** One automatic continuation per interruption, only when the
   interruption happened within the last 60 minutes, and never twice for the
   same tail. These are numbers, not ranges: `60 minutes` and `one attempt`.
   Raising the window is a new decision that must arrive with measured token
   consumption, not an estimate. The attempt stops immediately on: a 4xx from the provider
   (auth, invalid request, 413), the owner cancelling, the owner writing in the
   room after the interruption, a tool call left without a result, a non-empty
   queue, an unbound or deleted provider, a deleted room. Ticking **Auto
   continue** authorises an attempt that was already safe; it does not widen
   any of these conditions, and it does not make a second attempt possible.

8. **A tool is never re-run automatically.** No tool declares idempotency, and
   `exec` is an arbitrary shell — repeating it can commit twice, deploy twice,
   or delete twice. The harness reports what ran and who stopped it; deciding
   to run it again belongs to the model on a later turn, or to the owner. This
   is also why decision 6 forbids a tail with unanswered calls: `Resume` runs
   them, so an automatic resume there would be exactly the repetition this
   decision rules out.

9. **The interruption's cause is persisted per message.** The card must be able
   to name why the turn stopped, and `finish=interrupted` cannot: it is the same
   for an owner's Stop, a restart, and a dropped socket. A small field on the
   interrupted assistant message (`restart | user | caller-gone | transient`)
   is written from `context.Cause` at the moment the reply is sealed. Without
   it the card can only say "interrupted", and decision 6's cause rule cannot
   be evaluated at all.

10. **A manual retry always wins and cancels what has not run.** If the owner
    presses Retry while an automatic continuation is still pending (inside the
    window, queue busy, waiting for a condition to clear), the automatic one is
    dropped, not duplicated. If the owner writes in the room, the same applies.

11. **No automatic failover.** If the room's provider or model is gone, the
    continuation does not silently pick another: that changes the answer's
    author, and it is the owner's decision to make on the retry card, where the
    binding can be changed explicitly.

## Consequences

### Positive

- A restart, a dropped VPN, or a lid closing no longer costs the owner a manual
  click to keep a half-finished turn alive — when they asked for it, per room.
- The permission is explicit, revocable, and scoped; a global default never
  reaches a room that spoke for itself.
- The transcript stays honest: the continuation is visible as a harness notice,
  and no count treats it as something the owner said.
- The safety predicate is checkable from data that already exists, plus one
  field, so the rule can be tested rather than argued about.

### Negative

- Each automatic continuation re-sends the room's whole history; six rooms
  resuming at boot is six full contexts the owner did not ask for in that
  moment. The window and the one-attempt budget bound it, they do not make it
  free.
- A new `LLMUserType` value touches a shared predicate (`EditableMessage`) and
  three call sites; getting one of them wrong either lets a nudge be edited or
  counts it as an owner message.
- The card adds a second meaning to a banner the owner already reads as a
  failure, and a checkbox that persists state from a place that looks
  transient.
- Automatic continuation makes the room answer without the owner watching,
  which can be surprising in a shared or forwarded chat.

## Non-goals

- Splitting or resuming a provider stream; continuing a specific attempt id.
- Re-running interrupted tools, or any tool-idempotency metadata.
- Automatic failover to another provider or model.
- Continuing agent rooms, whose runtime owns its own dialogue.
- Catching up a continuation for a room interrupted days ago; the window is
  deliberate.
- A per-turn permission — the permission is per room, and a single attempt is
  the **Continue now** button's job.

## Alternatives considered

- **Offer only, never automatic** (banner and a button): zero risk and zero
  cost, but it leaves the owner doing exactly the work the feature exists to
  remove. Kept as the fallback when the room's permission is `inherit` with a
  global default of off.
- **A plain user message that looks like the owner wrote it** (Claude Code's
  shape): the model is triggered with no new mechanism, but the transcript then
  lies about authorship, which the owner ruled out. Rejected.
- **A `system`-role message mid-conversation**: some wires reject or relocate a
  system turn, and adapters would each need a rule. Rejected in favour of the
  existing user-role-plus-provenance mechanism.
- **A synthetic tool call carrying the nudge**: invents a tool the model never
  ran and pollutes tool-call accounting.
- **Automatic resume gated only by time** (any interruption inside the window):
  revives turns the owner stopped, and runs pending tools. Rejected; the
  predicate in decision 6 is the point.
- **Detecting sleep separately**: the idle timer already turns a frozen stream
  into an ordinary transport failure within 90 s, so a separate signal would
  add state without changing behaviour.
- **Two or more automatic attempts**: each attempt costs a full context, and a
  second attempt fails for the same reason as the first unless the world
  changed. One attempt, then the owner decides.

## Resolved decisions

All seven were answered on 2026-11-25 and are now part of the decisions above:
global default `off` (2), one attempt inside 60 minutes as fixed numbers (7),
the `harness` nudge with a bracketed head and the four predicate sites moved to
`origin` (4), the persisted cause as a prerequisite rather than a later phase
(9), the token cost accepted under the same bounds (7), a manual retry
cancelling a pending automatic attempt without exception (10), and no automatic
failover (11).

## Open question carried to the owner

The `off` default assumes a room has an owner at the screen when it is
interrupted. A room an **agent** spawns from another thread has no such owner,
so opting in per room practically never happens for those rooms — and they are
exactly the ones whose interruption nobody watches. Whether the `off` default
should also cover agent-spawned rooms, or whether they inherit their spawner's
answer, is not decided here; it travels to the owner with the release that
ships this feature. Nothing in this ADR presumes an answer.
