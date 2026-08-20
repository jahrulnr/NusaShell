# Agent tools

The agent ships with a built-in toolbox plus one tool per MCP server tool.

## Built-ins

| Tool | Purpose |
| --- | --- |
| `skill_list` | list available skills (name + description + owned_by + shadowed) |
| `skill_search` | search installed skills by name or description (case-insensitive substring) |
| `skill_read` | read a skill's `SKILL.md` (or a support file via `path`) by name; paginated with `offset`/`max_chars` |
| `skill_files` | list the files inside a skill folder (path, type, size, editable) |
| `skill_save` | create or update a skill (omit `id` to create new; pass `id` to update), or write a support file inside an existing skill (pass `path` like `references/errors.md`, `templates/config.yaml`, `scripts/verify.sh`; skill must already exist; plugin-owned skills are read-only) |
| `memory_save` | save a fact as a searchable fragment (unlimited archive); pick a category (`project`, `user`, `task`, `general`) + optional `project`, `task`, `tags` |
| `memory_replace` | update memory: `target="primary"` + `old_text` (substring) to edit the primary document, or omit `old_text` to rewrite the entire body; `target="fragment"` + `id` for fragments |
| `memory_search` | BM25 search over fragments with metadata filters (`category`, `project`, `task`, `tags`); returns ranked results with scores |
| `memory_list` | list entries: `target="primary"` for the always-injected document, `target="fragments"` (default) for the archive with optional metadata filters |
| `memory_delete` | delete a fragment by id |
| `todo` | replace the conversation task checklist (full-replace, Claude TodoWrite style; max 50 items, 500 chars each; prefer exactly one `in_progress` at a time). The optional `goal` argument sets a brief of what the user wants and why (max ~10k tokens). It stays available through conversation history and is re-injected with the fresh hydration checkpoint after compaction. The user can delete items from the UI — treat deleted items as gone and do not re-add them. |
| `ask_question` | block for a structured user decision; use only when progress genuinely requires a choice or approval |
| `docs_search` | search the product documentation when the page id is unknown |
| `docs_read` | read a documentation page by canonical extensionless id; a `.md` suffix is accepted as a compatibility alias |
| `mcp_list` | list all plugins (MCP servers) with runtime state: every plugin appears, running or idle |
| `tool_list` | list tools from a running plugin's MCP server (or across all running servers when the server is omitted) |
| `tool_search` | search a running MCP server's tools by name or description |
| `tool_schema` | load one MCP tool's input schema by server and tool name before calling it |
| `mcp_register` | copy a plugin from an absolute staging folder outside the installed plugins root; check inventory and ask before replacing an existing id |
| `mcp_enable` | connect an installed plugin and load its MCP tools; returns tool names + descriptions — call tools directly, no `tool_list` needed |
| `mcp_disable` | stop a plugin without uninstalling it |
| `mcp_unregister` | permanently delete an installed plugin; ask first and use `mcp_disable` when it only needs to stop |
| `mcp_install` | install a plugin from the curated catalog or GitHub |
| `mcp_server_add` | register a manual MCP server from command, arguments, and environment entries |
| `read_image` | load an image from the conversation into the model's context (vision models see it directly; non-vision models get a text description via the vision fallback) |
| `read_audio` | load an audio file from the conversation into the model's context (audio-capable models hear it directly; non-audio models get a text transcript via the audio fallback) |
| `read_video` | load a video file from the conversation into the model's context (video-capable models see it directly; non-video models get a text description via the video fallback) |
| `web_search` | search the web across Brave, Startpage, Wikipedia, and GitHub; returns ranked results with title, URL, and snippet |
| `web_fetch` | fetch a URL and return readable text; supports HTML, JSON (pretty-printed), XML/RSS/Atom, Markdown, CSV, and plain text with newlines preserved; collects links and selected response headers; honors `max_bytes`; surfaces `Retry-After` on 429/503 and structured JSON error bodies |
| `web_answer` | get a web-grounded answer via an LLM with built-in web search (only available when an answer-provider API key is configured) |
| `artifact_create` | create an interactive HTML/CSS/JS artifact rendered in a sandboxed iframe in the UI (prototypes, minigames, dashboards, visualizations); external resources and CDNs allowed |
| `artifact_update` | partial update of an existing artifact by id (only the fields you pass are replaced) |
| `artifact_read` | read an existing artifact's full content by id |
| `artifact_list` | list artifacts in the current conversation |
| `artifact_delete` | delete an artifact by id |
| `ci_pipeline_list` | list `.nusashell/pipeline.yaml` jobs in a workspace |
| `ci_pipeline_read` | read and validate the workspace pipeline |
| `ci_pipeline_validate` | validate pipeline/workflow YAML (INVALID vs BLOCKED) |
| `ci_run` | start a workspace pipeline or a saved automation; set `async: true` to return immediately with a `run_id` while the pipeline runs in the background |
| `ci_wait` | block until a run reaches a terminal state or the timeout expires; use after `ci_run` with `async: true` |
| `ci_run_status` | DAG summary and status; use this after `ci_run` |
| `ci_logs` | job log tail (prefer failed jobs) |
| `ci_cancel` | cancel a run |
| `ci_steer` | send additional instructions to a running pipeline agent step |
| `automation_list` | saved automations with availability |
| `automation_read` | one automation plus capability bindings |
| `automation_validate` | validate YAML |
| `automation_create` | persist a once/every/when/manual workflow (NusaShell owns the schedule) |
| `automation_enable` / `automation_disable` | lifecycle without deleting |
| `automation_status` | inspect waiting/blocked runs |
| `schedule_once` | one-shot RFC3339 automation |
| `schedule_every` | cron or interval automation (not equivalent) |
| `wait_until` | durable wait; the runner is not occupied |
| `sleep` | pause 1–300 seconds; use for retry backoff or between polls of an async `ci_run` |
| `subagent` | spawn 1–6 ACP coding-agent sessions (only listed when at least one ACP agent is enabled in Providers; never listed for pipeline `agent:` steps) |
| `subagent_steer` | queue an extra instruction on a live ACP run |
| `subagent_stop` | cancel a live ACP run (pending permissions fail closed) |
| `subagent_wait` | wait for an async ACP run to finish |

The provider receives the current `Toolbox.ListTools` roster. `web_answer` is
listed only when configured, while `subagent`, `subagent_steer`, `subagent_stop`,
and `subagent_wait` are listed only when an ACP agent is enabled.

## Workflow routing

Use the smallest sufficient workflow. One-step answers and lookups do not need
a TODO. Multi-step, asynchronous, or cross-turn work does.

Good examples:

    docs_read(id="automation")
    skill_read(name="release-checklist")
    web_search(query="current Go release notes")
    web_fetch(url="<URL selected from web_search>")
    ask_question(question="Which deployment target should I use?", options=[...])

Bad examples:

    todo(items=[{"id":"1","content":"Define MCP","status":"in_progress"}])
    skill_list()
    web_fetch(url="<guessed URL>")

Use the hydrated skill and tool catalogs before repeating a list call. If the
user says skills or plugin state changed, refresh with `skill_search` or
`mcp_list`. Built-in tool schemas come from `tools[]`; the hydration catalog
contains only names and descriptions. MCP schemas come from `tool_schema`.

## Output format

All built-in tools return output in **YAML×Markdown** format: a YAML front
matter block delimited by `---` lines (structured fields like `count`,
`status`, `items`) followed by an optional markdown body for content-heavy
results (file contents, page text, descriptions). This is consistent across
all 46+ built-in tools — no JSON or ad-hoc plain text. MCP plugin tools
(`mcp__<server>__<tool>`) return whatever the MCP server produces (format
depends on the server).

Example:
```
---
count: 2
status: ok
---

First result line
Second result line
```
MCP plugin tools (`mcp__<server>__<tool>`) are NOT advertised in the tool
list — the tool list must stay stable for the lifetime of a conversation so
the provider prompt cache (OpenAI / Claude) is not invalidated. The agent
can still discover MCP tools via `tool_list` / `tool_search` / `tool_schema`
and call them by name (`mcp__<server>__<tool>`); execution validates against
the connected MCP server at call time.
When at least one ACP agent is enabled, the interactive toolbox also advertises
`subagent`, `subagent_steer`, `subagent_stop`, and `subagent_wait`. ACP agents
do not receive this conversation, NusaShell MCP plugins, or shell meta-tools.
Pipeline `agent:` steps hide those four tools entirely so a fail-closed
permission prompt cannot stall an unattended run.

## ACP subagents

`subagent` fans a self-contained brief out to 1–6 parallel ACP sessions
(process-wide cap 8 live runs). Pass absolute paths. `workspace` overrides the
conversation workspace for new spawns only — an already-running session keeps
the directory it started with. The tool is always async: it returns run ids
with `status: "starting"` immediately and the parent agent is free to
continue other work. When a subagent finishes, the tool call is updated
with the subagent's text summary and a new parent-agent turn is triggered
(tool injection) so the parent processes the result without a user
message. While any subagent is running, the parent's auto-continue chain
pauses with reason `awaiting-background-jobs`. Completed run transcripts
are persisted to `acp_runs.jsonl`. Permissions are auto-allowed
(orchestrator delegates authority). The user can peek the transcript
from the Agent dock / drawer / popup. Unattended pipeline agents never
see these tools.

## Native web research (searchwire)

NusaShell ships with built-in web research tools powered by
[searchwire](https://github.com/jahrulnr/searchwire). These are native tools,
not MCP plugins — they work with zero configuration and no installed plugin.

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

Use web research for material factual claims that are current, disputed,
consequential, unfamiliar, or likely to have changed. Prefer official or primary
sources and cross-check unstable claims. Do not browse for pure arithmetic,
logic, fictional premises, or facts already observed through an authoritative
local tool.

Good example:

    web_search(query="official release notes for the detected framework version")
    web_fetch(url="<official result selected from web_search>")
    web_answer(question="Synthesize the verified sources and their trade-offs")

`web_answer` is optional and follows source discovery. It does not replace
`web_fetch` for consequential claims.

Bad examples:

    web_fetch(url="<guessed URL>")
    web_answer(question="Is the user's assumption definitely true?")

When evidence remains incomplete, report the claim as unverified and separate
observed or sourced facts from assumptions and inferences.

## MCP tools

The agent-facing tool names keep the `mcp_*` prefix even though the data
model is now "plugins": the tools genuinely come from each plugin's MCP
server, and the shell still connects to that server to fetch them. Every
plugin exposes its MCP tools as `mcp__<server>__<tool>`
with the server's own input schema. The shell connects to the server on
first use (stdio) and keeps the connection for the process lifetime;
re-saving or deleting the server drops the connection.

### MCP tool discovery workflow

MCP tools are not advertised in `tools[]`. Discover, check the schema, then
call by name — never guess a tool name or schema.

Good example:

    tool_list(server="files")                    # → [{name: "read", …}, {name: "write", …}]
    tool_schema(server="files", tool="read")     # exact argument shape
    mcp__files__read({path: "/home/user/a.txt"})

Bad examples:

    mcp__files__read_file({path: "a.txt"})       # guessed name + relative path

    mcp__files__read({path: "a.txt"})            # skipped discovery, relative path

    tool_list()                                   # called every round to re-check
