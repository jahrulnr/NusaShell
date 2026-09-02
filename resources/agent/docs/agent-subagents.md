# Subagents and internal delegates

ACP coding agents are spawn-only and always async. The user never chats
with them in the composer. When the parent agent calls `subagent`, the
tool returns immediately with YAML frontmatter listing the spawned runs
(`runs:` entries with `id` / `status` / `workspace`; `status: starting`)
and the tool call is marked `running` in the conversation. The parent
agent is free to continue other work — it does not block on the
subagent.

## Internal delegate

The internal delegate tool is a local NusaShell background agent. It uses the
same AgentEngine and standard toolbox in a hidden pipeline conversation, but
it is not an ACP subprocess and it cannot spawn another delegate or ACP
subagent. It receives only the self-contained prompt and workspace supplied
by the parent.

The delegate model is configured in Settings → Agent → Internal delegate
model. An empty setting inherits the active model of the parent conversation;
an explicit value uses the provider:model selection from Settings. The
delegate has its own system prompt in
resources/agent/prompts/delegate-agent.md because its role is to execute to
completion, not to behave like the interactive parent or an Automation step.

Delegate runs intentionally use the same ACP-shaped run events and DTOs as
ACP runs. Therefore the Agent dock, run card, drawer, popup, transcript
hydration, recent-run behavior, and scroll-follow behavior are identical for
both families. The backend only differs in how the run is executed.

The delegate result must be the terminal assistant message from the hidden
conversation, after all tool rounds have completed. An intermediate
acknowledgement such as “I will inspect the file” is transcript content only;
it must never become the delegate_result output returned to the parent.

Good example:

    delegate(prompt="Inspect /workspace/app.go, make the requested fix, run the
    focused tests, and report the changed files plus the test result.",
             workspace="/workspace")

Bad example:

    delegate(prompt="Please look at this and tell me what you think.")

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
result returns `plan_path` (the brief mirrored to disk, always current).
NusaShell automatically appends a `Parent plan file (read this first):
<abs path>` block to the spawn prompt when the parent conversation has a
brief — plus a compact Objective + Done when summary when the file lives
outside the subagent workspace or is unreadable. Keep your own prompt
focused on the delegated slice; the plan file carries the shared context.

Good example:

    subagent(prompt="Refactor /home/user/proj/src/api/client.go: extract the
    HTTP client into /home/user/proj/src/api/http.go, keep the existing public
    interface, then run `go build ./...` and `go test ./...` to verify. Report
    changed files and test results.")

Bad example — vague, no paths, no verification:

    subagent(prompt="as discussed, fix the thing in the project")

Bad example — delegating work the parent can do in one tool call:

    subagent(prompt="list the files in /home/user/proj/src")

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
each steering prompt that reaches the ACP session is recorded as another user
bubble before the assistant rounds that follow it. Steering text is persisted
with the terminal run, so it remains visible when the room is reopened after a
backend restart.

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
   completion is queued and injected at the next tool-round boundary —
   the same boundary as steer — then the parent continues. Auto-continue
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
slot lists every active subagent/delegate run (ID + spawning tool + worker
detail), so after a compaction the continuation agent still knows which
background agents were spawned and are pending — and can correlate each
`subagent_result`/`delegate_result` by run ID. Spawn calls and synthetic
result calls are also preserved verbatim through compaction (never stripped)
so the handoff never loses the background-agent picture.

While any subagent is running, the parent agent's auto-continue chain
pauses with reason `awaiting-background-jobs` instead of ending the
turn. When all subagents complete, the chain resumes.

## Waiting for results

`subagent` is always async. The harness injects `subagent_result` when
the run finishes. Call `subagent_wait` only when the next action in
this round cannot proceed without the result; to adjust a live run use
`subagent_steer`, and to cancel it use `subagent_stop`.

When `subagent_wait` reaches a terminal result, it persists the full run
before returning. Its tool result contains only `status`, `id`, `workspace`,
`output_path`, and the last meaningful text turn (or a compact
failure/cancellation fallback). If no text was produced, the last thought may
be used as that bounded fallback. A timeout can return a still-running status
without `output_path`. Read the path only when the full thought/tool
transcript is needed. The Agent drawer receives live transcript events
independently; the tool result never carries the full DTO.

`subagent_steer` and `subagent_stop` acknowledge the current run status.
Their provider-facing results are bounded and omit intermediate transcript
noise.

Good example — wait with a bounded timeout:

    subagent_wait(id="acp_run_123", timeout_ms=120000)

Bad example — polling in a sleep loop:

    sleep(seconds=5)  # repeat until the run finishes
    subagent_wait(id="acp_run_123")  # first call already returned

## Workspace and permissions

`edit_confirmed` auto-allows edit/delete/move only when every path stays
inside the bound workspace; slash-rooted paths (`/etc/passwd`, `\Windows\…`)
are treated as absolute even on Windows and never join onto the workspace.
Existing runs keep the workspace they bound at spawn; new spawns follow the
current conversation workspace unless the tool overrides it.

Stdio framing is newline-delimited JSON-RPC. Do not expect LSP
`Content-Length` headers; the ACP spec rejects them as invalid JSON.

Pipeline `agent:` steps never advertise `subagent` / `subagent_steer` /
`subagent_stop` / `subagent_wait`. Those tools require an interactive
context; unattended FireDue must not wait on them.
