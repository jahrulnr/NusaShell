# Agent runtime tasks

- [ ] Define agent contracts and red tests.
  - Acceptance: messages, tool calls, provider results, and trace events are
    discriminated and provider independent.
  - Verify: focused Vitest suite fails before implementation.

- [ ] Implement bounded MCP-only turn loop.
  - Acceptance: text, tool success, tool failure, unknown tool, and round-limit
    paths are covered.
  - Verify: focused Vitest suite passes.

- [ ] Wire provider registry and backend command.
  - Acceptance: `agent.run` invokes the configured provider without exposing a
    key or bypassing `PluginRuntimeManager`.
  - Verify: application and backend integration tests pass.

- [x] Add renderer agent surface and trace visibility.
  - Acceptance: users can submit a prompt and inspect returned tool activity.
  - Verify: renderer build and a manual Electron smoke test pass.
