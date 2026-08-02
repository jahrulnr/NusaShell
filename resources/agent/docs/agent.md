# Agent Chat

The shell-wide agent can control plugins, answer questions about NusaShell, and call MCP tools on your behalf.

## How the agent sees the conversation

1. Static system prompts (`system.md`, `mcp-tools.md`, `developer.md`) are injected before the conversation messages.
2. The developer prompt includes runtime context such as `{{current_date}}`, `{{environment}}`, `{{workspace}}`, and `{{available_tools}}`.
3. The selected conversation workspace is prompt context only — it is not injected into MCP tool arguments, environment variables, or plugin working directories. The agent must pass absolute paths explicitly to tools that need them.
4. If the context window grows too large, older turns are compacted into a summary using `compact.md`.

## Compaction

When compaction runs, the agent keeps the most recent turns and replaces older ones with a summary. The summary is stored as a system message so it remains in context. This lets long sessions stay within token limits.

## Built-in meta-tools

The agent has shell-owned tools that do not require a running MCP server:

- `docs_search`: find relevant chunks of this documentation corpus.
- `docs_list`: list all available documents.
- `docs_read`: read a specific document by path.
- `mcp_list`, `mcp_enable`, `mcp_disable`: control plugin lifecycle.
- `mcp_register`, `mcp_unregister`: confirm and admit/remove user plugins under userData/plugins; bundled plugins cannot be unregistered.
- `tool_search`, `tool_list`, `tool_schema`: discover and prepare MCP tools.
- `mcp_context`: discover prompts and resources. For a plugin's capability
  howto, use its `list_prompts` / `get_prompt` actions after enabling the plugin;
  prompt retrieval adds context and never executes a tool.
- Built-in skills, including `mcp-creator` and `skill-creator`, are seeded into
  the managed skills library with protected `builtin` provenance.

For shell/platform questions, use `docs_search`. Do not use the shell corpus as
an inventory of removable plugin tools or plugin-specific howtos; discover those
from the live plugin with `tool_list`/`tool_search` and plugin-owned MCP prompts.

These tools appear automatically; the model does not need to start an MCP server to use them.
