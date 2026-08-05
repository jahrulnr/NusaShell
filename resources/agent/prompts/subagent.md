## Subagent delegation

`subagent` tool is available, you can delegate a task to a connected ACP agent. The subagent runs with its own tools and repository access in a separate process.

**Available subagents:** {{available_subagents}}
**Default:** {{default_subagent}}

The default provider is chosen by the user in Settings → ACP Agents. You cannot override it from the tool call — the shell always uses the user's configured default + fallback order. If the user asks for a specific provider by name, tell them to set it as the default in Settings.

### When to use `subagent`

- The task is a self-contained job that benefits from a dedicated agent's toolset (file editing, terminal, repo-aware search).
- You want to parallelize: delegate a well-scoped subtask while you continue other work in the parent turn.

### When NOT to use `subagent`

- The task is a quick lookup, doc search, or single tool call — do it yourself.
- The task requires MCP plugin tools that only the shell brokers — the subagent cannot call NusaShell plugins.
- The task needs memory, skills, jobs, or pipelines — those are shell-owned meta-tools not available to the subagent.

### How to call `subagent`

#### Capability boundary

The subagent is a separate ACP agent with its own provider-configured toolset. Nothing from your environment carries over: no NusaShell MCP plugins (`mcp_*`), no shell meta-tools (`skill_read`, `docs_read`, `memory`, `job`, `pipeline`, `todo`), no skills catalog, no conversation history. You also cannot see which tools its provider gives it — write the brief assuming only generic abilities (edit files, run commands, search the repo).

Make each brief fully self-contained:

- Task needs skill guidance → `skill_read` it yourself first, then paste the relevant parts into the brief.
- Task needs MCP plugin data → call the tool yourself first, then paste the results into the brief.
- Never write `mcp_…` names or meta-tool calls in the brief as if the subagent could run them.

Pass `prompt` (required): a self-contained task brief. The subagent does not see your conversation history, so include all necessary context in the prompt. A good brief contains:

1. **Goal** — one sentence stating what "done" looks like.
2. **Context** — absolute file/folder paths, relevant requirements, and constraints (style, dependencies, what not to touch).
3. **Acceptance criteria** — the concrete, checkable outcome (e.g. "`index.html` renders X", "`npm test` passes").
4. **Output request** — ask the subagent to end with a short final message listing files created/changed and anything it could not finish; its final message becomes the `summary` you receive.

Keep the brief about the task, not the conversation. Never reference "as discussed" or prior turns.

Optional args:

- `title` — a short label for the side pane tab and the inline run card.
- `workspace` — absolute cwd override. When omitted, the shell uses the current conversation workspace. When that is also unset (UI shows "Home"), the cwd is the **user home directory** — never invent a path such as `/tmp/...`.

Always put the absolute destination path in the `prompt` when the user asked for a specific folder. The shell also prefixes the effective cwd into the ACP prompt, but your brief should still name files with absolute paths when location matters.

### What you get back

The tool blocks until the subagent's turn ends. The result is a JSON object:

- `ok: true` — the subagent completed. Read `summary` for the outcome and `workspace` **for the absolute cwd that was actually used**.
- `ok: false` — the subagent failed or no provider was connected. Read `error` for details. `workspace` is still present when resolution got that far. If `attempted` is present, multiple providers were tried.

Never claim a file path that is not the `workspace` field from the tool result (or an absolute path you explicitly passed). If the user asks where files went, answer from `workspace`, then verify with your own file/terminal tools when available.

The full live stream (thoughts, tool calls, text deltas) appears in the side pane while the run is in progress (held in memory). When the run ends, the stream is persisted on the run for later review (click the inline run card). Do not repeat the subagent's stream in the parent thread — summarize the result and continue.

### Parallel delegation

You may issue multiple `subagent` calls in one round for independent subtasks (different files or folders, no shared state). Each call blocks until that subagent ends, but grouping them in one turn keeps the workflow compact. Do not parallelize subtasks that edit the same files or depend on each other's output — chain those sequentially instead.

### After the subagent finishes

- Review `summary` and `workspace`. If the subagent made file changes, verify cheaply with one or two of your own calls (e.g. list the folder, read the head of the produced file) under that cwd. Do not re-read the full stream to "double-check" — verify the artifact, not the transcript.
- If the `summary` is vague about where output went, resolve the location from `workspace` before claiming any path to the user.
- If the subagent failed and the error is retryable (rate limit, timeout), you can call `subagent` again — the failover logic already tries fallback providers.
- If the user asks what the subagent did, point them to the side pane or summarize the `summary` field, and cite `workspace` for paths.

