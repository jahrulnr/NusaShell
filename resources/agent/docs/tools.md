# Agent tools

The agent ships with a built-in toolbox plus one tool per MCP server tool.

## Built-ins

| Tool | Purpose |
| --- | --- |
| `skill_list` | list available skills (name + description) |
| `skill_search` | search installed skills by name or description (case-insensitive substring) |
| `skill_read` | read a skill's full markdown content by name |
| `skill_run` | load a skill's instructions by name (alias of `skill_read` for the legacy prompt path) |
| `memory_save` | persist a fact with optional tags |
| `memory_search` | substring search over memory |
| `memory_list` | list all memory entries |
| `memory_delete` | remove a memory entry by id |
| `todo` | replace the conversation task checklist (full-replace, Claude TodoWrite style; max 50 items, 500 chars each; prefer exactly one `in_progress` at a time). The user can delete items from the UI — treat deleted items as gone and do not re-add them. |
| `docs_search` | search the product documentation |
| `docs_read` | read a documentation page by id |
| `mcp_list` | list all plugins (MCP servers) with runtime state: every plugin appears, running or idle |
| `tool_list` | list tools from a running plugin's MCP server (or across all running servers when the server is omitted) |
| `tool_search` | search a running MCP server's tools by name or description |
| `tool_schema` | load one MCP tool's input schema by server and tool name before calling it |
| `read_image` | load an image from the conversation into the model's context (vision models see it directly; non-vision models get a text description via the vision fallback) |
| `web_search` | search the web across Brave, Startpage, Wikipedia, and GitHub; returns ranked results with title, URL, and snippet |
| `web_fetch` | fetch a URL and return readable text; supports HTML, JSON (pretty-printed), XML/RSS/Atom, Markdown, CSV, and plain text with newlines preserved; collects links and selected response headers; honors `max_bytes`; surfaces `Retry-After` on 429/503 and structured JSON error bodies |
| `web_answer` | get a web-grounded answer via an LLM with built-in web search (only available when an answer-provider API key is configured) |
| `ci_pipeline_list` | list `.nusashell/pipeline.yaml` jobs in a workspace |
| `ci_pipeline_read` | read and validate the workspace pipeline |
| `ci_pipeline_validate` | validate pipeline/workflow YAML (INVALID vs BLOCKED) |
| `ci_run` | start a workspace pipeline or a saved automation |
| `ci_run_status` | DAG summary and status; use this after `ci_run` |
| `ci_logs` | job log tail (prefer failed jobs) |
| `ci_cancel` | cancel a run |
| `automation_list` | saved automations with availability |
| `automation_read` | one automation plus capability bindings |
| `automation_validate` | validate YAML |
| `automation_create` | persist a once/every/when/manual workflow (NusaShell owns the schedule) |
| `automation_enable` / `automation_disable` | lifecycle without deleting |
| `automation_status` | inspect waiting/blocked runs |
| `schedule_once` | one-shot RFC3339 automation |
| `schedule_every` | cron or interval automation (not equivalent) |
| `wait_until` | durable wait; the runner is not occupied |
| `subagent` | spawn 1–6 ACP coding-agent sessions (only listed when at least one ACP agent is enabled in Providers; never listed for pipeline `agent:` steps) |
| `subagent_steer` | queue an extra instruction on a live ACP run |
| `subagent_stop` | cancel a live ACP run (pending permissions fail closed) |
| `subagent_wait` | wait for an async ACP run to finish |

The system prompt advertises the same set: `skill_list`, `skill_search`,
`skill_read`, `memory_*`, `docs_*`, `mcp_list`, `tool_list`, `tool_search`,
`tool_schema`, `read_image`, `web_search`, `web_fetch`,
`web_answer` (when available), `ci_*`, `automation_*`, `schedule_once`,
`schedule_every`, `wait_until`, plus `mcp__<server>__<tool>` for each enabled MCP server.
When at least one ACP agent is enabled, the interactive toolbox also advertises
`subagent`, `subagent_steer`, `subagent_stop`, and `subagent_wait`. ACP agents
do not receive this conversation, NusaShell MCP plugins, or shell meta-tools.
Pipeline `agent:` steps hide those four tools entirely so a fail-closed
permission prompt cannot stall an unattended run.

## ACP subagents

`subagent` fans a self-contained brief out to 1–6 parallel ACP sessions
(process-wide cap 8 live runs). Pass absolute paths. `workspace` overrides the
conversation workspace for new spawns only — an already-running session keeps
the directory it started with. `async: true` returns run ids immediately;
otherwise the tool waits for each spawn. Permission prompts are fail-closed
(timeout = deny). The user can peek, steer, stop, and promote risk from the
Agent dock / drawer / popup. Unattended pipeline agents never see these tools.

## Native web research (searchwire)

NusaShell ships with built-in web research tools powered by
[searchwire](https://github.com/jahrulnr/searchwire). These are native tools,
not MCP plugins — they work with zero configuration and no plugins installed
installed.

- **`web_search`**: metasearch across Brave, Startpage, Wikipedia, and
  GitHub. No API key required for the default path (HTML scraping + public
  APIs). Returns ranked, deduplicated results with snippets.
- **`web_fetch`**: fetches a URL and returns readable text. Supports HTML
  (nav/footer/aside/form stripped, `<pre>`/`<code>` preserved, links
  collected, `og:title`/`<h1>` title fallbacks), JSON (pretty-printed;
  invalid JSON returned raw), XML/RSS/Atom (tag-stripped, newlines
  preserved), Markdown/CSV/plain text (newlines preserved). Non-UTF-8
  charsets are decoded. Surfaces `ETag`/`Last-Modified`/rate-limit
  headers, redirect count, and a `[truncated]` marker. Non-2xx returns
  `Retry-After` (seconds) and parsed JSON error envelope when present.
- **`web_answer`**: web-grounded LLM answer (optional). Only
  registered when a vendor and API key are configured in Settings → Web Answer.
  This is a separate config from the chat providers — pick a searchwire-supported
  vendor (Brave, OpenRouter, OpenAI, Perplexity, Anthropic, xAI) and enter its
  API key manually. The key is stored in the credential store, not in settings
  JSON. An optional model/preset override can be set per vendor.

Recommended workflow: `web_search` → pick URLs → `web_fetch`.

## MCP tools

The agent-facing tool names keep the `mcp_*` prefix even though the data
model is now "plugins": the tools genuinely come from each plugin's MCP
server, and the shell still connects to that server to fetch them. Every
plugin exposes its MCP tools as `mcp__<server>__<tool>`
with the server's own input schema. The shell connects to the server on
first use (stdio) and keeps the connection for the process lifetime;
re-saving or deleting the server drops the connection.
