# ACP subagents

ACP coding agents are spawn-only and always async. The user never chats
with them in the composer. When the parent agent calls `subagent`, the
tool returns immediately with YAML frontmatter listing the spawned runs
(`runs:` entries with `id` / `status` / `workspace`; `status: starting`)
and the tool call is marked `running` in the conversation. The parent
agent is free to continue other work — it does not block on the
subagent.

## Delegation brief

Give a compact self-contained brief: goal, relevant absolute paths,
necessary constraints, and the expected artifact. Do quick lookups and
shell-owned work yourself — delegate only what genuinely benefits from an
independent agent.

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
finishes in this room), in spawn order — live delta updates never
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
   later turns (auto-continue) too.
4. A new parent-agent turn is triggered (tool injection) so the parent
   processes the result without a user message. The parent agent sees
   the `subagent_result` tool call in its message history and acts on
   the summary as if it had just called the tool.

Runtime state (delegation config, continuation instructions, subagent
results) is never appended to the system prompt — it travels as tool
descriptions or tool hydration so the system prefix keeps its
prompt-cache hits.

While any subagent is running, the parent agent's auto-continue chain
pauses with reason `awaiting-background-jobs` instead of ending the
turn. When all subagents complete, the chain resumes.

## Waiting for results

`subagent` is always async. When you need the result before continuing,
call `subagent_wait` with the run id; to adjust a live run use
`subagent_steer`, and to cancel it use `subagent_stop`. All three return
the full run DTO (status, workspace, transcript) to the frontend
transcript drawer, but the model only receives a short text summary: run
id, status, the LAST text chunk (the agent's final message, truncated to
2000 chars), or the last reasoning chunk if no text was produced, or the
error/stop reason + last tool as fallback. Intermediate progress,
thought, tool, plan, status, and usage chunks are stripped — the full
transcript stays in the persisted JSON (`output_path`) for reference via
`file_read`.

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
