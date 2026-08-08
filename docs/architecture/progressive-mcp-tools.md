# Progressive MCP tools

The agent must not receive every MCP schema at the start of a turn for **idle**
plugins. One server may expose hundreds of tools while providers commonly impose
function-count and prompt-size limits. When a plugin is **running**, its full
tool catalog (name + description + `inputSchema`) is carried in the hidden
per-conversation runtime checkpoint, not the system prompt. Provider `tools[]`
remains bounded to callable routes; the checkpoint supplies complete awareness
while `tool_schema` is the escape hatch for a known tool outside that typed
working set. Progressive discovery remains the path for **starting** plugins
(`mcp_enable`) and for stale, truncated, or failed capability lookup.

## Initial tool set

Every turn starts with thirteen shell-owned meta-tools:

- `mcp_list` — list installed MCP plugins with runtime state and autostart.
- `mcp_enable` — start one plugin MCP through `PluginRuntimeManager`.
- `mcp_disable` — stop one running plugin MCP through
  `PluginRuntimeManager`.
- `mcp_register` / `mcp_unregister` — interactive confirmation-gated admission
  and removal for user plugin folders directly under `{userData}/plugins/`; jobs
  and background turns are denied, and bundled plugins are protected.
- `tool_search` — search a running plugin's MCP tool names/descriptions with a
  bounded result set. Match mode is **token-OR**: the query is split on
  whitespace and a tool matches when **any** token hits the name (+3) or
  description (+1); results are sorted by score desc then name asc, capped at
  20. Returns an envelope `{ pluginId, query, matchMode: "token_or", count,
  matches, hint? }` — `count: 0` with `matches: []` and a `hint` is a valid
  success (no tools matched), not a turn interrupt or failure. Whole-phrase
  substring match was replaced because multi-keyword queries like "read file
  list directory terminal" silently returned zero hits.
- `tool_list` — list all tool names and descriptions from a running MCP plugin
  without a search query. Returns `{ pluginId, count, tools }`.
- `tool_schema` — return one tool schema and advertise that concrete tool for
  the current turn (optional when recalling a known `mcp_*` name on a running
  plugin).
- `tool_schemas` — advertise multiple tool schemas for the current turn in one
  call (same optional rule as `tool_schema`).
- `mcp_context` — access non-tool MCP context through an explicit action:
  `list_prompts`, `get_prompt`, `search_resources`,
  `list_resource_templates`, `complete`, or `read_resource`.
- `docs_search` — search the internal Markdown documentation corpus for
  keyword-scored chunks.
- `docs_list` — list all documents in the internal documentation corpus.
- `docs_read` — read a full document, or a single chunk by `chunk_id`, with
  optional `max_chars`/`offset` pagination.
- `skill_list` — list installed local skill summaries.
- `skill_search` — search installed skills by name and description.
- `skill_read` — read bounded text from one selected skill package.
- `ask_question` — interactive clarifying question available only on
  interactive desktop turns. The turn pauses until the user answers via
  `agent.ask_answer` or cancels the turn. Background jobs and review turns
  never receive this tool.

`tool_schema` / `tool_schemas` advertise a concrete MCP tool (with schema) for
the rest of the current turn so the provider can call it through the typed
broker path. Grants are scoped by turn trace ID and cleared in a `finally`
cleanup, so concurrent conversations cannot share advertised catalogs. Running
plugin routes are seeded into the conversation-local callable working set for
direct continuation; the provider `tools[]` array is hard-capped at 96 MCP
entries beyond meta-tools. The full catalog is separately retained in the
runtime checkpoint, so tools beyond that cap remain known and can be selected
with one `tool_schema` call instead of broad rediscovery.

The model may also call a previously used `mcp_<plugin>_<tool>` name **without**
a prior grant when that plugin is already `running`. The gateway lazily resolves
the name against running plugins, executes the matching tool, and auto-advertises
it for the remainder of the turn. Idle/stopped plugins never match. Wrong names
or stopped plugins return a normal tool error; the turn continues. Soft-reject
still applies to clearly non-NusaShell names (for example `ReadFile`). There is
no untyped catch-all `tool_call` function.

`mcp_context` keeps prompts and resources out of the launcher UI and out of the
initial prompt payload. Prompt arguments remain strings and required arguments
are validated. Prompt/resource search is capped at 20 results; text resource
reads are capped at 50 KB. Binary resource content is not injected into the
provider. Plugin-authored prompts are the narrative howto channel for that
plugin; shell docs remain platform-only and must not duplicate removable plugin
catalogs.

The foreground agent also owns `memory`, `skill_manage`, and `job` meta-tools
(personal memory, agent-owned skill mutation, and scheduled automation
management respectively). `job` exposes the same CRUD surface as the `job.*`
WS methods and is denied inside scheduled job turns; see
[`job-automation.md`](job-automation.md) for the action table and recursion
guard.

The Agent UI intentionally has no MCP catalog, scope selector, prompt browser,
or resource attachment panel. The model can discover, start, stop, and inspect
MCPs when needed; NusaShell remains the only broker.

## Autostart

Autostart is a user preference per installed plugin, persisted separately from
live runtime state. On backend startup, `PluginRuntimeManager` starts opted-in
plugins independently. One failure is logged and never blocks shell startup.
