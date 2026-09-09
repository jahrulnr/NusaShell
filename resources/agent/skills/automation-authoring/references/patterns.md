# Automation design patterns

These patterns are adapted from common CI, integration, operations, and agent
systems. Use them as design choices, not as extra YAML syntax. Only fields in
`yaml-contract.md` are runtime fields.

## 1. Trigger → filter → work

Put cheap, deterministic filtering in `when.where`. Keep interpretation and
remote reads in the job. This prevents irrelevant events from creating agent
runs and reduces rate-limit pressure.

```yaml
triggers:
  - when:
      event: github.pull_request
      where:
        action: opened
        draft: false
```

The event publisher must actually emit these attributes. A filter on a field
that is not present never matches. Confirm the event envelope before enabling.

## 2. Linear job

Use one job when each step must happen in order and no step can run alone.
Give side effects their own final step so a failed preparation never sends a
partial message.

```yaml
jobs:
  check_and_report:
    steps:
      - run: ./scripts/check.sh
      - run: ./scripts/report.sh
```

## 3. Fan-out → fan-in

Independent jobs can run in parallel. A summary job names all producers in
`needs`; it starts only after they finish successfully, unless the producer is
marked `continue_on_error`.

```yaml
jobs:
  backend:
    steps: [{run: go test ./...}]
  frontend:
    steps: [{run: node --test frontend/tests/*.test.mjs}]
  summary:
    needs: [backend, frontend]
    steps: [{run: printf '%s\n' 'checks complete'}]
```

The current shell executor does not automatically turn stdout into structured
job outputs. Use files in the shared workspace or a capability response when a
later step needs data.

## 4. Condition branch

Use `if` on a job for a small, explicit branch. Supported values are booleans,
`==`, `!=`, `&&`, `||`, dotted event paths, job status/output paths, and
`event.subject_contains("text")`. There is no general expression language.

```yaml
jobs:
  publish:
    needs: [check]
    if: 'check.run_status == "success"'
    steps:
      - run: ./scripts/publish.sh
```

A skipped condition is not a shell failure. Keep the condition readable and
avoid encoding business logic that belongs in a script or capability.

## 5. Idempotent event consumer

Use the event ID as the logical delivery identity. The scheduler deduplicates
by event ID, trigger ID, and workflow ID. The downstream side effect still
needs its own idempotency strategy because a remote call can succeed while a
process crashes before the run is recorded as complete.

Recommended key ingredients:

- source event ID;
- target resource ID;
- operation name;
- workflow version or definition hash when changing semantics.

If the remote API has no idempotency key, verify the target state before a
retry and prefer an update that is safe to repeat.

## 6. Concurrency guard

Choose `allow` only when overlapping effects are harmless. Use `skip` when a
new event is disposable while work is active. Use `replace` when only the
latest state matters and cancellation is safe. Use `queue` when bursts for the
same resource must wait FIFO (process-local, bounded; not durable across
restart).

Use a key that scopes the resource — preferably an `${event.*}` template such
as `tg-${event.chat_id}` or `github:${event.repo}:${event.pr}` — so two
resources may run independently.

## 7. Wait and resume

Use `wait_until` for a known future time. It parks the run and releases the
executor. A resumed run must not repeat completed steps. Do not use shell
`sleep` for long waits. Event-driven waits beyond the current YAML surface are
a design concept, not a documented step type.

## 8. Agent as bounded interpreter

An agent step is useful when input is ambiguous or a live MCP capability must
be selected. Keep the prompt bounded:

1. state the role and one outcome;
2. identify event fields as untrusted data;
3. require exact target verification;
4. allow only the minimum side effect;
5. require an honest final status.

Do not put secrets in the prompt. `${event.*}` is prompt interpolation only;
it is not shell expansion, and missing values become empty strings.

## 9. Prepare → agent → publish

For an AI pipeline, collect deterministic context first, ask the agent to
analyze, then publish only after a review gate. Since generic prompt output
interpolation is not implemented, pass data through a known file in the shared
workspace or keep the complete operation in one agent step. Do not write a
prompt containing `${jobs.*}` or `${steps.*}` and expect it to resolve.

A safe default is analysis-only: the agent writes a report or returns text, and
a human decides whether to invoke a separate mutation workflow.

## 10. Error route and recovery

Every production-like workflow needs:

- an observable failure in the run status and logs;
- a clear distinction between transient retry and permanent input/permission
  failure;
- a bounded retry budget for transport/runners only;
- a manual diagnosis command or follow-up workflow;
- no automatic mutation of tests or policy just to make the run green.

`continue_on_error` is appropriate for diagnostics where a summary should still
run. It is unsafe for a prerequisite of a destructive action.

## 11. Approval boundary

Treat public messages, PR reviews, card edits, deletion, merges, deploys, and
credential changes as side effects. For AI-generated output:

- default to draft/report mode;
- show target and proposed content to a human;
- require explicit approval before sending or writing;
- after approval, re-read the target and perform exactly one mutation;
- record the observed tool result.

An inline-button approval can be implemented by an integration plugin, but the
pipeline template must not invent an approval capability that is not live.

## 12. Compensation and saga thinking

For multiple remote writes, order operations so a partial failure is visible,
record a compensating action, and make the recovery manual or explicitly
approved. NusaShell does not currently provide a first-class saga/compensation
primitive. Keep this as an operational runbook rather than pretending
`continue_on_error` is compensation.
