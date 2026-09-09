# Automation complexity levels

Use the smallest level that meets the outcome. A higher level is not automatically
better: it increases failure modes, operational cost, and the number of effects
that must be reviewed.

## Level map

| Level | Shape | Use it for | Default safety posture |
| --- | --- | --- | --- |
| **Simple** | one trigger, one job, one to three sequential `run` steps | alarm, reminder, local health check, deterministic maintenance | `safe`, disabled until reviewed |
| **Medium** | one trigger, two to six jobs, explicit `needs`, conditions, timeouts, logs | CI checks, daily ops collection, report generation, scheduled triage | `trusted` only when the job needs broader access |
| **Advanced** | event or schedule, fan-out/fan-in, `agent`/MCP, side effects, concurrency and recovery plan | PR review, Telegram assistant, kanban triage, AI briefing, approval workflow | `trusted` or `privileged` only with a written reason and human gate |

The level describes workflow design, not whether an agent is present. A small
AI step can still be simple. A non-AI pipeline with many dependencies is
medium or advanced.

## Classify by dimensions

Raise the level when any answer is true:

1. **Trigger**: Does it consume an external event, or have more than one trigger?
2. **Work graph**: Are there parallel jobs, a fan-in summary, or conditional branches?
3. **State**: Does work continue after a wait, restart, or human decision?
4. **Effect**: Does it send, edit, delete, merge, deploy, or change a remote system?
5. **AI**: Must an agent interpret untrusted input or choose a tool/action?
6. **Reliability**: Can duplicate delivery, retry, rate limits, or a missed schedule
   cause harm?
7. **Security**: Does it need credentials, a privileged workspace, or write scope?

A workflow with an external side effect is never “just a simple example” for
activation. It may have a simple graph, but its preflight must still be strict.

## Promotion rules

Start simple and promote only when a concrete failure or requirement demands it:

- Simple → medium when a second independent check or an explicit condition is
  needed. Add `needs`, `if`, and job timeouts together so the graph remains visible.
- Medium → advanced when an external event, agent, MCP action, human approval,
  or cross-system handoff is added. Write the event identity and side-effect
  policy before adding the agent prompt.
- Advanced → split into multiple workflows when the graph has unrelated outcomes,
  different owners, or different credentials. One pipeline should have one
  business outcome.

Do not use `every` to imitate an event source. Do not add an agent merely to
format deterministic text. Prefer `queue` for ordered bursts on one rendered
concurrency key; it is process-local and bounded — see `capability-matrix.md`.

## Level acceptance checklist

### Simple

- [ ] One trigger is unambiguous and timezone is explicit for calendar work.
- [ ] The command is deterministic, bounded by a timeout, and safe to repeat.
- [ ] No untrusted event text is concatenated into a shell command.
- [ ] The YAML stays disabled until validation and review finish.

### Medium

- [ ] Job IDs are nouns, dependencies point only backward, and the DAG is acyclic.
- [ ] Independent jobs are truly independent, otherwise use `needs`.
- [ ] Conditions use only the supported expression language.
- [ ] Failure, timeout, and `continue_on_error` behavior is deliberate.
- [ ] Output is visible in logs or a known workspace file.

### Advanced

- [ ] The event has a stable identity and an early `where` filter.
- [ ] Concurrency key and duplicate policy are documented for the side effect.
- [ ] The agent receives only the data and permissions needed for its role.
- [ ] MCP tools are discovered by live ref and schema, never guessed.
- [ ] The agent verifies target identity and observes a successful result.
- [ ] Human approval is required before irreversible or public mutation unless
  the owner has explicitly approved an equivalent policy.
- [ ] A failed run has a diagnosis path, a bounded retry decision, and a safe
  manual recovery path.
