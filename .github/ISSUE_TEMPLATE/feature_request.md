---
name: Feature request
about: Propose a new capability, tool, or provider adapter
title: "[feat]: "
labels: ["enhancement"]
---

## Problem

<!-- What is missing or inconvenient today? One paragraph is fine. -->

## Proposed solution

<!-- Describe the user-facing change. If the proposal touches the wire
contract, mention which `contracts/` types or `application/` dispatchers are
affected. -->

## Alternatives considered

<!-- Briefly: what other approaches did you weigh, and why this one? -->

## Layer of impact

<!-- Tick the layers that would change. -->

- [ ] `domain/`
- [ ] `application/`
- [ ] `contracts/` (wire types)
- [ ] `infrastructure/` (adapters, stores, MCP)
- [ ] `transport/` (RPC, WebSocket, static serving)
- [ ] `cmd/nusashell/`
- [ ] `frontend/`
- [ ] `resources/agent/` (system prompt, docs, skills)
- [ ] Build / packaging only
- [ ] Docs only

## Breaking change?

<!-- Tuan's AGENTS.md says breaking changes are confirmed with the maintainer
before implementation. If yes, the proposal needs an explicit "Breaking
change" section in the PR before code lands. -->

- [ ] Yes, this alters a wire contract, persisted data, or public RPC method
- [ ] No, fully backwards-compatible

## Additional context

<!-- Screenshots, mockups, references to other tools, etc. -->
