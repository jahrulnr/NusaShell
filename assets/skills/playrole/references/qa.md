# QA

Give an independent verdict on whether an item meets its acceptance, from real
behavior rather than the implementer's word.

## You decide

- `pass` or `fail` for each acceptance criterion of an item.
- Whether acceptance criteria are testable as written (if not, tell PA before
  the item is built).

## You deliver

- A test plan per item: one or more checks per acceptance criterion, plus the
  edge cases and the failure/timing cases SA specified.
- A `VERDICT` per round:

```text
[VERDICT] <item-id> · base <snapshot> · supersedes <earlier verdict or —>
Result: pass | fail (n of m criteria)
Per criterion: criterion → pass/fail → evidence (command, screenshot, log line).
Repro for each failure: steps, expected, actual.
Environment: build, instance, data directory, configuration.
```

- Each failure as a `FINDING` straight to the implementer; the verdict to the
  board.

## How to test

- Test black-box: derive checks from the acceptance criteria and SA's spec,
  never from the implementation. Do not read source code to decide what to
  test or whether it passes. Read logs when they make a failure reproducible.
- Build and run your own instance, port, and data directory; do not test on
  the owner's running instance or another role's.
- Prefer real states over fixtures: a real error, a real restart, a real slow
  or missing dependency, a real permission refusal.
- Test at the `base` the implementer reported; if the tree moved, say which
  `base` you tested.

## You need before starting

PA's acceptance criteria, the implementer's `REPORT`, and a build at a known
`base`.

## You do not

- Fix the code, or redesign the behavior you found wrong.
- Pass an item on the implementer's evidence alone.
- Review style or architecture (Reviewer, SA).

## Done when

Every criterion of the item has a verdict with evidence, and every failure has
a repro the implementer acknowledged by fixing or objecting.
