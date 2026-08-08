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
- **Agent tool:** call `job` directly (no jobs plugin to enable). Actions:
  `list`, `validate_schedule`, `add`, `update`, `set_enabled`, `run`,
  `cancel`, `remove`, `output`. Always `list` before creating a duplicate.
  Call `validate_schedule` before `add` / schedule-changing `update`.
  Set `mode` to `"agent"` or `"tool"`. Plugin IDs / tool names / event
  patterns in examples are illustrative — confirm real capabilities via
  `mcp_list` / `tool_list` before wiring them.
- **API:** `job.add` / `job.update` WS methods. Agent mode `mode` object:
  `{ type: "agent", prompt: "...", providerId?: "...", model?: "...", effort?: "..." }`.
  Tool mode: `{ type: "tool", pluginId: "...", toolName: "...", args: {...} }`.
- **Denial:** the `job` tool is denied inside scheduled job turns (no recursion).

## Schedule grammar

| Input | Meaning |
| --- | --- |
| `every 30m` / `2h` / `1d` | Relative interval from now — timezone does not matter |
| `0 9 * * *` | 5-field cron — hour/minute use the host machine's local clock |
| `2025-12-01T09:00:00Z` | One-shot at that instant (UTC) |
| `2025-12-01 09:00` (no offset) | One-shot at 09:00 on the host machine's local clock |

Jobs run only while NusaShell is open. A one-shot missed while the app was
closed is marked errored, not silently fired.

### Timezone rules (important)

Cron and bare timestamps use the **host machine's local timezone**. The Jobs UI
and scheduler therefore agree on `0 9 * * *` meaning 09:00 on the machine
running NusaShell. Intervals (`every 30m`) are timezone-independent.

For a one-shot that must preserve an external timezone or a specific instant,
use an explicit `Z` or numeric offset such as `2025-12-01T09:00:00+07:00`.
Confirm the machine-local time when the user is scheduling across locations.

## Monitoring

- `job.list` returns `running` and `activeTraceId` for live runs.
- `job.cancel` aborts an in-flight run.
- Events: `job.started`, `job.completed`, `job.failed`, `job.cancelled`.
- `job.output` with `includeBody: true` returns the full markdown body.

## Event triggers

Jobs can fire on events instead of schedules. Use `trigger` instead of `schedule`:

```json
{
  "action": "add",
  "name": "Auto-reply to new mail",
  "trigger": { "kind": "event", "pattern": "mail.new", "pluginId": "nusashell.mail" },
  "mode": "agent",
  "prompt": "Draft a reply to the new email."
}
```

- `pattern` is a glob (`*` matches any segment, e.g. `mail.*` matches `mail.new` and `mail.sent`).
- `pluginId` optionally scopes to one plugin's events.
- `conditions` adds AND-conditions on the event payload: `[{ path: "payload.urgent", op: "eq", value: "true" }]`.
- `throttleMs` and `maxFiresPerHour` prevent runaway loops.

## Soft chains (on_complete)

A job can emit an automation event when it completes successfully. Another job
with a matching event trigger will fire — forming a chain without a full DAG.

```json
{
  "action": "add",
  "name": "Classify email",
  "trigger": { "kind": "event", "pattern": "mail.new" },
  "mode": "agent",
  "prompt": "Classify this email. Reply with 'urgent' or 'normal'.",
  "on_complete": { "type": "mail.classified", "payload": { "source": "auto" } }
}
```

Another job with `trigger: { kind: "event", pattern: "mail.classified" }` will
fire when this one completes. Cycle guards prevent infinite loops
(self-triggers and mutual cycles are detected and blocked).

For multi-step workflows with branching and context passing, use `pipeline`
instead of chaining jobs — see the Pipelines doc.
