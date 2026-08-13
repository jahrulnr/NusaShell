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
   up to 8 tool rounds per turn.
5. The finished assistant message (content, tool calls, usage) is saved to
   the conversation file.

## Compaction

When the estimated token count of a conversation exceeds the configured
threshold (default 40000), the agent summarizes the older history with the
provider and replaces it with a summary marker. Recent messages stay intact.
Compaction can be toggled in settings.

## Prompt caching

For Messages-format providers, the system prompt and tool definitions are marked
with `cache_control: ephemeral`, so repeated turns reuse the cached prefix.
Usage shows `cache_read` tokens when a cache hit happens. OpenAI-compatible
providers have no standard caching knob; the flag is a no-op there.

## Stop

A running turn can be stopped; the partial assistant message is kept and
marked as interrupted.
