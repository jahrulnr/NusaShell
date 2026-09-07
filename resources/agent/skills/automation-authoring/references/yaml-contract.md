# Automation YAML contract

Use this reference when adapting a template. The parser accepts workflow
version `1`; unknown YAML fields are ignored by the decoder, so keep documents
to this contract. A syntactically valid document can still be `BLOCKED` by a
stopped provider or be only partially supported at runtime. See
`capability-matrix.md`.

## Top level

```yaml
version: 1
name: required workflow name
enabled: false
trust: safe # safe | trusted | privileged
concurrency:
  key: optional lock key
  policy: allow # allow | queue | replace | skip
missed: skip_missed # skip_missed | run_once_after_restart | catch_up_all
defaults:
  shell: sh
  timeout: 10m
env:
  KEY: value
webhook_url: https://example.invalid/hook
triggers: []
jobs: {}
```

`name`, `jobs`, and a valid trigger/step shape are required by syntax
validation. `enabled` is useful in pipeline files. When using
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
case-insensitive substring matching. An event with no stable identity is less
safe to replay, so the publisher should supply one.

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
- `output_schema` is accepted but is not validated by the headless runner. The
  current result is a text output map.
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

`allow` permits overlaps. `skip` drops a new run while the lock is active.
`replace` cancels the active run before starting the new one. `queue` currently
keeps the existing lock and does not create a durable waiting backlog, so use it
only when that behavior is acceptable. A static key scopes the whole workflow;
the current YAML contract does not interpolate event fields into lock keys.

`missed` controls scheduled work encountered after downtime. Choose
`skip_missed`, `run_once_after_restart`, or `catch_up_all` only after deciding
whether duplicate work is safe. Do not use a frequent interval as an event
poller.

## Event-driven prompts

Agent prompts can use `${event.<key>}`. Rendering resolves standard fields and
publisher attributes. Missing values become empty strings. This is prompt
rendering, not shell expansion, and there is no generic `${jobs.*}` or
`${steps.*}` interpolation.

For Telegram, use `telegram.message` and preserve `chat_id` plus `message_id`.
For GitHub or kanban, use the exact event type and attributes documented by the
installed publisher. A template cannot create an event publisher by itself.

## Partial features and safe handoff

Artifact and cache models/ports exist, but current storage/executor wiring is
incomplete. Never make correctness depend on `artifacts` or `cache` in a
new template. If a later job needs data, use a known shared workspace file and
test that workspace topology, or keep the producer and consumer in one job.

There is no first-class `approval`, `signal`, loop, saga, or compensation step.
Use a plugin's explicit approval tool or split the mutation into a manually
started workflow. Treat AI output as a draft until a human approves it.
