# Agent Chat

The shell-wide agent can control plugins, answer questions about NusaShell, and call MCP tools on your behalf.

## How the agent sees the conversation

1. Static system prompts (`system.md`, `mcp-tools.md`, `developer.md`) are injected before the conversation messages.
2. The developer prompt includes runtime context such as `{{current_date}}`, `{{environment}}`, `{{workspace}}`, and `{{available_tools}}`.
3. The conversation workspace is the source of truth for agent tool I/O: bundled Terminal/Files calls are workspace-bound, and `subagent` uses it as the ACP cwd (user home when unset). Prefer absolute paths in prompts when location matters; read `workspace` from the `subagent` tool result before telling the user where files went.
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

## Agent Canvas

Completed assistant messages can carry **canvas fences** — fenced code blocks
tagged `html`/`htm`, `svg`, or `mermaid`. The shell renders these beside the
conversation in a shell-owned preview pane.

When to draw Mermaid and which diagram type to pick (flowchart, sequence, class,
ER, state, and the rest of Mermaid’s sample gallery): see
[mermaid-workflow.md](mermaid-workflow.md). If the visual must be dynamic or
interactive beyond what Mermaid can render, use an `html` canvas fence instead
(auto-rendered inline; Sidebar for the drawer); use `svg` for static custom drawings.

- **SVG** and **Mermaid** fences auto-render inline on completed messages.
  Mermaid is lazy-loaded and runs with `securityLevel: 'strict'`, so it
  compiles to static SVG. A **Sidebar** action promotes the fence into the
  canvas pane.
- **HTML** fences also auto-render inline in a sandboxed iframe
  (`sandbox="allow-scripts"`, no `allow-same-origin`) with a
  Content-Security-Policy that denies every remote origin in v1 (empty
  external allowlist). Remote scripts, stylesheets, and fonts referenced by
  the artifact fail closed; this is intended. Inline scripts and styles work.
  **Sidebar** opens the drawer; **Show source** hides the inline preview and
  shows a scrollable source block (about 10 rows).
- Artifacts persist per conversation (max 20 and 3 MB total, oldest non-active
  evicted) and survive compaction. Switching conversations hides the pane and
  reopens it only for the newly selected conversation's active artifact.
- The canvas can be disabled from **Settings → Startup & background → Agent
  Canvas**. When off, canvas fences stay as source code blocks only.

The canvas is shell chrome, not a plugin window. It does not vet or moderate
model output — the source is always visible verbatim in the message — and it
does not expand the deferred host-isolation or MCP/AI behavioral-security
scope.
