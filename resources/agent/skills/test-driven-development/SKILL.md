---
name: test-driven-development
description: Strict TDD: write a failing test first (RED), watch it fail, write minimal code (GREEN), refactor; vertical tracer bullets, one behavior per cycle. NO PRODUCTION CODE WITHOUT A FAILING TEST FIRST. Use when implementing new features, fixing bugs, refactoring, or changing behavior; when the user asks to develop test-first, write tests before code, follow TDD/red-green-refactor, or wants regression coverage for a fix.
metadata:
  source: "hermes-agent skills/software-development/test-driven-development v1.1 (MIT, adapted from obra/superpowers) — adapted"
  version: "2"
---

# Test-Driven Development (TDD)

Write the test first. Watch it fail. Write the minimal code to pass it.

**Core principle: if you did not watch the test fail, you do not know if it tests the right thing.**

## When to use

**Always**: new features, bug fixes, refactoring, behavior changes. **Exceptions (ask the user first)**: throwaway prototypes, generated code, configuration files.

Thinking "skip TDD just this once"? Stop. That is rationalization.

## The Iron Law

```
NO PRODUCTION CODE WITHOUT A FAILING TEST FIRST
```

Wrote code before the test? Delete it. Start over. No exceptions: do not keep it "as reference", do not "adapt" it while writing tests, do not look at it. Delete means delete. Implement fresh from tests. Period.

## The RED-GREEN-REFACTOR cycle

### RED — write a failing test

One minimal test showing what should happen. Requirements: one behavior per test; a clear descriptive name (name needs "and"? Split it); tests real code, not mocks (unless truly unavoidable); the name describes behavior, not implementation.

**VERIFY RED — MANDATORY, never skip.** Run the test (via `exec`). Confirm: the test FAILS (not an error from a typo), the failure message matches expectations, and it fails because the feature is missing.

- Test passes immediately? You are testing existing behavior. Fix the test.
- Test errors? Fix the error, re-run until it fails correctly.

### GREEN — minimal code

The simplest code to make the test pass. Nothing more.

- Do not add features, refactor other code, or "improve" beyond the test.
- **Cheating is OK in GREEN**: hardcode return values, copy-paste, duplicate code, skip edge cases. You will fix it in REFACTOR.

**VERIFY GREEN — MANDATORY.** Run the specific test (passes), then run the FULL suite (no regressions), output pristine (no errors/warnings). Test fails? Fix the code, not the test. Other tests fail? Fix regressions now.

### REFACTOR — clean up

After green only: remove duplication, improve names, extract helpers, simplify expressions. Keep tests green throughout; do not add behavior. If tests fail during refactor: undo immediately, take smaller steps.

### Repeat

Next failing test for the next behavior. One cycle at a time.

## Avoid horizontal slices

Do NOT write all tests first and then all implementation. That is horizontal slicing: RED becomes "write a pile of imagined tests" and GREEN becomes "make the pile pass" — brittle tests, because they are designed before the implementation taught you which behavior and interface actually matter.

Use vertical tracer bullets instead:

```
WRONG:
  RED:   test1, test2, test3, test4
  GREEN: impl1, impl2, impl3, impl4

RIGHT:
  RED→GREEN: test1→impl1
  RED→GREEN: test2→impl2
  ...
```

A tracer bullet is one end-to-end behavior slice. It proves the path works, teaches you about the interface, and keeps each next test grounded in what you just learned.

## Why order matters

- **"I'll write tests after to verify it works"** — tests written after code pass immediately. Passing immediately proves nothing: they might test the wrong thing, test implementation instead of behavior, miss edge cases you forgot, and you never saw the test catch the bug. Test-first forces you to see the test fail, proving it actually tests something.
- **"I already manually tested all the edge cases"** — manual testing is ad-hoc: no record, cannot re-run, easy to forget cases under pressure. Automated tests are systematic and run the same way every time.
- **"Deleting X hours of work is wasteful"** — sunk cost fallacy. Your choice: delete and rewrite with TDD (high confidence) vs keep it and add tests after (low confidence, likely bugs). The "waste" is keeping code you cannot trust.
- **"TDD is dogmatic, being pragmatic means adapting"** — TDD IS pragmatic: finds bugs before commit, prevents regressions, documents behavior, enables refactoring. "Pragmatic" shortcuts = debugging in production = slower.
- **"Tests after achieve the same goals"** — no. Tests-after answer "what does this do?" Tests-first answer "what should this do?" Tests-after are biased by your implementation; you test what you built, not what is required.

## Common rationalizations

| Excuse | Reality |
|---|---|
| "Too simple to test" | Simple code breaks. A test takes 30 seconds |
| "I'll test after" | Tests passing immediately prove nothing |
| "Tests after achieve the same purpose" | Tests-after = "what does it do?"; tests-first = "what should it do?" |
| "Already manually tested" | Ad-hoc ≠ systematic. No record, cannot re-run |
| "Deleting X hours is wasteful" | Sunk cost fallacy. Keeping unverified code is technical debt |
| "Keep as reference, write tests first" | You will adapt it. That is testing after. Delete means delete |
| "Need to explore first" | Fine. Throw away exploration, start with TDD |
| "Test hard = design unclear" | Listen to the test. Hard to test = hard to use |
| "TDD will slow me down" | TDD is faster than debugging. Pragmatic = test-first |
| "Existing code has no tests" | You are improving it. Add tests for the code you touch |

## Red flags — STOP and start over

Code before test; test after implementation; test passes immediately on first run; cannot explain why the test failed; tests added "later"; rationalizing "just this once"; "I already manually tested"; "tests after achieve the same purpose"; "keep as reference" or "adapt existing code"; "already spent X hours"; "TDD is dogmatic". ALL of these mean: delete the code, start over with TDD.

## Verification checklist

- [ ] Every new function/method has a test
- [ ] Watched each test fail before implementing
- [ ] Each test failed for the expected reason (feature missing, not typo)
- [ ] Wrote minimal code to pass each test
- [ ] All tests pass
- [ ] Output pristine (no errors, warnings)
- [ ] Tests use real code (mocks only if unavoidable)
- [ ] Edge cases and errors covered

Cannot check all boxes? You skipped TDD. Start over.

## When stuck

| Problem | Solution |
|---|---|
| Don't know how to test | Write the wished-for API. Write the assertion first. Ask the user |
| Test too complicated | Design too complicated. Simplify the interface |
| Must mock everything | Code too coupled. Use dependency injection |
| Test setup huge | Extract helpers. Still complex? Simplify the design |

## NusaShell integration

- Run tests with `exec` at each step: RED (verify failure) → GREEN (verify pass) → full suite (verify no regressions).
- **With delegate**: when dispatching implementation to a subagent, enforce TDD in the brief: "1) write the failing test FIRST 2) run it to verify it fails 3) minimal code to pass 4) run to verify it passes 5) refactor if needed 6) commit. Project test command: <cmd>."
- **With systematic-debugging**: bug found? Write a failing test reproducing it, follow the TDD cycle. The test proves the fix and prevents regression. Never fix a bug without a test.

## Final rule

```
Production code → test exists and failed first
Otherwise → not TDD
```

No exceptions without the user's explicit permission.