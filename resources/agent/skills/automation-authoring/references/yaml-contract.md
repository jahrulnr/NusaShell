# Automation YAML contract

Use this reference when adapting a template. The parser accepts workflow
version `1`; unknown fields are ignored by the YAML decoder, so keep examples
to the fields below.

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
validation. `enabled` is useful in pipeline files; when using
`automation(op="create")`, pass the `enabled` argument explicitly because the
dispatcher defaults new saves to enabled.

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

`every` chooses exactly one of `cron` or `interval`. `once.at` is RFC3339.
`when` filters compare attributes; keys ending in `_contains` perform a
case-insensitive substring match. A trigger can also set `auto_start` when a
capability policy requires it.

## Jobs and steps

```yaml
jobs:
  build:
    name: Optional display name
    needs: [prepare]
    if: 'event.subject_contains("urgent")'
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
      - name: Builtin or MCP capability
        uses: filesystem.read
        with: {path: ./README.md}
      - name: Park until a future time
        wait_until: "2026-12-31T08:00:00+07:00"
      - name: Full NusaShell agent turn
        agent:
          prompt: Do the focused task.
          model: provider:model
          output_schema: {type: object}
    artifacts:
      paths: [dist/]
      retention: 7d
    cache:
      namespace: build
      paths: [.cache/]
      key: [go.sum]
```

Each step chooses exactly one of `run`, `uses`, `wait_until`, or `agent`.
`needs` is a DAG dependency; steps in a job remain sequential. `wait_until`
parks the run and releases the executor, while an `agent` step runs the normal
headless toolbox and can render `${event.<key>}` in its prompt.
