# Agent

The Agent workspace runs multi-conversation chat against any configured
provider model. Each conversation keeps its own history on disk as a JSON
file under `conversations/` in the data directory.

## Turn lifecycle

1. You send a message; the shell appends your message to the conversation.
2. The agent builds a request from the history, the system prompt and the
   live tool list (skills, memory, docs, MCP servers).
3. The provider streams text and tool calls back; each delta is pushed to
   the UI over SSE and WebSocket.
4. Tool calls are executed locally and their results feed the next round,
   up to `max_tool_rounds` per turn (default 8, configurable in Settings).
5. The finished assistant message (content, tool calls, usage) is saved to
   the conversation file.

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
