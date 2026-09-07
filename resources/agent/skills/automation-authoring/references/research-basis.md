# Research basis and adopted patterns

Reviewed 2026-09-08. The links below are external design references, not
NusaShell runtime contracts. The local contract remains `yaml-contract.md` and
the capability matrix.

## Sources

| Source | What it contributes | NusaShell adoption |
| --- | --- | --- |
| [GitHub Actions workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax) | event filters, scheduled workflows, job outputs, permissions, artifacts | precise trigger filters, explicit least privilege, DAG and output caveats |
| [GitHub concurrency](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency) | one active/pending run, cancel/replacement, group-key scope | explicit concurrency policy and resource-scoped keys; queue caveat documented |
| [GitHub secure use](https://docs.github.com/en/actions/reference/security/secure-use) | least privilege, secret handling, shell-injection risk, pinned actions, OIDC | no secrets in YAML/prompts, untrusted values never become shell code, verify capabilities |
| [Copilot PR lifecycle](https://docs.github.com/en/copilot/tutorials/use-copilot-code-review-across-the-pull-request-lifecycle) | early review, re-review after pushes, repo instructions, human review | analysis-first PR template and human gate before public review mutation |
| [GitHub webhook events](https://docs.github.com/en/webhooks/webhook-events-and-payloads) | event-specific payloads, globally unique delivery ID, HMAC header, payload limits | stable event identity, exact event type, publisher verification |
| [GitHub pull request reviews API](https://docs.github.com/en/rest/pulls/reviews) | review permissions, review event choices, diff position, rate limits | never guess review tool; default draft/report; write only after explicit authorization |
| [Telegram Bot API](https://core.telegram.org/bots/api) | update IDs, webhook retry behavior, polling/webhook exclusivity, bounded delivery | exact message identity, no unread polling, observe successful send result |
| [Trello Automation overview](https://support.atlassian.com/trello/docs/automation-overview) and [create/manage](https://support.atlassian.com/trello/docs/create-and-manage-automations) | trigger/action/variables, buttons, scheduled runs, logs, test/run-now, branching | trigger → actions model, manual buttons, dry/test run and log-first operation |
| [Jira automation triggers](https://support.atlassian.com/cloud-automation/docs/jira-automation-triggers) and [conditions](https://support.atlassian.com/cloud-automation/docs/jira-automation-conditions) | event/schedule/manual triggers, conditions and branches, failure stops actions | condition-first design and explicit failure semantics |
| [systemd.timer](https://www.freedesktop.org/software/systemd/man/systemd.timer.html) | calendar/monotonic timers, accuracy, jitter, persistence after missed time | timezone, missed-run policy, bounded schedule behavior; no invented jitter field |
| [n8n flow logic](https://docs.n8n.io/build/flow-logic) and [error handling](https://docs.n8n.io/build/flow-logic/handle-errors-gracefully) | split/merge/loop/wait/sub-workflow, error workflow and execution logs | fan-out/fan-in, wait, dedicated diagnosis and observability |
| [Temporal recover without restart](https://docs.temporal.io/guides/recover-without-restart) | durable state, human correction, resume failed step, transient vs permanent error, compensation | recovery vocabulary and runbook; first-class signals/compensation marked unsupported |

## Daily automation checklist distilled from the research

For a daily or scheduled workflow, decide all of these before writing YAML:

1. **Clock**: exact timezone, cron/interval semantics, and what happens after downtime.
2. **Load**: whether concurrent runs are safe; key the lock to the real resource.
3. **Input**: schema, maximum size, truncation, and whether it is trusted.
4. **Work**: deterministic collection before interpretation; independent checks in
   parallel; summary after `needs`.
5. **Effect**: report, draft, or mutation; target identity; idempotency key.
6. **Failure**: stop, continue for diagnostics, retry transport, or ask a human.
7. **Observability**: run ID, job/step logs, webhook summary, and a diagnosis path.
8. **Security**: minimum trust, credentials outside YAML, no shell interpolation of
   event text, and exact MCP schema/ref discovery.
9. **Test**: validate syntax, run once with harmless input, inspect status/logs,
   then enable. A test run is not proof of remote delivery unless its tool result
   says the operation succeeded.

The common theme is deliberate state transition. A trigger should create a
bounded run, not an unbounded conversational loop.
