---
name: Bug report
about: Report broken behavior, crashes, or unexpected output
title: "[bug]: "
labels: ["bug"]
---

## What happened

<!-- A clear, one-sentence description of the bug. -->

## Reproduction steps

1. <!-- Step one -->
2. <!-- Step two -->
3. <!-- … -->

## Expected behavior

<!-- What you expected to see. -->

## Actual behavior

<!-- What actually happened. Include relevant log lines, error messages, or
screenshots. -->

## Environment

<!-- Tick what applies. -->

- OS: <!-- Linux / Windows / macOS, distribution & version -->
- Go version: <!-- `go version` output -->
- NusaShell version: <!-- output of `nusashell --version` or `git rev-parse HEAD` -->
- Frontend mode: <!-- embedded (default) or `NUSASHELL_DEV=1` -->
- Provider & model: <!-- Anthropic Claude Sonnet 4.5, OpenAI gpt-5, LM Studio qwen3-30b, etc. -->

## Logs and traces

<!-- Paste the relevant lines from the UI Logs view, `journalctl`, or the
terminal where you ran `make run`. Strip secrets, but keep stack traces. -->

## Additional context

<!-- Anything else that might help — MCP servers in use, attachments,
network constraints, related issues, etc. -->
