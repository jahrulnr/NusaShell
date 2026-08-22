# Agent tools

The agent ships with a built-in toolbox plus one tool per MCP server tool.

## Built-ins

| Tool | Purpose |
| --- | --- |
| `skill_list` | list available skills (name + description + owned_by + shadowed) |
| `skill_search` | search installed skills by name or description; results ranked by relevance (BM25 + related-skill expansion, no embedding) |
| `skill_read` | read a skill's `SKILL.md` (or a support file via `path`) by name; paginated with `offset`/`max_chars` |
| `skill_files` | list the files inside a skill folder (path, type, size, editable) |
| `skill_save` | create or update a skill (omit `id` to create new; pass `id` to update), or write a support file inside an existing skill (pass `path` like `references/errors.md`, `templates/config.yaml`, `scripts/verify.sh`; skill must already exist; plugin-owned skills are read-only) |
| `memory_save` | save a fact as a searchable fragment (unlimited archive); pick a category (`project`, `user`, `task`, `general`) + optional `project`, `task`, `tags` |
| `memory_replace` | update memory: `target="primary"` + `old_text` (substring) to edit the primary document, or omit `old_text` to rewrite the entire body; `target="fragment"` + `id` for fragments |
| `memory_search` | BM25 search over fragments with metadata filters (`category`, `project`, `task`, `tags`); returns ranked results with scores |
| `memory_list` | list entries: `target="primary"` for the always-injected document, `target="fragments"` (default) for the archive with optional metadata filters |
| `memory_delete` | delete a fragment by id |
| `todo` | replace the conversation task checklist (full-replace, Claude TodoWrite style; max 50 items, 500 chars each; prefer exactly one `in_progress` at a time). The optional `brief` argument is a living planning document (max ~10k tokens) with required markdown sections `## Objective` and `## Done when`, plus optional `## Findings` and `## Approach` that grow as the task progresses. It stays available through conversation history and is re-injected with the fresh hydration checkpoint after compaction. The user can delete items from the UI — treat deleted items as gone and do not re-add them. |
| `ask_question` | block for a structured user decision; use only when progress genuinely requires a choice or approval |
| `docs_search` | search the product documentation when the page id is unknown; results ranked by relevance |
| `docs_read` | read a documentation page by canonical extensionless id (from `docs_search` results) |
| `mcp_list` | list all plugins (MCP servers) with runtime state: every plugin appears, running or idle |
| `tool_list` | list ALL tools of a running MCP server (no query); accepts plugin id only; returns full tool defs (ref, name, server, description, parameters) — pass the `ref` to `mcp_call` to execute; call after `mcp_enable` to discover tools |
| `tool_schema` | load one MCP tool's full definition as a single JSONL line (name, description, parameters with type/properties/required) before calling it; accepts plugin id only |
| `mcp_search` | universal MCP tool discovery — search running MCP servers by name or description (token match, ranked); returns a `ref` (`<plugin-id>:<tool>`) plus the full `parameters` schema for each match; pass the `ref` to `mcp_call` to execute; always prefer `mcp_search` + `mcp_call` over guessing tool names |
| `mcp_call` | the only MCP tool execution path — run a tool by `ref` (from `mcp_search` or `tool_list`; format `<plugin-id>:<tool>`) with `arguments_json` (a JSON-encoded string of the arguments matching the tool's parameters schema, or a JSON object directly; optional — defaults to `{}` for parameterless tools); returns a `STALE_TOOL_REF` error if the server was disabled/restarted since discovery (re-search and retry) |
| `mcp_register` | copy a plugin from an absolute staging folder outside the installed plugins root; check inventory and ask before replacing an existing id |
| `mcp_enable` | connect an installed plugin so its tools become available; returns only status + tool count — follow with `tool_list` or `mcp_search` to discover tools; returns `already_enabled` if already connected (no reconnect) |
| `mcp_disable` | stop a plugin without uninstalling it |
| `mcp_unregister` | permanently delete an installed plugin; ask first and use `mcp_disable` when it only needs to stop |
| `mcp_install` | install a plugin from the curated catalog or GitHub |
| `mcp_server_add` | register a manual MCP server from command, arguments, and environment entries |
| `read_image` | load an image from the conversation into the model's context (vision models see it directly; non-vision models get a text description via the vision fallback) |
| `generate_image` | generate an image with the configured auxiliary image model (OpenAI Images, OpenRouter Image API, or Codex ChatGPT plan). Only listed when Settings → Image generation is set. The UI displays the print — do not re-render it as Markdown |
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
listed only when configured, `generate_image` is listed only when an image
generation model is set in Settings, while `subagent`, `subagent_steer`,
`subagent_stop`, and `subagent_wait` are listed only when an ACP agent is
enabled.

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

Use the skill catalog before repeating a `skill_list` call. If the
user says skills or plugin state changed, refresh with `skill_search` or
`mcp_list`. The built-in tool catalog always comes from `tools[]`. The MCP schemas and refs come from `mcp_search`.

## Output format

All built-in tools return output in **YAML frontmatter + JSONL body** format:
a YAML front matter block delimited by `---` lines (structured metadata like
`count`, `status`, `query`) followed by zero or more JSONL lines — one JSON
object per line. Each JSONL line is a self-contained record the agent can
parse independently. This is consistent across all built-in tools. MCP tools
called via `mcp_call` return whatever the MCP server produces (format depends
on the server).

Example:
```
---
count: 2
status: ok
---

{"id":"frag_1","category":"user","content":"prefers Indonesian"}
{"id":"frag_2","category":"project","content":"Go + Clean Architecture"}
```
MCP plugin tools are NOT advertised in the tool list — the tool list must
stay stable for the lifetime of a conversation so the provider prompt cache
(OpenAI / Claude) is not invalidated. The agent discovers and calls MCP tools
via the universal `mcp_search` + `mcp_call` pair, which works on every
provider; `mcp__<server>__<tool>` names are not callable:

1. `mcp_search(query="read file")` → returns `{"ref":"nusashell.files:read","parameters":{...}}`
2. `mcp_call(ref="nusashell.files:read", arguments_json="{\"path\":\"/etc/hosts\"}")` → executes the tool

`tool_list` and `tool_schema` return the same `ref`-shaped definitions for
inspection and exact schema lookups.
If `mcp_call` returns `STALE_TOOL_REF`, the server was disabled or restarted
since the search; run `mcp_search` again and retry.
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

## Image generation

`generate_image` is a client-side function tool. The active chat model
orchestrates; the image backend is the auxiliary model from Settings →
Image generation (OpenAI Images, OpenRouter Image API, or a signed-in
Codex ChatGPT plan). It is not listed until that setting is configured.
Codex uses the same function tool over OAuth — do not emit a hosted
`type: "image_generation"` tool.

The UI shows the print as soon as the tool completes. Do not embed the
image again as Markdown, a data URL, or a file link.

Good examples:

    generate_image(prompt="a wooden fishing boat in a night harbor, sea-glass water")

    generate_image(prompt="same boat at dawn, watercolor", referenced_image_paths=["/home/user/.config/nusashell/attachments/conv_1/gen-tc_abc.png"])

Bad examples:

    generate_image(prompt="a cat")
    # then rendering `![cat](data:image/png;base64,...)` in the assistant text

    generate_image(prompt="edit the last image")  # missing referenced_image_paths

    generate_image(prompt="logo", referenced_image_paths=["logo.png"])  # relative path is rejected

Pass absolute `file_path` values from earlier `generate_image` YAML output
or from user attachments. `n` is clamped to 1–4. At most two
`generate_image` calls run at once per process.

## MCP tools

MCP tools come from each plugin's MCP server with the server's own input
schema. Discovery (`mcp_search` / `tool_list`) returns a `ref`
(`<server>:<tool>`, e.g. `Files:exec`) and execution goes through
`mcp_call`. The shell connects to the server on first use (stdio) and keeps
the connection for the process lifetime; re-saving or deleting the server
drops the connection.

### MCP tool discovery workflow

MCP tools are not advertised in `tools[]`. Use the universal `mcp_search` +
`mcp_call` pair to discover and execute — never guess a tool name or schema.

Good example:

    mcp_search(query="read file")                # → {"ref":"nusashell.files:read","name":"read","server":"nusashell.files","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}
    mcp_call(ref="nusashell.files:read", arguments_json="{\"path\": \"/home/user/a.txt\"}")

Bad examples:

    mcp__files__read_file({path: "a.txt"})       # not in tools[], not callable

    mcp_call(ref="nusashell.files:read", arguments_json="{}")  # empty payload — the tool requires path

    tool_list()                                   # called every round to re-check
