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
