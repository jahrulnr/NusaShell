---
name: babysit-pr
description: Continuously watch a GitHub pull request until it is merged or closed: poll review comments, CI checks, and mergeability; classify CI failures (branch-related vs flaky), retry flaky up to 3 times, auto-fix and push for branch-related issues, with a strict GitHub state mutation policy. Use when the user asks to monitor, watch, babysit, track, or keep an eye on a PR, its CI, or its review comments until it lands.
metadata:
  source: "codex .codex/skills/babysit-pr (Apache-2.0) — adapted"
  version: "2"
---

# Babysit PR

Keep watching a PR until one of the terminal outcomes: **merged/closed**, or user help is required (broken CI infra, flaky retry budget exhausted, permissions, ambiguity). A green + mergeable + review-clean PR is **progress**, not a reason to stop, while the PR is still open.

## Input

- No argument: infer the PR from the current branch (`--pr auto` via script).
- PR number or PR URL.

## Running it (NusaShell)

One-shot snapshot:

```
bash scripts/pr_watch.sh --pr auto --once
```

Watch mode (loop inside the turn, polling every `--interval` seconds):

```
bash scripts/pr_watch.sh --pr auto --watch --interval 60
```

Retry failed checks (only when the watcher suggests `retry_failed_checks`):

```
bash scripts/pr_watch.sh --pr auto --retry-failed-now
```

Polling mechanics: `pr_watch.sh` uses `gh` (gh api / gh pr view / gh run view) and `jq`; it needs a POSIX shell (on Windows, Git Bash). Check `gh auth status` first. Output: JSON with `state`, `mergeable`, `review_decision`, `checks[]`, `reviews[]`, `actions[]` (see Core workflow). In `--watch` mode snapshots stream to stdout — consume them in the same turn; do not leave a `--watch` process running and then end the turn as if monitoring were complete.

Duration note: one turn must not block indefinitely. If the user wants hour-long supervision, suggest `--once` + resume periodically, or an automation workflow that runs snapshots on an interval and forwards results — do not loop forever inside a single turn.

## Core workflow

1. Take a snapshot (`--once` or `--watch`).
2. Read `actions` from the watcher output.
3. **Check terminal state first**: PR merged/closed → report and stop.
4. Only then check CI, new review items, and mergeability/conflict status.
5. If `diagnose_ci_failure` is present: fetch the failed job logs (`gh run view <id> --log-failed`, or the job logs endpoint directly) and classify:
   - **Branch-related** (compile/test/lint/typecheck/snapshot failures in areas the branch touches) → patch, commit, push. If in doubt, do one manual diagnosis before concluding.
   - **Flaky/unrelated** (timeouts, runner provisioning, registry/network outages, GitHub Actions infra) → do NOT fix by changing tests, build scripts, CI config, or dependency pins. Rerun only when the watcher suggests `retry_failed_checks`.
6. If `process_review_comment` is present: act only on **published** feedback (ignore PENDING review state and comments attached to pending reviews; do not mark pending feedback as handled).
7. When both review feedback and a flaky retry are present, process review feedback first (a new commit will retrigger CI; avoid rerunning flaky checks on the old SHA).
8. After any push/rerun, resume polling on the updated SHA in the same turn.

## CI classification

| Indicator | Conclusion |
|---|---|
| Failed logs point at changed code (compile/test/lint/snapshot) | branch-related → fix |
| Timeouts, runner provisioning, registry/network outage, infra errors | flaky/unrelated → do not fix |
| Ambiguous | one manual diagnosis first |

Never touch unrelated tests, build scripts, CI configuration, or dependency pins just to make CI green.

## Review comment handling

- Surfaced items include trusted human review authors (OWNER/MEMBER/COLLABORATOR plus the authenticated operator) and approved review bots; ignore unrelated bot noise.
- Agree + actionable: patch → commit `babysit: address PR review feedback (#<n>)` → push → resolve the thread (only when policy allows) with a comment prefixed `[from babysit]:` → keep watching.
- **Never reply to human-authored comments automatically.** Disagree / non-actionable / already addressed / needs a written answer → surface the item to the user with a suggested response and wait for explicit confirmation before posting anything on GitHub.
- Threads already resolved = non-actionable, ignore. Existing unaddressed feedback surfaced on a fresh watcher state is processed (do not skip feedback that predates monitoring).
- If a later snapshot surfaces your own approved reply (operator counts as a trusted author), treat it as already handled and do not reply again.

## GitHub state mutation policy

Allowed:
- push to the PR head branch (fixes or forcing CI re-runs),
- resolve review threads from the user who requested babysitting or from an approved review bot.

Do NOT (unless explicitly asked):
- comment on other humans' review threads (communicate via chat instead),
- resolve review threads from other humans,
- interact with other humans,
- change draft/ready state, close or reopen PRs.

Principle: never act on GitHub in a way that makes it hard to tell whether you or the user did something visible to other humans. When in doubt, ask the user in chat. Before any mutation, fetch the PR state yourself via `gh pr view` — do not rely on the watcher's output alone.

## Git safety

- Work only on the PR head branch. Avoid destructive git commands.
- Before editing, check for unrelated uncommitted changes; if present, stop and ask the user.
- After each fix: commit and push, then continue polling. A push is not a terminal outcome.
- Keep one watcher active per PR/state file; do not spawn multiple `--watch` processes for the same PR.
- Default commit messages: `babysit: fix CI failure on PR #<n>` / `babysit: address PR review feedback (#<n>)`.

## Stop rules

Stop only when: (a) merged/closed — report the final state; (b) a user-help blocker (infra outage, retry budget exhausted, unclear reviewer request, permissions) — report the blocker and stop. Do not ask "keep polling?" every loop; continue autonomously until a stop condition is met.