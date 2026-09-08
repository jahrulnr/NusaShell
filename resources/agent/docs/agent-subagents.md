# Subagents

Subagents are spawn-only and always async. The user never chats
with them in the composer. When the parent agent calls `subagent`, the
tool returns immediately with YAML frontmatter listing the spawned runs
(`runs:` entries with `id` / `status` / `workspace`; `status: starting`)
and the tool call is marked `running` in the conversation. The parent
agent is free to continue other work — it does not block on the
subagent.

## Internal delegate target

`subagent(agent_id="internal")` runs the task on a local NusaShell background
agent. It uses the same AgentEngine and standard toolbox in a hidden pipeline
conversation, but it is not an ACP subprocess and it cannot spawn another
subagent. It receives only the self-contained prompt and workspace supplied
by the parent.

The delegate model is configured in Settings → Agent → Internal delegate
model. An empty setting inherits the active model of the parent conversation;
an explicit value uses the provider:model selection from Settings. The
delegate has its own system prompt in
resources/agent/prompts/delegate-agent.md because its role is to execute to
completion, not to behave like the interactive parent or an Automation step.

Internal delegate runs use the same ACP-shaped run events and DTOs as ACP
runs. Therefore the Agent dock, run card, drawer, popup, transcript
hydration, recent-run behavior, and scroll-follow behavior are identical for
both targets. The backend only differs in how the run is executed, and the
`subagent` ops `steer` / `stop` / `wait` work on both.

The delegate result must be the terminal assistant message from the hidden
conversation, after all tool rounds have completed. An intermediate
acknowledgement such as “I will inspect the file” is transcript content only;
it must never become the subagent_result output returned to the parent.

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

A dock appears above the composer: chips for every live run (and recent
finishes in this room), newest first — live delta updates never
reshuffle chips under the cursor. Click a chip for the right-hand drawer
(all parallel spawns), or peek one run in a popup. Both surfaces stream
the transcript live, patched in place like the conversation thread
(auto-follows the bottom while you're at the bottom; your scroll
position is never reset by incoming updates). Completed runs stay
reopenable from their delegation card or the drawer for as long as the
conversation exists — the drawer lists every run of the room, settled
included. The user is an observer: steer, stop, mode change, and risk
promotion are handled by the orchestrator (parent agent), not the user.
Permissions are auto-allowed — the orchestrator delegates authority
when it spawns a subagent.

The drawer and peek transcript use the same Agent conversation structure as
the parent room. The initial delegation brief is shown as a user bubble, and
each steering prompt that reaches the ACP session (after an interrupt cancel)
is recorded as another user bubble before the assistant rounds that follow it.
Steering text is persisted with the terminal run, so it remains visible when
the room is reopened after a backend restart.

## Async completion (tool injection)

When a subagent finishes (completed, failed, or cancelled):

1. The full transcript is persisted to
   `conversations/<conversation_id>.acp/<run_id>.json` under the data
   directory — one JSON file per run, linked to the parent conversation.
   Writes are atomic and per-run, so parallel spawns finishing at the
   same time never contend on a shared file.
2. The original `subagent` tool call is updated from `running` to
   `ok`/`fail` with a brief terminal status (the full output travels in
   the synthetic `subagent_result` tool call below — old history is
   never silently rewritten).
3. A synthetic `subagent_result` tool call carrying the full result
   (YAML frontmatter with `status` / `id` / `workspace` / `output_path`
   plus the subagent's last-turn text summary) is injected into the
   conversation, announcement-style. It is persisted as a normal
   assistant tool call so the model sees it in fresh context and in
   later turns (auto-continue) too. If the parent is still in a turn,
   completion is queued and injected at the next tool-round boundary,
   before a queued user steer. The steer is appended last so the next
   provider request ends with the user's newest instruction. Auto-continue
   stays paused (`awaiting-background-jobs`) until that injection lands.
4. If the parent is idle, a new parent-agent turn is triggered so the
   parent processes the `subagent_result` without a user message. The
   parent sees that tool call in history and acts on the summary as if
   it had just completed the tool.

Runtime state (delegation config, continuation instructions, subagent
results) is never appended to the system prompt — it travels as tool
descriptions or tool hydration so the system prefix keeps its
prompt-cache hits.

Background/async runs are hydration-aware: the `runtime_context` hydration
slot lists every active subagent run (ID + spawning tool + worker
detail), so after a compaction the continuation agent still knows which
background agents were spawned and are pending — and can correlate each
`subagent_result` by run ID. Spawn calls and synthetic
result calls are also preserved verbatim through compaction (never stripped)
so the handoff never loses the background-agent picture.

While any subagent is running, the parent agent's auto-continue chain
pauses with reason `awaiting-background-jobs` instead of ending the
turn. When all subagents complete, the chain resumes.

## Steering priority and timing

A steer from the main composer is a real user message, not background runtime
state. It is queued while the current provider or tool round is in flight and
applied at the next safe boundary. `subagent(op="wait")` is intentionally blocking,
so a composer steer can wait for that call to return; this delay does not mean
the message was dropped.

At a boundary, the parent applies finished background results and harness
announcements first, then appends the queued composer steer, then starts one
fresh assistant round. This ordering keeps the steer as the newest user
instruction. After a steer is applied, re-evaluate the user's request before
resuming the older plan. Do not call `subagent(op="wait")` again merely
because the previous plan was waiting if the steer changes the requested
action.

`subagent(op="steer")` is different: it redirects a **child run** by
cancelling the in-flight work and sending the new text as the next prompt.
On an ACP session this is interrupt-and-replace (`session/cancel` then
`session/prompt`); on an internal delegate the text is queued and applied at
the next tool-round boundary. Prefer the `steer` op when the parent needs
to change direction mid-work; use the `stop` op only to abandon the run.

Good example, steer a child run when the user changes its direction:

    subagent(op="steer", id="acp_run_123", text="Stop the framework comparison and inspect the existing desktop pet code instead.")

Bad example, continue the stale parent plan after a user steer:

    subagent(op="wait", id="acp_run_123", timeout_ms=120000)
    subagent(op="wait", id="acp_run_456", timeout_ms=120000)

The bad sequence ignores the newly requested direction. First process the
latest user steer, then wait only if that revised plan still needs a result.

## Waiting for results

`subagent` is always async. The harness injects `subagent_result` when
the run finishes. Call `subagent(op="wait")` only when the next action in
this round cannot proceed without the result; to adjust a live run use the
`steer` op, and to cancel it use the `stop` op.

When the `wait` op reaches a terminal result, it persists the full run
before returning. Its tool result contains only `status`, `id`, `workspace`,
`output_path`, and the last meaningful text turn (or a compact
failure/cancellation fallback). If no text was produced, the last thought may
be used as that bounded fallback. A timeout can return a still-running status
without `output_path`. Read the path only when the full thought/tool
transcript is needed. The Agent drawer receives live transcript events
independently; the tool result never carries the full DTO.

The `steer` and `stop` ops persist compact tool results, not the
full run DTO. Steer returns `status` / `id` / `workspace` plus
`Steer accepted.` and does not include last-turn text. Stop returns the
same bounded completion shape as `wait` (last meaningful turn,
plus `output_path` when the cancelled run is persisted). The Agent drawer
still receives live transcript events independently; read `output_path`
only when the full thought/tool transcript is needed.

Good example — wait with a bounded timeout:

    subagent(op="wait", id="acp_run_123", timeout_ms=120000)

Bad example — polling in a sleep loop:

    sleep(seconds=5)  # repeat until the run finishes
    subagent(op="wait", id="acp_run_123")  # first call already returned

## Workspace and permissions

`edit_confirmed` auto-allows edit/delete/move only when every path stays
inside the bound workspace; slash-rooted paths (`/etc/passwd`, `\Windows\…`)
are treated as absolute even on Windows and never join onto the workspace.
Local stdio workspaces and tool paths are checked after resolving symlink
aliases, so a bind through `/home/...` and a target under `/media/...` can
refer to the same physical workspace. A symlink that escapes the workspace
is rejected. Existing runs keep the canonical workspace they bound at spawn;
new spawns follow the current conversation workspace unless the tool
overrides it.

The configured preferred ACP mode is applied before the first prompt. If the
ACP agent rejects that mode switch, spawning fails explicitly; the runtime
does not silently continue with a different, potentially more restrictive
mode.

Stdio framing is newline-delimited JSON-RPC. Do not expect LSP
`Content-Length` headers; the ACP spec rejects them as invalid JSON.

Pipeline `agent:` steps never advertise `subagent`. The tool requires an
interactive context; unattended FireDue must not wait on it.
