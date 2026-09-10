# Automation YAML contract

Use this reference when adapting a template. The parser accepts workflow
version `1` and rejects unknown YAML fields, malformed nested fields, and
multiple YAML documents. A syntactically valid document can still be `BLOCKED`
by a stopped provider or be only partially supported at runtime. See
`capability-matrix.md`.

## Top level

```yaml
version: 1
name: required workflow name
enabled: false
trust: safe # safe | trusted | privileged
concurrency:
  key: tg-${event.chat_id} # optional ${event.*} template; empty → workflow id
  policy: allow # allow | queue | replace | skip
missed: skip_missed # skip_missed | run_once_after_restart | catch_up_all
defaults:
  shell: sh
  timeout: 10m
env:
  KEY: value
webhook_url: https://example.invalid/hook
notify:
  plugin: nusashell.telegram   # optional; trusted|privileged only
  detail: tools                # none | tools | text | all (default tools)
  chat_id: "${event.chat_id}"  # optional template; unresolved → skip
triggers: []
jobs: {}
```

`notify:` streams agent-step lifecycle (observer events: run/step/tool_call
pre+post via CallID, reasoning/text per round) to a plugin's host-internal
progress tool (`internal_send_progress`, fallback `admin.send_progress`).
Sinks are side-effect only, async best-effort, and never block or fail the
run; unresolved `chat_id` skips with `automation.notify.skipped`. Plugin
tools named `internal_*` or `admin.*` are hidden from all agent tool
listings and cannot be invoked by agents. The step's final output is
delivered in full (the plugin splits long text into multiple messages);
tool/reasoning progress lines stay short previews. When the sink posts a chat
reply, state a character budget in the prompt (e.g. "under 3000 characters")
— models cannot count characters reliably, and the budget keeps the reply to
one message.

`name`, `jobs`, and a valid trigger/step shape are required by syntax
validation. Each trigger item must choose exactly one of `once`, `every`,
`when`, or `manual`, and `manual` must be `true`; combining kinds is rejected.
An omitted or empty `triggers` field is accepted by the parser only for
file-pipeline compatibility. Directory discovery turns that case into a
manual-only trigger, while API-created definitions should declare a trigger
explicitly. `enabled` is useful in pipeline files. When using
`automation(op="create")`, pass `enabled` explicitly because the dispatcher
otherwise defaults a new save to enabled.

Use the lowest `trust` level that permits the work. Credentials are owned by
providers and must not be placed in `env`, `webhook_url`, or prompts.

## Trigger families

```yaml
triggers:
  - once:
      at: "2026-12-31T07:00:00+07:00"
      timezone: Asia/Jakarta
  - every:
      cron: "0 9 * * 1-5"
      timezone: Asia/Jakarta
  - every:
      interval: 24h
  - when:
      event: telegram.message
      where: {chat_type: dm}
      debounce: 2s
  - manual: true
```

`every` chooses exactly one of `cron` or `interval`. `once.at` is parsed as a
future time string. `when` matches an event publisher's normalized type and
attributes. `where` supports equality and keys ending in `_contains` for
case-insensitive substring matching. `manual` must be explicitly `true`. An
event with no stable identity is less safe to replay, so the publisher should
supply one.

Multiple matching triggers can create multiple deliveries because trigger IDs
are part of the deduplication key. Do not add overlapping triggers casually.

## Jobs and steps

```yaml
jobs:
  build:
    name: Optional display name
    needs: [prepare]
    if: 'prepare.run_status == "success"'
    runs_on: [local]
    env: {MODE: production}
    timeout: 30m
    continue_on_error: false
    retry:
      max_attempts: 2
      on: [runner_error, timeout]
    steps:
      - name: Shell command
        run: echo hello
        shell: sh
        env: {FOO: bar}
        timeout: 5m
      - name: Builtin or registered capability
        uses: filesystem.read
        with: {path: ./README.md}
      - name: Park until a future time
        wait_until: "2026-12-31T08:00:00+07:00"
      - name: Full NusaShell agent turn
        agent:
          prompt: Do the focused task.
          model: provider:model
          output_schema: {type: object}
```

Each step should choose exactly one of `run`, `uses`, `wait_until`, or `agent`.
`needs` is a DAG dependency. Steps inside one job remain sequential;
independent jobs may run in parallel. `wait_until` parks the run and releases
the executor. `agent` runs a headless NusaShell turn.

The parser accepts `runs_on`, artifacts, cache, and retry fields, but the
current runtime has important limits:

- `runs_on` does not by itself provide a remote runner. Local execution is the
  reliable default.
- `retry` describes job policy, but the current executor does not yet loop job
  attempts automatically. Do not use it as proof that an external write is
  retried safely.
- `output_schema` is validated against the final assistant content by the
  headless runner. The current result is still a text output map with
  `output`; schema properties are not transported as separate job outputs.
- shell stdout is logged but is not automatically converted into structured
  job outputs. A later `if` can inspect outputs only when the step/capability
  actually returns them.

## Conditions

`if` is deliberately small. It supports empty/boolean expressions, `==`,
`!=`, `&&`, `||`, dotted event paths, job status/output paths, and
`event.subject_contains("text")`. It does not support loops, arbitrary
functions, numeric operators, `${{ }}`, or shell substitution.

Useful paths include:

- `job_id` or `job_id.run_status` for the job run status;
- `job_id.exit_code` for its exit code;
- `job_id.status` for a returned `status` output or the run status;
- `job_id.<output>` for a returned job output;
- `event.<attribute>` for event attributes.

## Concurrency and missed schedules

`allow` permits overlaps. `skip` drops a new run while the lock is active for
the same rendered key. `replace` cancels that active run before starting the
new one. `queue` waits FIFO (process-local, cap 10 per key) until the active
run finishes; overflow skips the new run. `concurrency.key` may be static or
an `${event.*}` template (empty → workflow id). Distinct rendered keys never
block each other.

`missed` controls scheduled work encountered after downtime. Choose
`skip_missed`, `run_once_after_restart`, or `catch_up_all` only after deciding
whether duplicate work is safe. Do not use a frequent interval as an event
poller.

## Event-driven prompts

Agent prompts can use `${event.<key>}`. Rendering resolves standard fields and
publisher attributes. Missing values become empty strings. This is prompt
rendering, not shell expansion, and there is no generic `${jobs.*}` or
`${steps.*}` interpolation.

Generic MCP event publishers use `notifications/nusashell/event`, not
`notifications/message`, with required `schema_version: 1`, `event_id`, and
`type` fields. Optional fields are `occurred_at` (RFC3339), `subject`,
`attributes` (object), and `data` (any valid JSON value). The host assigns the
source from the connected MCP server and namespaces the event ID for
at-least-once delivery deduplication. Unknown top-level fields, unsupported
versions, malformed values, and oversized strings/payloads are rejected.

For example, a GitHub publisher can emit:

```text
notifications/nusashell/event {
  schema_version: 1,
  event_id: "github-delivery-123",
  type: "github.pull_request",
  subject: "owner/repo#42",
  attributes: {action: "opened", repository: "owner/repo"},
  data: {pull_request_number: 42, head_sha: "abc123"}
}
```

The installed Telegram bridge publishes `telegram.message` through the
generic envelope (host-assigned source `plugin:nusashell.telegram`) with
`chat_id`, `message_id`, `chat_type`, `sender_id`, `sender_username`,
`sender_name`, `text` (bounded to 200 characters), and `from_me` attributes.
Bot-originated and duplicate updates are never emitted, so `from_me` is
always `false` on published events; read the full message through the
plugin's read tools when `text` may be truncated. New publishers must use
the generic envelope even when their event happens to contain chat-like
fields.
For GitHub, trading, or kanban, use the exact event type and attributes
published by the installed provider. Event admission requires a nonempty type
and durable event storage before matching; a template cannot create an event
publisher by itself.

The scheduler deduplicates by normalized event ID, trigger ID, and workflow ID.
Remote sends remain non-transactional, and Telegram Bot API send methods do not
expose a generic idempotency key, so verify the target before a bounded retry.

The scheduler persists run and activation state before lifecycle notifications; a
schedule, wait, debounce, lock, or run-store write failure is returned to the
caller instead of being treated as success. When a built-in event store has
claimed a delivery but run creation fails, the claim is rolled back so a later
replay can try again. Disabling a workflow retires its pending timer records
before a later re-enable. This still does not make remote side effects
transactional.


Artifact and cache models/ports exist, but current storage/executor wiring is
incomplete. Never make correctness depend on `artifacts` or `cache` in a
new template. If a later job needs data, use a known shared workspace file and
test that workspace topology, or keep the producer and consumer in one job.

There is no first-class `approval`, `signal`, loop, saga, or compensation step.
Use a plugin's explicit approval tool or split the mutation into a manually
started workflow. Treat AI output as a draft until a human approves it.
