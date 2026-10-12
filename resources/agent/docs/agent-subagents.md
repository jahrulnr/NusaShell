# Subagents

Subagents are spawn-only and always async. The user never chats
with them in the composer. When the parent agent calls `subagent`, the
tool returns immediately with YAML frontmatter listing the spawned runs
(`runs:` entries with `id` / `status` / `workspace`; `status: starting`)
and the tool call is marked `running` in the conversation. The parent
agent is free to continue other work — it does not block on the
subagent. A spawn rejected before the session opens (unreachable agent,
unsupported mode, or a cancelled call) still returns its
`runs:` entry — `status: failed` with `id`, `title`, `workspace`, and
`error` — and follows the same async completion flow below, so the
attempt stays visible in the run list and the tool call never stalls.

## Internal delegate target

`subagent(agent_id="internal")` runs the task on a local background agent
with the standard toolbox; it cannot spawn another subagent, and it
receives only the self-contained prompt and workspace supplied by the
parent.

The delegate model is configured in Settings → Agent → Internal delegate
model. An empty setting inherits the active model of the parent conversation;
an explicit value uses the provider:model selection from Settings. The
delegate works to completion and resumes open checklists automatically.

Both targets appear identically in the run UI and support the `steer`,
`stop`, and `wait` ops.

The delegate result is the final assistant answer after all its work is
done; an intermediate acknowledgement such as “I will inspect the file”
is never delivered as the `subagent_result` output.

Good example:

    subagent(prompt="Inspect /workspace/app.go, make the requested fix, run the
    focused tests, and report the changed files plus the test result.",
             title="Fix app.go",
             agent_id="internal",
             workspace="/workspace")

Bad example:

    subagent(prompt="Please look at this and tell me what you think.", agent_id="internal")

The bad brief does not define a concrete completion condition, so the
delegate may spend its run acknowledging or planning without producing useful
evidence for the parent.

## Delegation brief

Give a compact self-contained brief: goal, relevant absolute paths,
necessary constraints, and the expected artifact. Do quick lookups and
shell-owned work yourself — delegate only what genuinely benefits from an
independent agent.

When the delegation touches a running task, reference the parent plan
file instead of re-summarizing the plan from memory: the `todo` tool
result returns `plan_path` (the brief mirrored under the data directory,
always current). NusaShell automatically appends a `Parent plan file
(read this first): <abs path>` block to the spawn prompt when the parent
conversation has a brief — plus a compact Objective + Done when summary,
because the plan file lives outside the subagent workspace. Keep your own
prompt focused on the delegated slice; the plan file carries the shared
context.

For an internal delegate (`agent_id="internal"`), the plan file lives
outside the child's workspace, so NusaShell inlines
only the compact Objective + Done when summary into the spawn prompt and
never advertises a plan file path the delegate cannot read. The explicit
parent prompt stays authoritative; the inline summary carries the
parent's planning intent so the delegate does not lose the task context.

Good example:

    subagent(prompt="Refactor /home/user/proj/src/api/client.go: extract the
    HTTP client into /home/user/proj/src/api/http.go, keep the existing public
    interface, then run `go build ./...` and `go test ./...` to verify. Report
    changed files and test results.",
             title="Refactor HTTP client")

Bad example — vague, no paths, no verification:

    subagent(prompt="as discussed, fix the thing in the project")

Bad example — delegating work the parent can do in one tool call:

    subagent(prompt="list the files in /home/user/proj/src")

Pass `title` whenever the user may have more than one live run: the Agent dock
and drawer show that label instead of the bare worker agent name so the
user can tell what each run is for. With `count` > 1, titles are auto-suffixed
`(1)`, `(2)`, ….

The user is an observer of subagent runs: steer, stop, mode change, and
risk promotion are handled by the parent agent, not the user.
Permissions are auto-allowed — the parent delegates authority
when it spawns a subagent.

## Async completion (tool injection)

When a subagent finishes (completed, failed, or cancelled):

1. The run's full transcript is written to disk; its path is reported
   back to the parent as `output_path`.
2. The original `subagent` tool call is updated from `running` to
   `ok`/`fail` with a brief terminal status (the full output travels in
   the synthetic `subagent_result` tool call below — old history is
   never silently rewritten).
3. A synthetic `subagent_result` tool call carrying the full result
   (YAML frontmatter with `status` / `id` / `workspace` / `output_path`
   plus the subagent's last-turn text summary) is injected into the
   conversation, announcement-style, so the model sees it in fresh
   context and in later turns (auto-continue) too. Finished results are
   applied before a queued user steer, and the steer is appended last so
   it stays the newest user instruction.
4. If the parent is idle, a new parent-agent turn is triggered so the
   parent processes the `subagent_result` without a user message. The
   parent sees that tool call in history and acts on the summary as if
   it had just completed the tool.

While any subagent is running, auto-continue pauses instead of ending
the turn, and it resumes when all runs finish. Spawn calls and their
result calls survive compaction with full arguments and outputs; older
history is covered by the compaction summary.

## Auto-continue TODO rehydration

When the auto-continue chain starts the next turn (open TODOs remain and
the chain budget is not exhausted), the current open TODO items and the
continuation guidance are carried into the new turn. On continuation,
reconcile open todos first: verify what prior work already completed
before resuming the remaining items.

## Steering priority and timing

A steer from the main composer is a real user message, not background runtime
state. It is queued while the current provider or tool round is in flight and
applied at the next safe boundary. `subagent(op="wait")` is intentionally blocking,
so a composer steer can wait for that call to return; this delay does not mean
the message was dropped.

At a boundary, the parent applies finished background results and
announcements first, then appends the queued composer steer, then starts one
fresh assistant round. This ordering keeps the steer as the newest user
instruction. After a steer is applied, re-evaluate the user's request before
resuming the older plan. Do not call `subagent(op="wait")` again merely
because the previous plan was waiting if the steer changes the requested
action.

`subagent(op="steer")` is different: it redirects a **child run** by
cancelling the in-flight work and sending the new text as the next prompt.
Prefer the `steer` op when the parent needs to change direction
mid-work; use the `stop` op only to abandon the run.

Good example, steer a child run when the user changes its direction:

    subagent(op="steer", id="acp_run_123", text="Stop the framework comparison and inspect the existing desktop pet code instead.")

Bad example, continue the stale parent plan after a user steer:

    subagent(op="wait", id="acp_run_123", timeout_ms=120000)
    subagent(op="wait", id="acp_run_456", timeout_ms=120000)

The bad sequence ignores the newly requested direction. First process the
latest user steer, then wait only if that revised plan still needs a result.

## Waiting for results

`subagent` is always async. NusaShell injects `subagent_result` when
the run finishes. Call `subagent(op="wait")` only when the next action in
this round cannot proceed without the result; to adjust a live run use the
`steer` op, and to cancel it use the `stop` op.

When the `wait` op reaches a terminal result, its tool result contains
`status`, `id`, `workspace`, `output_path`, and the last meaningful text
turn (or a compact failure/cancellation fallback; if no text was
produced, the last thought may be used). A timeout can return a
still-running status without `output_path`. Read the path only when the
full thought/tool transcript is needed.

Steer returns `status` / `id` / `workspace` plus `Steer accepted.` and
does not include last-turn text. Stop returns the same bounded
completion shape as `wait` (last meaningful turn, plus `output_path`
when the cancelled run has been persisted). Read `output_path` only
when the full thought/tool transcript is needed.

Good example — wait with a bounded timeout:

    subagent(op="wait", id="acp_run_123", timeout_ms=120000)

Bad example — polling in a sleep loop:

    sleep(seconds=5)  # repeat until the run finishes
    subagent(op="wait", id="acp_run_123")  # first call already returned

## Workspace and permissions

Edit/delete/move is auto-allowed only when every path stays inside the
bound workspace; slash-rooted paths (`/etc/passwd`, `\Windows\…`)
are treated as absolute even on Windows and never join onto the workspace.
Workspace and tool paths are checked after resolving symlink
aliases, so a bind through `/home/...` and a target under `/media/...` can
refer to the same physical workspace. A symlink that escapes the workspace
is rejected. Existing runs keep the workspace they bound at spawn;
new spawns follow the current conversation workspace unless the tool
overrides it.

Full-filesystem bypass is never the default — the user promotes it from
the live subagent UI. Only a promoted local run may resolve host paths
outside its workspace; every other run keeps the contained resolver and
rejects outside paths, and remote (cloud) subagents keep their own
filesystem at every tier.

The configured preferred mode is applied before the first prompt. If the
subagent rejects that mode switch, spawning fails explicitly; the runtime
does not silently continue with a different, potentially more restrictive
mode.

Pipeline `agent:` steps never offer `subagent`; unattended runs cannot
spawn or wait on subagents.
