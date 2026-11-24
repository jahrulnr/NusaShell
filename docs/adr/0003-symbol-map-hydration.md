# ADR-0003: Symbol map hydration cadence

## Status

Accepted — 2026-11-25. The owner decided the cadence (hydrate on the first
turn, stale afterwards, re-hydrate after compaction), the workspace rebind, the
marker-only gate, and that a fold is a technical reset; the remaining rulings
are the SA's, made under the owner's delegation. Decisions 11–17 answer the SA
review and close every open item; Decision 17 was added after the first
implementation showed a stateless gate drifting from the gate's own reason.
Supersedes nothing; composes with ADR-0002 for `/compact`, `/clear`, and `/fork`.

## Context

Every provider step runs a discovery phase before the call
(`application/job/turn.go`: workspace pin, origin record, then
`appendToolContexts`). The selected environment renders one record per
system-callable tool (`application/job/environment.go`), and the application
persists the ones whose newest live record for the same `name+input` differs
after `Dedup` normalization (`application/job/hydration.go`).

For `symbol_map` that rule means **append-on-change**: the record's input is
only the workspace binding, its output is the rendered map, so any file change
in the workspace (including a change made by another room on the same root)
writes another full map as a new system message. The old record stays in the
live tail until compaction. A coding session with ten workspace changes carries
ten maps, and the manager's shared build does not prevent the records from
multiplying per room.

Measured cost of one record (four bound folders — @raven, thread
`c_4TCLFPOTI7LSWB4FYSYZWY4V5N`): **12,220 bytes ≈ 3,055 tokens**, one full map
plus one line per extra folder; first build **11–15 s**, on-demand cached render
**15 ms**; the pre-optimization record was ~13–14k tokens. Per-change
repetition, not the single map, is the cost problem.

The neighbouring records already have the cadence this ADR adopts, in a weaker
form: `appendWorkspaceContext` and `appendOriginContext` scan the live tail and
skip when the **newest record still states the same thing**, re-recording only
when the value changed or the record was sealed by compaction. Presence, not
equality, is the missing piece for an orientation record.

External reference: Claude Code injects no repository or symbol map at all — its
hydration is `AGENTS.md`, the skill catalog, the memory index, and env/model
context; its code navigation is grep/read on demand (53 tool schemas, many
deferred). A repo map is therefore a deliberate NusaShell choice, and it should
be paid once per conversation, not once per change.

## Decision

1. **Once per live tail.** A room's `symbol_map` record is written on the first
   provider step whose live tail carries no `symbol_map` injection record, then
   never refreshed while that record remains. Later turns keep the record as-is
   even when files change.
2. **Stale is accepted by design.** The record is orientation: it says what the
   workspace looked like when the conversation started. It is not a claim about
   the tree now.
3. **Re-hydration happens only when the record leaves the live tail**: a
   compaction (manual `/compact` or the automatic budget/media fold) seals it,
   or `/clear` empties the history. The next provider step then records a fresh
   map exactly once, like the first turn.
4. **`fork` counts as hydrated.** A fork copies the source's logical history, so
   its tail already carries the record; the copy is not re-hydrated. The copied
   workspace is the source's, so the record is as current as the source's.
5. **A provisional render is never recorded.** `symbol_map` can answer with the
   "is being built" marker on a cold root. That text must not become the room's
   permanent orientation; the step records nothing and the next step retries
   (the manager's build is already queued). A **final** answer — a map, a
   truncation, or a skip line such as "not a project root" — is recorded once
   like any other.
6. **On-demand refresh stays.** The model may call `symbol_map(path)` for any
   folder bound to the room (workspace or extra) and gets the current map,
   waiting for a build when needed. A model-originated call is not the
   hydration record and does not fill the slot.
7. **The prompt says what the record is.** The system prompt must describe the
   map as an orientation snapshot taken at the start of the conversation, to be
   verified with `read_file`/`search` before being trusted, with
   `symbol_map(path)` naming the refresh path — never as the current tree. The
   final sentence (system prompt line 43):

   > When a symbol map is in context, treat it as an orientation snapshot taken
   > when the conversation started: it lists declarations, not usages, and it
   > does not track later edits. Pick a file from it, then verify with
   > `read_file`/`search` before trusting a path or a declaration; call
   > `symbol_map(path)` to refresh a folder bound to this thread.
8. **A workspace rebind re-hydrates.** Detection is the record's own input: the
   newest live `symbol_map` record cites `{path, folders}`, and when that no
   longer matches the running binding the slot is void. The next step renders
   one map for the new binding — in the same step as the `workspace changed`
   pin — replacing the orientation that would otherwise point at another tree.
   This is the one exception to "presence alone decides", and it is about
   correctness, not freshness. A rebind to a root that fails the gate adds no
   record at all (Decision 14): the slot stays void, the earlier map remains in
   the tail as history, and the prompt's verification rule covers it.
9. **Ownership.** The application owns the cadence (it is the only layer that
   sees the room's history); the tool declares that its record is one-shot and
   which render is provisional; the environment renders only the slots the
   application did not skip; the symbol manager keeps owning builds, caching,
   and stale-while-revalidate, unaware of rooms.
10. **Scope.** `symbol_map` is the only one-shot record in this version.
    `skills`, `workspace`, `origin`, and the other environment snapshots keep
    value-based dedup: they are cheap, and a changed value there is information
    the model needs.

11. **A fold inside a turn refills the slot in the same step.** The automatic
    fold runs inside the step loop before the provider call (`turn.go`: fold →
    workspace pin → origin record → discovery). A fold that seals the record
    leaves the slot empty for that same step, so discovery renders and records
    one fresh map before the call — the turn that triggered the fold keeps its
    orientation. A manual `/compact` is its own job: it seals the record and
    writes nothing; the room's next turn fills the slot in its first step. One
    rule, two owners, two tests.

12. **No retroactive cleanup — final.** A room that already accumulated
    several `symbol_map` records keeps them. This ADR stops new accumulation
    from the moment it ships; the existing pile leaves the context at the
    room's next compaction (the sealed copies stay in the archive as history
    and are never replayed). There is deliberately no retirement rule and no
    migration: dropping older injection records by rewriting the tail would
    need a second rewriting path with its own repair story, and it invalidates
    the provider's cached prefix — cost without a correctness gain. The
    bounded outcome is accepted: an existing room may carry its old maps until
    its next fold.

13. **Final render, precisely.** A render is provisional when it is the
    manager's "is being built" marker or the waiting form that ends in "index
    still building — call again shortly". Every other answer — a map (fresh, or
    stale-while-revalidate while a rebuild runs), a truncation footer, or a skip
    line — is final and is recorded once. Consequence, stated plainly: on a
    cold root the first turn records **nothing** and the model works without a
    map until the build lands; the next step that sees a final render records
    exactly one. A warm root (disk cache, or a process that already built the
    tree) hydrates on the first turn — the common case.

14. **A root must pass the gate before anything is rendered.** The owner's
    decision, final form — a root marker is the only evidence:

    ```
    hydrate if  root != fallback home root
            and root has a project marker
    else skip entirely
    ```

    - `root` is the turn workspace (the room's binding, else the driver's
      fallback); the fallback home root is `EnvironmentDriver.FallbackRoot()`,
      which `serve` sets to the owner's home.
    - Markers, read at the root only: `go.mod`, `package.json`, `Cargo.toml`,
      `pyproject.toml`, `pom.xml`, `build.gradle`, `build.gradle.kts`,
      `composer.json`, `Gemfile`, `.git`, `.svn`, `.hg`, plus the root-level
      name patterns `*.csproj`, `*.sln`, `build.gradle.*` — the same set the
      index gate already probes, so one list serves both.
    - A project binding is **not** evidence: a room in a project whose folder
      has no marker is skipped like any other non-code root.
    - Why (the owner's reason): while a root holds no project yet — an empty
      folder, or one without a marker — the agent is still writing its
      contents, so a map would only repeat what the agent already knows.
      Injecting it is context spam, not orientation.
    - A skipped root writes **nothing**: no record, no "map skipped" line, no
      probe of the map. Zero tokens, zero build. The gate runs before any
      render and adds the slot to the same skip set an already-hydrated slot
      uses.
    - The fallback home root is skipped even when it carries a marker: a home
      directory that happens to be a dotfiles repo is not the project the room
      works in.
    - On-demand `symbol_map(path)` is not gated: the model may still map any
      folder bound to the room. The gate governs the hydration record, not the
      tool.
    - Accepted consequence: a room whose first turns are gated out gets no map
      until a fold, a clear, or a different root, even if the agent later gives
      that root a marker. The verdict is remembered per root (Decision 17) — a
      stateless gate would hand the room a map of the files the agent just
      wrote, which is the spam this gate exists to prevent. See Decision 15 for
      why the fold is the next chance, not a periodic retry.

15. **A fold is a technical reset, and re-hydrating after it is intended.**
    Compaction leaves a room that is a new conversation in everything but id:
    the tail keeps a handoff from the previous agent, the history that produced
    the map is sealed. Orientation is useful again at that point, so the next
    step hydrates exactly once — the same rule as the first turn (Decision 1),
    not a refresh of a stale map (Decision 2). This pairing is deliberate:
    nobody should "fix" the re-hydration as duplicate work, just as nobody
    should add a per-turn retry to the gate.

16. **Rollout is two commits, and the gate is not observed before it ships.**
    The cadence change (Decisions 1–13) and the gate (Decision 14) land as two
    commits in one change: the first removes a measured cost without taking
    anything away, the second removes a map from rooms that would have had one.
    Two commits keep a regression bisectable and let the gate alone be reverted.
    The work does not wait for production observation: the cost is measured
    (≈3k tokens per record, one record per workspace change), the behaviour is
    offline-testable, and the observation window would itself run on the old
    behaviour.

17. **A refusal is remembered for the root, not re-probed.** The gate caches
    its verdict on the room as the root it refused — one nullable string — so a
    gated-out room pays one probe per episode instead of one per provider step.
    The state is conversation state, so the aggregate owns it and the
    repository only persists it.

    - Field: `Conversation.symbolMapRefusedAt` (the refused root, `""` = none),
      with `SymbolMapRefusedAt()`, `RefuseSymbolMap(root)`, and
      `AllowSymbolMap()`. Persisted as `symbolMapRefusedAt` (`omitempty`) in
      `meta.json`, restored by `Load`; absent means "never evaluated", so no
      migration.
    - Cleared by the aggregate's own transitions: `Compact()` — both the manual
      job and the automatic fold — and `ClearHistory()`, which is what `/clear`
      rebuilds the record from. `SetWorkspace` deliberately does not clear it:
      `Load` calls it, and the verdict is keyed by root, so a rebind to another
      root re-evaluates by comparison without needing a write.
    - `Fork()` does not copy it: a copy is a new room, and its first step
      reaches the same verdict on the same root with one probe.
    - A fallback-root refusal is never stored: the comparison is free, so the
      state only ever holds a marker verdict.
    - An unreadable root stores no verdict: a failed `store.Open`, a `Stat`
      that fails for any reason but `fs.ErrNotExist`, and a failed or impossible
      listing are all "not judged", not "no marker". Only proven absence is an
      answer, so a transient mount, permission, or remote-backend failure is
      retried on the next step instead of being remembered. Determinism holds
      only for a root the gate actually read — opened and stat-able/listing-able
      — which is why `TestRefusedRootIsNotReprobed` covers readable roots only
      and `TestUnreadableRootIsNotRemembered` covers all three failure paths.
    - The step that changes the verdict saves the room right away, so the
      answer survives a turn that dies before its final save. A failed save
      leaves the in-memory verdict; the next step re-probes and reaches the
      same answer, because the gate is deterministic for a root.

### Why the environment port takes a skip set

`ToolContexts` renders every system-callable tool *before* dedup decides
anything, so filtering after the fact would still pay the probe (up to 2 s) and
any build cost on every turn. The skip set is therefore computed where the
history is (application), honoured where the tools are executed (environment),
and the one-shot declaration stays with the producer (the tool). Without that
split the cadence would exist on paper while the cost it removes stayed in place.

## Consequences

### Positive

- A room pays one map (≈3k tokens) instead of one per workspace change; the
  live tail no longer accrues maps that only compaction would remove.
- The cost is predictable per conversation: at most one map per tail epoch
  (start, after a fold, after clear, after a workspace rebind).
- No cadence bookkeeping in the tail: the record's presence is the epoch
  marker, so compaction, clear, and fork need no counters. The one new field
  (Decision 17) is a per-root verdict, not a counter.
- The model keeps a fast orientation while `search`/`read_file` stay the source
  of truth, which is what the prompt already prefers.
- A chat, automation, or channel room whose root is the fallback home pays no
  map at all: no record, no probe, no build — the gate removes the class of
  rooms that never needed one.
- A gated-out room costs one marker probe per episode, not per provider step:
  the fallback comparison is free and the refusal is cached for its root
  (Decision 17). Without that cache a remote workspace driver would pay ~15
  port calls on every step for a map it will never render.

### Negative

- The map can be wrong after the first edit; a model that trusts it blindly
  will pick a path that moved. Mitigated by the prompt, not by the record.
- A room created only for prose still pays the first render (a cached map, a
  skip line, or one background build). Uniform rule, bounded by the shared
  manager cache.
- A room whose first build is cold records nothing for a turn or two; the model
  starts without a map. Accepted deliberately (see Decision 13) rather than
  waiting 11–15 s inside the first provider step.
- A fold re-adds the map right after sealing it, so the fold's context saving
  is one map (≈3k tokens) smaller than the raw history delta suggests.
- Rooms that accumulated maps before this rule keep paying for them until their
  next compaction; the rule does not reclaim the past.
- A room rebound from a code root to a gated-out root keeps the earlier map in
  its tail: nothing new is written, and only the prompt's verification rule
  stands between the model and a map of the previous tree.
- A room whose first turns are gated out never receives a map until a fold, a
  clear, or a different root, even after the agent gives that root a marker.
  Accepted deliberately: the gate trades that first orientation for zero
  context spam in rooms that are still being written.
- The room carries one nullable string for that verdict. It is the only new
  persisted state this ADR adds, and it can outlive a real change (a marker
  cloned into the folder later is ignored until the reset) — the intent, not a
  defect.

## Non-goals

- A room-kind gate (skip because a room came from a channel, an automation, or
  a project): the gate is root-based, so every room follows the same rule.
- Epoch counters persisted in metadata; extending the same cadence to `skills`
  or other records; changing the token budget; auto-refreshing the map in the
  background; injecting a map for agent rooms differently from provider rooms.

## Alternatives considered

- **Keep append-on-change** (status quo): correct but unbounded per edit; this
  is the cost this ADR removes.
- **Epoch counter in `meta.json`** bumped by compaction/clear: same behaviour,
  new persisted state and three writers to keep honest; the live tail already
  proves the epoch.
- **Wait for the build on the first hydration step** (`MapSync`): the room
  always starts with a map, but a cold root adds the measured 11–15 s to the
  first provider step — paid by prose, chat, and channel rooms too, and charged
  again for every new root. Rejected; the provisional rule gets the same map one
  step later without blocking the call.
- **Retire older records by rewriting the tail** at discovery time: frees the
  accumulated tokens earlier, but the store is append-only outside edit and
  compact, the rewrite needs its own repair path, and it invalidates the
  provider's cached prefix. Cost without correctness.
- **Record the "being built" placeholder**: guarantees a slot exists, but a
  placeholder can outlive its build forever; rejected.
- **No hydration at all, on-demand only** (closest to Claude Code): cheapest,
  but every room would start navigation from grep; the owner chose a one-shot
  orientation instead.
- **A stateless gate** (evaluate on every step while the slot is open): no new
  state, and a root that gains a marker later hydrates on the next step. It was
  the first implementation and was rejected: it pays one probe per provider
  step for a room that will not hydrate (≈15 port calls per step on a remote
  driver), and it hands a room a map of the files its own agent just wrote —
  the context spam the gate exists to prevent. It also made Decision 14's
  stated consequence untrue.
- **A short "map skipped" record** as the sticky marker: keeps every bit of
  state in the append-only tail, so fold, clear, and fork already handle it.
  Rejected: it is the record the owner explicitly excluded ("skip means no
  record, zero tokens"), and it cannot be honest — either the tool renders it
  (paying the probe it was meant to avoid) or the application fabricates a tool
  call it never executed, breaking the rule that injection records come from
  the tool that produced them.
