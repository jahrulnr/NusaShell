---
name: systematic-debugging
description: Root-cause debugging in 4 phases: investigate first (read errors carefully, build a tight red-capable feedback loop, check recent changes, trace data flow), analyze patterns, test ranked falsifiable hypotheses one variable at a time, then implement the fix. NO FIXES WITHOUT ROOT CAUSE. Use for ANY technical issue: test failures, bugs, unexpected behavior, performance problems, build failures, flaky tests, CI failures. Especially under time pressure or after multiple failed fixes.
metadata:
  source: "hermes-agent skills/software-development/systematic-debugging v1.1 (MIT, adapted from obra/superpowers) — adapted"
  version: "2"
---

# Systematic debugging

Random fixes waste time and create new bugs. Quick patches mask underlying issues.

**Core principle: ALWAYS find the root cause before attempting fixes. Symptom fixes are failure.**

## The Iron Law

```
NO FIXES WITHOUT ROOT CAUSE INVESTIGATION FIRST
```

If you have not completed Phase 1, you cannot propose fixes.

## The feedback loop rule

The feedback loop is the debugging work. Before reading code to build a theory, create or identify a **tight** command that can go red on the user's exact symptom and green when the bug is fixed. A tight loop is fast, deterministic, agent-runnable, and specific enough to catch this bug — not merely "doesn't crash".

When a clean repro is hard, spend disproportionate effort building the loop. Guessing without a red-capable loop is the failure mode this skill exists to prevent.

## When to use

Use for ANY technical issue: test failures, bugs in production, unexpected behavior, performance problems, build failures, integration issues. Use ESPECIALLY when: under time pressure (emergencies make guessing tempting), "just one quick fix" seems obvious, you have already tried multiple fixes, a previous fix did not work, you do not fully understand the issue.

Do not skip when: the issue seems simple (simple bugs have root causes too), you are in a hurry (rushing guarantees rework), or someone wants it fixed NOW (systematic is faster than thrashing).

## The four phases — complete each before proceeding

### Phase 1: Root cause investigation

BEFORE attempting ANY fix:

1. **Read error messages carefully** — do not skip past errors or warnings; they often contain the exact solution. Read stack traces completely. Note line numbers, file paths, error codes.
2. **Build a tight feedback loop** — one command that triggers the user's exact symptom, fails for this bug and only passes once the bug is fixed, is fast enough to run repeatedly, and is deterministic (for flaky bugs, raise the reproduction rate enough to debug). If not reproducible → gather more data, do not guess. Ways to construct a loop, roughly in order:
   1. Failing test at the seam that reaches the bug (unit/integration/e2e)
   2. HTTP script / curl against a running dev server
   3. CLI invocation with fixture input, diffing stdout/stderr against expected output
   4. Headless browser script (Playwright/Puppeteer) asserting on DOM, console, or network
   5. Replay a captured trace: HAR, request payload, event log, queue message, webhook body
   6. Throwaway harness that boots the smallest useful slice of the system and calls the failing path
   7. Property / fuzz loop for intermittent wrong output over a broad input space
   8. Bisection harness suitable for `git bisect run` when the bug appeared between two known states
   9. Differential loop comparing old vs new version, two configs, two providers, or two datasets
   10. Human-in-the-loop script only as a last resort
   - Tighten the loop: make it faster (cache setup, narrow scope), sharpen the signal (assert the exact symptom, not generic success), make it more deterministic (pin time, seed randomness, isolate filesystem).
   - For non-deterministic bugs the immediate goal is a higher reproduction rate, not perfection. Run the trigger 100x, parallelize, add stress. A 50% flake is debuggable; a 1% flake usually is not.
3. **Check recent changes** — `git log --oneline -10`, `git diff`, `git log -p --follow <file>`.
4. **Multi-component systems: gather evidence at every boundary** — log what enters and leaves each component, check environment/config propagation, inspect state at each layer. Run once to show WHERE it breaks, then analyze.
5. **Trace data flow** — where does the bad value originate? Who called this function with it? Use `grep` to trace references, keep going upstream until you find the source. Fix at the source, not at the symptom.

**Phase 1 completion checklist:** errors fully read; a tight loop command exists and has been run at least once; loop is red-capable (asserts the exact symptom); loop is deterministic or the flake rate is high enough; recent changes reviewed; evidence gathered; problem isolated to a specific component/code; root cause hypotheses can be stated and tested.

**STOP:** do not proceed to Phase 2 until you understand WHY.

### Phase 2: Pattern analysis

1. **Minimize the reproduction** — once the loop is red, shrink it to the smallest scenario that still goes red. Cut inputs, callers, config, data, and steps one at a time, re-running after each cut. Done when removing any remaining element makes the loop go green. A minimal repro narrows the hypothesis space and often becomes the cleanest regression test.
2. **Find working examples** — similar working code in the same codebase (or elsewhere). Read the reference implementation COMPLETELY; do not skim.
3. **Identify differences** — what differs between working and broken? List every difference, however small. Do not assume "that can't matter".
4. **Understand dependencies** — what other components does this need? What settings, config, environment? What assumptions does it make?

### Phase 3: Hypothesis and testing

1. **Form 3-5 ranked falsifiable hypotheses** before testing any single one. Rank by likelihood and cheapness to falsify. Each hypothesis states a testable prediction: "If X is the cause, then changing or observing Y should make Z happen." Discard or sharpen any hypothesis without a testable prediction. If the user is present, show the ranked list before testing — they may have domain knowledge that instantly re-ranks it.
2. **Test minimally** — test the highest-ranked hypothesis with the smallest possible probe. Change one variable at a time. Do not fix multiple things at once. Prefer a debugger/REPL; one breakpoint beats ten logs. If you add logs, tag every temporary line with a unique prefix such as `[DEBUG-a4f2]` so cleanup is a single search.
3. **Verify before continuing** — worked? → Phase 4. Failed? → form a NEW hypothesis. Do not pile more fixes on top.
4. **When you don't know: say "I don't understand X"** — do not pretend; ask the user; research more.

### Phase 4: Implementation

1. **Create a failing test** — simplest possible reproduction, automated if possible, REQUIRED before fixing (see `test-driven-development` skill).
2. **Implement a single fix** — the identified root cause; ONE change at a time. No "while I'm here" improvements. No bundled refactoring.
3. **Verify** — run the specific regression test, then the full suite (no regressions).
4. **Rule of three** — fix does not work? **STOP.** Count: how many fixes tried? < 3 → return to Phase 1 with new information. **≥ 3 → stop and question the architecture** (step 5). Do NOT attempt fix #4 without architectural discussion.
5. **If 3+ fixes failed: question the architecture.** Patterns indicating an architectural problem: each fix reveals new shared state/coupling in a different place; fixes require "massive refactoring"; each fix creates new symptoms elsewhere. STOP and question fundamentals: is the pattern sound? Are we sticking with it through sheer inertia? Discuss with the user before more fixes. This is NOT a failed hypothesis — this is a wrong architecture.

## Red flags — STOP and follow the process

If you catch yourself thinking: "quick fix for now, investigate later"; "just try changing X and see"; "add multiple changes, run tests"; "skip the test, verify manually"; "it's probably X, fix that"; "I don't fully understand but this might work"; "one more fix attempt" (after 2+); "each fix reveals a new problem elsewhere" — ALL of these mean STOP, return to Phase 1.

## Common rationalizations

| Excuse | Reality |
|---|---|
| "Issue is simple, no process needed" | Simple issues have root causes too. Process is fast for simple bugs |
| "Emergency, no time for process" | Systematic debugging is FASTER than guess-and-check thrashing |
| "Just try this first, then investigate" | The first fix sets the pattern. Do it right from the start |
| "I'll write the test after confirming the fix" | Untested fixes don't stick. Test first proves it |
| "Multiple fixes at once saves time" | You can't isolate what worked. Causes new bugs |
| "Reference too long, I'll adapt the pattern" | Partial understanding guarantees bugs. Read it completely |
| "I see the problem, let me fix it" | Seeing symptoms ≠ understanding root cause |

## NusaShell integration

- Tools: `file_read` (read source + stack traces), `grep` (trace references/error strings), `exec` (run the loop, git, tests), `web_search`/`web_fetch` (research error messages, library docs).
- **With delegate**: for complex multi-component debugging, dispatch an investigation subagent with a brief: "Follow systematic-debugging: 1) read the error carefully 2) reproduce 3) trace the data flow to the root cause 4) report findings — do NOT fix yet." Include the full error, file paths, and the exact test command.
- **With test-driven-development**: write a test reproducing the bug (RED) → debug systematically → fix the root cause (GREEN) → the test proves the fix and prevents regression. Never fix a bug without a test.