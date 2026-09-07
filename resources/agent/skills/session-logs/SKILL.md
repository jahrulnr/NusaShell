---
name: session-logs
description: Search and analyze your own session logs (older, parent, or compacted conversations) with jq: extract user/assistant messages, search keywords across sessions, count tokens/cost, breakdown tool usage. Use when the user references older, parent, or compacted conversations, asks "what did we say before", wants to audit usage/tokens/cost of past sessions, or wants to search conversation history that is not in memory files.
metadata:
  source: "openclaw skills/session-logs (MIT) — adapted to the NusaShell data directory"
  version: "2"
---

# Session logs

Search and analyze your complete conversation history stored in session files. Use when the user references older/parent conversations, asks "what did we say before", or wants to audit usage/cost/tokens of past sessions — things not stored in memory files.

## Location

NusaShell session logs live under the runtime data directory (platform-dependent — never hardcode one OS path). Verify with `file_list`: Linux: `~/.config/nusashell`; macOS: `~/Library/Application Support/nusashell`; Windows: `%APPDATA%\nusashell`. The layout can change between versions; typical pattern: one JSONL file per conversation (one JSON object per line) plus an index mapping conversation IDs to paths.

**Step 0 — learn the structure first.** Before any query: `file_list` the data directory, read one line from a session file (`exec: head -1 <file>`), and note the fields. Do not assume the schema matches other ecosystems. Common fields: `role` (user/assistant/tool), `content` (string or blocks), `timestamp`, and usage metadata (`tokens`, `cost`, `model`).

If fields differ, adapt the queries below (replace `.role`, `.content`, `.usage` with the actual paths).

## Common queries (example simple JSONL schema)

Example fields: `{"role": "user"|"assistant"|"tool", "content": "..." | [{"type":"text","text":"..."}], "timestamp": "...", "usage": {...}}`. If content is a block array, filter text: `select(.type == "text")`.

### Extract user messages from one session

```bash
jq -r 'select(.role == "user") | (.content | if type == "string" then . else [.[]? | select(.type == "text") | .text] | join("\n") end)' <session>.jsonl
```

### Search a keyword in assistant responses

```bash
jq -r 'select(.role == "assistant") | (.content | if type == "string" then . else [.[]? | select(.type == "text") | .text] | join("\n") end)' <session>.jsonl | rg -i "keyword"
```

### Stats for one session

```bash
jq -s '{
  messages: length,
  user: [.[] | select(.role == "user")] | length,
  assistant: [.[] | select(.role == "assistant")] | length,
  first: .[0].timestamp,
  last: .[-1].timestamp
}' <session>.jsonl
```

### Total cost / tokens per session (adapt the usage field paths)

```bash
jq -s '[.[] | (.usage.cost // 0)] | add' <session>.jsonl
jq -s '[.[] | (.usage.total_tokens // .usage.tokens // 0)] | add' <session>.jsonl
```

### Tool usage breakdown

```bash
jq -r 'select(.role == "tool") | (.name // .tool // "?")' <session>.jsonl | sort | uniq -c | sort -rn
```

### Search across ALL sessions

```bash
rg -l "keyword" <data-dir>/*.jsonl 2>/dev/null
```

If archived/reset variants exist (e.g. files with suffixes like `.reset.*` or `.compacted.*`), include them too — they still contain real conversation content. Check the naming with `file_list` first.

## Tips

- Session logs are append-only JSONL; large sessions can be several MB — sample with `head`/`tail` first.
- Conversations that were compacted/reset are often archived to separate files, not deleted — still searchable.
- `head -1` per file to check date/structure is far cheaper than loading the whole file into context; use jq to project only the fields you need instead of reading full transcripts into the prompt.
- For long spans (all sessions in a month): project `(timestamp, role, preview)` first and summarize, rather than concatenating full transcripts.