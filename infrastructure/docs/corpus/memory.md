# Memory

Memory stores short facts the agent can recall across conversations. Entries
are append-only JSONL (`memories.jsonl`) with optional tags; search is a
simple case-insensitive substring match over content and tags.

## Tools

- `memory_save` — store a fact (`content`, optional `tags`).
- `memory_search` — find entries (`query`, optional `limit`, default 10).
- `memory_list` — show all entries.
- `memory_delete` — remove an entry by id.

Use memory for stable facts (preferences, decisions, environment details)
rather than transient chat content. There are no embeddings: search quality
depends on the wording of the query and the stored text.
