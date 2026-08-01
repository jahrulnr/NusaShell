# Jobs: Agent vs Tool mode

NusaShell scheduled jobs have two modes. Pick the right one for the task.

## Agent mode

- **What it does:** fires a headless AI agent turn with a prompt. The agent
  can call MCP plugin tools, read docs, and plan multi-step work.
- **When to use:** judgment tasks — "summarize unread mail", "review PRs and
  post notes", "check calendar and create a reminder".
- **Costs tokens:** yes. Each run is a full LLM turn.
- **Required field:** `prompt` (min 1 char, max 10000).
- **Optional fields:** `providerId`, `model`, `effort` — override the shell's
  active AI settings. Leave blank to use the default (or, when created via the
  foreground agent `job` tool, inherit the caller turn's model).

## Tool mode

- **What it does:** calls one plugin tool directly with fixed args. No AI
  model is involved — it is a scheduled RPC.
- **When to use:** deterministic cron — "every hour call `mail_sync`",
  "daily call `cleanup_temp`".
- **Costs tokens:** no. There is no LLM in the loop.
- **Required fields:** `pluginId`, `toolName`, `args` (JSON object).
- **No prompt field:** tool mode has no `prompt`. The desktop form hides the
  prompt textarea and shows a schema-driven arg form instead.

## Creating and editing

- **Desktop:** Jobs view → + New job, or Edit on a row. The form swaps fields
  based on mode. Agent shows prompt + model picker; tool shows plugin/tool
  dropdowns + schema arg form.
- **Agent tool:** `job` action `add` or `update` with `mode` set to
  `"agent"` or `"tool"`.
- **API:** `job.add` / `job.update` WS methods. Agent mode `mode` object:
  `{ type: "agent", prompt: "...", providerId?: "...", model?: "...", effort?: "..." }`.
  Tool mode: `{ type: "tool", pluginId: "...", toolName: "...", args: {...} }`.

## Schedule grammar

`every 30m` / `2h` / `1d` / `0 9 * * *` (5-field cron, UTC) / ISO timestamp
for one-shot. Jobs run only while NusaShell is open. A one-shot missed while
the app was closed is marked errored, not silently fired.

## Monitoring

- `job.list` returns `running` and `activeTraceId` for live runs.
- `job.cancel` aborts an in-flight run.
- Events: `job.started`, `job.completed`, `job.failed`, `job.cancelled`.
- `job.output` with `includeBody: true` returns the full markdown body.
