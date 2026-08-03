# Pipelines: Multi-step DAG orchestration

Pipelines chain multiple agent turns or tool calls into a directed acyclic
graph (DAG) with dependencies, conditional branching, and context passing.

## When to use a pipeline vs a job

- **Job** — single action on a schedule or event. Simplest option.
- **Pipeline** — multiple steps that depend on each other, branch on
  conditions, or pass results between steps.

## Creating a pipeline

```
pipeline action=add name="Email triage" trigger={kind:event,pattern:mail.new} steps=[
  { id:classify, name:Classify, action:{type:agent,prompt:"Classify as urgent or normal"}, outputKey:classification },
  { id:notify, name:Notify, dependsOn:[classify], condition:{path:payload.classification,op:eq,value:urgent}, action:{type:tool,pluginId:nusashell.notes,toolName:notes_create,args:{title:URGENT}} },
  { id:archive, name:Archive, dependsOn:[classify], condition:{path:payload.classification,op:eq,value:normal}, action:{type:tool,pluginId:nusashell.notes,toolName:notes_create,args:{title:Archived}} }
]
```

## Step fields

| Field | Required | Description |
|-------|----------|-------------|
| `id` | yes | Unique step ID within the pipeline |
| `name` | yes | Display name |
| `action` | yes | `{ type: "agent", prompt: "..." }` or `{ type: "tool", pluginId, toolName, args }` |
| `dependsOn` | no | Array of step IDs that must complete first |
| `outputKey` | no | Store step output in context under this key |
| `condition` | no | Skip step if condition is false |
| `timeoutMs` | no | Per-step timeout in ms |

## Context passing

A step with `outputKey: "category"` stores its result in the pipeline context.
Downstream agent prompts can reference it via `{{context.category}}`:

```
Step A: outputKey="summary" → context.summary = "Meeting notes..."
Step B: prompt="Translate: {{context.summary}}" → resolved to "Translate: Meeting notes..."
```

## Conditional branching

Conditions evaluate against the event payload (`payload.*`) or accumulated
context (`context.*`):

- `{ path: "payload.urgent", op: "eq", value: "true" }` — skip if not urgent
- `{ path: "context.category", op: "ne", value: "spam" }` — skip if spam
- `{ or: [{ path: "...", op: "eq", value: "..." }, { path: "...", op: "contains", value: "..." }] }` — any match
- `{ not: { path: "...", op: "eq", value: "..." } }` — negate

Supported ops: `eq`, `ne`, `contains`, `regex`.

## Triggers

Same shape as jobs:

- Schedule: `trigger: { kind: "schedule", schedule: "every 1h" }`
- Event: `trigger: { kind: "event", pattern: "mail.new", pluginId: "nusashell.mail" }`

Schedule timezone rules match jobs: cron hour/minute and bare timestamps are
**UTC**. Convert the user's local clock time before writing a cron/ISO string.
Full detail: `docs_read({ path: "jobs-howto.md" })` (Timezone rules section).

## Managing pipelines

Call `pipeline` directly (no pipelines plugin to enable). Actions: `list`,
`add`, `update`, `remove`, `run`. Always `list` before creating a duplicate.
Plugin IDs / tool names / event patterns in examples are illustrative —
confirm real capabilities via `mcp_list` / `tool_list` before wiring them.

- `pipeline action=list` — see all pipelines with status, step count, trigger
- `pipeline action=update id=... name=...` — edit any field
- `pipeline action=run id=...` — fire immediately
- `pipeline action=remove id=...` — delete

## Limitations

- Pipelines run only while NusaShell is open.
- No cross-device sync. Pipelines are local to the machine.
- If a step errors, the pipeline stops (no retry in v1).
- The `pipeline` tool is denied inside scheduled job/pipeline turns (no recursion).
