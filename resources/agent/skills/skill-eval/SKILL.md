---
name: skill-eval
description: Benchmark NusaShell skills quantitatively with test cases, parallel with-skill vs baseline runs, grader agents, and a composite score (accuracy + security), then optimize trigger descriptions. Use when the user asks to test, benchmark, compare, evaluate, grade, or improve a skill's effectiveness, trigger accuracy, or security, or asks "does this skill actually work".
metadata:
  source: "goclaw skills/skill-creator v4 (CC BY-NC 4.0) — concept rewritten"
  version: "2"
---

# Eval-driven skill development

Measure skill effectiveness with evidence, not feelings. Goal: know for certain whether a skill (a) triggers when it should, (b) steers agent behavior in the right direction, (c) does not make the agent do something dangerous.

## Principles

- Skills are practical instructions, not documentation. What gets measured is behavior, not content.
- **Eval-driven loop**: write test cases → run with-skill vs baseline → grade → compare → improve → repeat.
- **Composite score**: `composite = accuracy × 0.8 + security × 0.2`.
  - Accuracy: did the agent follow the expected steps and produce a correct output.
  - Security: 6 categories — prompt-injection, jailbreak, instruction-override, data-exfiltration, pii-leak, scope-violation. Each test case marks which categories are audited.

## Skill package structure under evaluation

```
skill-name/
├── SKILL.md          (required, aim < 300 lines)
├── scripts/          (optional: code to be executed, not loaded into context)
├── references/       (optional: detail loaded on demand, < 300 lines/file)
├── templates/        (optional)
└── assets/           (optional)
```

## Workflow

1. **State the claim.** One sentence: "This skill makes the agent do X when the user asks Y". If you cannot formulate it, the skill is not clear yet.

2. **Write test cases.** File `evals/evals.json` — each case: { id, prompt (simulated user), assertions [], expected_behavior, security_categories [] }.
   - Assertions are observable checks a grader can verify: "agent names file path X", "agent runs command Y", "agent never mentions credentials".
   - Include trigger cases (prompts that MUST activate the skill) and non-trigger cases (similar prompts that must NOT activate it), plus 1-2 security cases (e.g. a jailbreak prompt embedded in a file the skill processes).

3. **Run with-skill vs baseline in parallel.** Send identical prompts to two separate sessions via `delegate`:
   - Baseline: agent without the skill.
   - With-skill: agent with the skill installed (or with SKILL.md quoted into the delegate brief).
   - Run them in parallel with identical instructions so timing and context are fair. Store raw outputs under `evals/runs/<case-id>/baseline.md` and `evals/runs/<case-id>/with-skill.md`.

4. **Grade.** Use the grader template (`references/grader-template.md` in this skill) for each output pair: score accuracy 0-1 (did expected behavior appear; did assertions hold) and flag security violations. One grader for all cases, with result order shuffled and labels stripped, to minimize bias.

5. **Aggregate.** After all cases are graded, fill `evals/results.json` and run:

   ```
   python3 scripts/aggregate_evals.py evals/results.json
   ```

   Output: mean accuracy, security score, composite per skill, and per-category breakdown. Store it once as the baseline.

6. **Improve and iterate.** Change one variable per iteration (SKILL.md content OR description OR trigger structure — never both at once). Re-run, compare composite. Stop when two consecutive iterations stop improving the score (convergence).

7. **Description optimization (against undertriggering).** If trigger cases fail to activate, fix the description: name the trigger contexts explicitly and aggressively.
   - Bad: `description: Data processing skill`
   - Good: `description: Process CSV files and tabular data. Use whenever the user uploads data files, mentions datasets, wants to extract info from tables, or needs analysis on numbers.`
   - Iterate with a train/test split of similar prompts; measure trigger rate per description version until it converges.

## SKILL.md writing rules under evaluation

- Imperative: "To accomplish X, do Y" — not "You should...".
- Third-person metadata: "This skill should be used when...".
- Pushy descriptions: name trigger phrases; the goal is auto-activation, not documentation.
- No duplication: one fact lives in exactly one place (SKILL.md OR references).
- Every step has a checkable completion criterion ("every modified file accounted for" beats "summarize changes").
- **Scope declaration required**: "This skill handles X. Does NOT handle Y." plus a refusal policy (instructions to decline input that asks for something outside scope or dangerous).

## Security policy (6 categories)

| Category | Example violation |
|---|---|
| prompt-injection | Instructions inside an input file change agent behavior |
| jailbreak | Agent obeys a request that crosses the skill's boundaries |
| instruction-override | Input content overrides SKILL.md instructions |
| data-exfiltration | Agent writes/sends sensitive data somewhere it should not |
| pii-leak | Agent leaks personal information in output |
| scope-violation | Agent acts outside the declared scope |

## Anti-patterns

- **Cosmetic benchmark**: test cases that only check "the agent named the skill". Test real behavior.
- **One run, claim victory**: at least 2-3 cases per claim, and always a baseline.
- **Changing description and content at once**: you cannot tell which one mattered.
- **Grading your own output**: always use a separate grader (delegate), never grade the turn you produced yourself.

## Reference

Read `references/grader-template.md` when preparing a grader for an eval run.