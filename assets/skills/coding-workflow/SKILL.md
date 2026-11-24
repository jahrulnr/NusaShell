---
name: coding-workflow
description: Use when changing code, tests, build configuration, or a user interface — read the project guide and existing patterns, fix root causes, run the project's build/tests/lint, and verify UI in a real rendered view rather than trusting code or unit tests alone.
metadata:
  short-description: Change code and verify it properly
---

# Coding workflow

## Project conventions

- Read `AGENTS.md` or the project's architecture/contribution guide when present, and follow it.
- Read the existing implementation and nearby patterns first. Use `search` / `rg` and `read_file`; do not design from names alone.
- Trace behavior through its callers before moving a rule or adding a second copy of it.
- Follow the project's compatibility policy. If the project has none, keep the existing public contracts unless the owner asks to change them.

## Implement

- Keep the change scoped to the request.
- Fix the root cause. If the existing design is wrong, correct it and update the affected callers rather than hiding the symptom.
- Preserve behavior outside the request. Read the whole flow before splitting a method; extract only for reuse, a testable contract, or a real dependency boundary.
- Match the project's naming, error handling, logging, persistence, and test style.
- Add or update tests that assert the intended behavior and observable ordering, not the presence of a helper.
- Keep debug output, temporary files, and unrelated formatting out of the diff.

## Verify

Use the build, test, and lint commands the project defines — a Makefile, package scripts, task runner, CI configuration, README, or contributor guide. Run the checks that cover the change, then report the exact commands and results. For example, a Go project may use `go build ./...` and `go test ./...`, but use whatever that project specifies.

If a check cannot run, say so and explain what remains unverified; never claim a check passed when it was not run.

For UI changes, verify the actual rendered result:

1. Start the app or open the affected page.
2. Use a browser automation tool or a desktop screenshot tool available in the environment.
3. Inspect the screenshot: scrolling, layout, margins, padding, borders, overflow, truncation, responsive widths, empty/loading/error states, and interactive behavior.
4. Resize or use device emulation when responsiveness matters; re-check after the change.
5. Never call UI work correct from code or unit tests alone. If it cannot be rendered in this environment, say which visual checks are still pending.

For a bug fix, reproduce the failure before the fix when practical and confirm the fix against the same reproduction. For a refactor, confirm behavior with the existing tests before and after.
