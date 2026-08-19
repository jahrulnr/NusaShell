# Prerequisites

Before authoring, use `mcp_list` and live discovery to confirm:

- A Files-like plugin is running for bounded file reads/writes.
- A Terminal-like plugin is running for absolute-cwd commands, builds, and tests.
- The conversation is interactive so `ask_question` can confirm replacement or
  removal before `mcp_register` or `mcp_unregister`. The runtime does not enforce
  that confirmation boundary for unattended jobs.

If a prerequisite is missing, ask the user to enable an installed substitute.
Do not write to the repository or bypass the shell broker.
