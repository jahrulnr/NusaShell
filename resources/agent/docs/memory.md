# Memory

Memory stores short facts the agent can recall across conversations. Entries
are append-only JSONL (`memories.jsonl`) with optional tags; search is a
simple case-insensitive substring match over content and tags.

Each entry carries:

- `id` — stable identifier (`mem_<ulid>`).
- `content` — the fact text.
- `tags` — optional free-form tags for filtering.
- `source` — who wrote it: `user` (via the UI), `agent` (via `memory_save`), or `system`.
- `created_at` — UTC RFC3339 timestamp.

## Tools

- `memory_save` — store a fact (`content`, optional `tags`). The entry is
  recorded with `source: "agent"` and the current timestamp.
- `memory_search` — find entries (`query`, optional `limit`, default 10).
  Results are newest-first and include id, created_at, source, tags, and
  content so the agent can reason about recency and provenance.
- `memory_list` — list all entries newest-first with full metadata.
- `memory_delete` — remove an entry by id.

Use memory for stable facts (preferences, decisions, environment details)
rather than transient chat content. There are no embeddings: search quality
depends on the wording of the query and the stored text.
