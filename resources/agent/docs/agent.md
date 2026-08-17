# Agent

The Agent workspace runs multi-conversation chat against any configured
provider model. Each conversation keeps its own history on disk as a JSON
file under `conversations/` in the data directory.

## Turn lifecycle

1. You send a message; the shell appends your message to the conversation.
2. The agent builds a request from the history, the system prompt and the
   live tool list (skills, memory, docs, MCP servers, CI/automation).
3. The provider streams text and tool calls back; each delta is pushed to
   the UI over SSE and WebSocket.
4. Tool calls are executed locally and their results feed the next round,
   up to `max_tool_rounds` per turn (default 8, configurable in Settings).
   When a round emits several tool calls, they run **concurrently** (bounded,
   up to 6 at once) to cut wall-clock latency; their results are then persisted
   in the model's original tool-call order, and a single follow-up request is
   sent to the provider for the whole round (parallel tools do not add provider
   round-trips). A cancelled turn skips any not-yet-started tools.
5. The finished assistant message (content, tool calls, usage) is saved to
   the conversation file.

### Mermaid diagrams

Fenced ` ```mermaid ` blocks in assistant messages are rendered as diagrams in
the chat. Rendering is deferred to settle points (a finished turn or a
re-rendered thread), never on every streaming delta, so live text keeps
streaming cheaply. Invalid Mermaid (a syntactically wrong diagram from the
model) is detected before rendering and falls back to showing the raw source
with a note — it never breaks the message.

### ask_question restoration

`ask_question` blocks the turn until you answer. If you switch rooms or reload
the page while a question is pending, the interactive card is rebuilt from the
backend (via `agent.ask.pending`) so the turn is never stuck behind a
non-answerable card.

### Switching rooms during a live turn

A conversation is a "room" in the Agent workspace. The run itself lives on
the backend (fire-and-forget), so a turn keeps streaming even after you
switch to another room. To keep the UI stable, the frontend keeps a small
per-room **live buffer**: every delta (text, reasoning, tool calls) that
arrives over the shared WebSocket while a room is not visible is mirrored
in memory on that room's buffer (capped at 512 KiB per room).

- Switching back to a room that is still streaming re-merges its live buffer
  into the thread immediately, so you never see a blank/frozen turn or a
  "content appears only after the turn finishes".
- A turn that **finishes while you are in another room** is shown in the
  sidebar with a small pulsing live dot while it is still active. Once it
  settles, the UI re-reads the authoritative snapshot from the backend, so
  opening the room shows the final persisted content (the turn's full text +
  tool calls), never a blank turn.
- The live buffer covers only in-flight turns (buffers are marked terminal
  when the run finishes and expire shortly after); the persisted snapshot is
  the source of truth once a turn is done.
- Buffers are capped for memory safety; a room that exceeds the cap falls
  back to rendering from the persisted snapshot on switch-back.
- Unlike session-scoped SSE, the shared WebSocket keeps delivering every
  conversation's deltas to the one open page, so this works without extra
  backend polling or workers.

## Task checklist

The `todo` tool replaces the conversation's task checklist in one call
(full-replace, Claude TodoWrite style). Use it to make multi-step work
visible to the user: pending, in_progress, completed. Prefer exactly one
`in_progress` item at a time. The user can delete items from the UI strip —
treat deleted items as gone and do not re-add them on the next call.

## Steer (mid-turn user messages)

A new user message that arrives while a turn is running is a "steer": an
active instruction, not a replacement of the task. The shell queues it and
injects it at the next safe tool-round boundary, then re-injects runtime
hydration so the model sees fresh context. Answer the user, weigh their
suggestion, then continue the current task — never drop the task merely
because a message arrived. If the user explicitly says "stop" or an
equivalent halt, stop the turn and preserve any unfinished work. The
frontend shows the queued steer in a strip with a Cancel button; only one
steer can be queued at a time.

## ACP subagents

ACP coding agents are spawn-only. The user never chats with them in the
composer. When the parent agent calls `subagent`, a dock appears above the
composer: chips for every live run (and recent finishes in this room). Click
a chip for the right-hand drawer (all parallel spawns), or peek one run in a
popup. Both surfaces stream the transcript and offer steer, stop, mode
change, and risk promotion (edits / bypass). Permission prompts use a global
overlay — Allow once, Allow for this session, or Deny. Timeout denies.
`edit_confirmed` auto-allows edit/delete/move only when every path stays
inside the bound workspace; slash-rooted paths (`/etc/passwd`, `\Windows\…`)
are treated as absolute even on Windows and never join onto the workspace.
Existing runs keep the workspace they bound at spawn; new spawns follow the
current conversation workspace unless the tool overrides it.

Stdio framing is newline-delimited JSON-RPC. Do not expect LSP
`Content-Length` headers; Gemini CLI rejects them as invalid JSON.

Pipeline `agent:` steps never advertise `subagent` / `subagent_steer` /
`subagent_stop` / `subagent_wait`. Those tools require an interactive
permission overlay; unattended FireDue must not wait on approval.

## Compaction

When the estimated token count of a conversation exceeds the configured
threshold (default 40000), the agent summarizes the older history with the
provider and replaces it with a summary marker. Recent messages stay intact.
Compaction can be toggled in settings.

## Prompt caching

When prompt caching is enabled in settings, the system prompt and tool
definitions are marked with provider-native caching hints: `cache_control:
ephemeral` for Messages, `prompt_cache_key` (and `prompt_cache_options` in
explicit mode) for Responses and Chat, and `prompt_cache_key` plus
session/thread headers for Codex. Repeated turns in the same conversation
reuse the cached prefix. Usage shows `cache_read` (and `cache_write` for
Messages) tokens when a cache hit or write happens.

## Stop

A running turn can be stopped; the partial assistant message is kept and
marked as interrupted.

## Image attachments and vision

Users can attach images (PNG, JPEG, GIF, WebP) to a turn. The agent runtime
checks whether the active chat model supports image input using the `Vision`
capability flag from the model catalog (models.dev / OpenRouter).

- **Vision-capable model:** images are sent directly to the model as
  `image_url` (Chat Completions) or `image` (Messages) content blocks.
- **Non-vision model:** images are stripped from the conversation history
  before sending to the provider. A text placeholder is appended to the
  user message that shows the absolute file path(s) of the stripped image(s)
  and instructs the model to call the `read_image` tool with `file_path` to
  load the image.
  Example: `[image content omitted — this model does not support image
  input. Image file(s): /home/user/.config/nusashell-light/attachments/conv_1/cat.png.
  Call the read_image tool with file_path set to one of the absolute paths
  above to load the image into your context.]`. This prevents provider
  errors when switching from a vision model to a text-only model
  mid-conversation and gives the model an actionable way to access the
  image. Only absolute paths are accepted — relative paths are rejected to
  avoid ambiguity between the model's working directory and the actual file
  location.

### Vision fallback

When the active chat model does not support vision but the user has
configured a **Vision fallback model** in settings (VisionProviderID +
VisionModelID), the agent describes each attached image using the fallback
vision model before the first turn round. The description is injected as a
text attachment on the user message. The original image is preserved so a
later switch to a vision-capable model can still see it.

If no fallback is configured, non-vision models receive the text placeholder
described above.

### read_image tool

The `read_image` tool lets the model request an image from the conversation
on demand. It accepts a `file_path` (the absolute path of an image file on
disk, shown in the image placeholder) and an optional `question`. Relative
paths are rejected — only absolute paths are accepted.

- **Vision-capable model (native fast path):** the image is returned
  directly as a tool result attachment. The provider adapter serializes it
  as an `image_url` content block (Chat Completions) or `image` content
  block (Messages) in the tool result, so the model sees the pixels in the
  next round.
- **Non-vision model + fallback configured:** the image is described using
  the vision fallback model and the text description is returned as the
  tool result.
- **Non-vision model + no fallback:** returns an error message explaining
  that the model cannot see images and no fallback is configured.

The image attachment is preserved on the original user message, so
`read_image` can re-load it even after compaction prunes it from the
visible context window.

## Automation

Durable pipelines and schedules are a separate subsystem (see the Automation
page). Use `ci_*` / `automation_*` / `schedule_*` tools; NusaShell owns the
timer table in `automation.db`. A `waiting` run does not hold a runner.
