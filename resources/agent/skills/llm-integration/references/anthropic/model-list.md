# Anthropic — Models (discovery / naming / snapshot)

## Discovery endpoint

`GET https://api.anthropic.com/v1/models`

```bash
curl https://api.anthropic.com/v1/models \
  -H 'anthropic-version: 2023-06-01' \
  -H "x-api-key: $ANTHROPIC_API_KEY"
```

- Pagination: `limit` (1–1000, default 20) + `after_id` / `before_id` cursors;
  response carries `has_more`, `first_id`, `last_id` (not page tokens).
- Newest models first.

High-signal response fields (per `data[]` entry):

| Field | Meaning |
|---|---|
| `id`, `display_name` | Model id / human name |
| `max_input_tokens` | Context window |
| `max_tokens` | Max `max_tokens` value (sync Messages) |
| `capabilities.thinking.types.{adaptive,enabled}` | Which thinking configs work |
| `capabilities.effort.{low…max}` | Supported effort levels |
| `capabilities.image_input` / `pdf_input` | Modality support |
| `capabilities.structured_outputs` | `output_config.format` / strict tools |
| `capabilities.batch` / `code_execution` / `citations` | Feature flags |

Query it when unsure instead of guessing from marketing names.

## Current lineup (snapshot 2026-09-10 — verify live)

| Model | API id | Context | Max out | Price in/out (MTok) | Thinking |
|---|---|---|---|---|---|
| Fable 5.1 | `claude-fable-5-1` | 1M | 128k | $10 / $50 | adaptive, always on |
| Mythos 5.1 | `claude-mythos-5-1` | — | 128k | $10 / $50 | adaptive, always on (limited-availability program) |
| Opus 5 | `claude-opus-5` | 1M | 128k | $5 / $25 | adaptive (default on) |
| Sonnet 5 | `claude-sonnet-5` | 1M | 128k | $2 / $10 | adaptive (default on, can disable) |
| Haiku 4.5 | `claude-haiku-4-5` (+ dated) | 200k | 64k | $1 / $5 | extended only, no interleaving |
| Opus 4.8 / 4.7 / 4.6 / 4.5 | `claude-opus-4-8`, … | — | 128k / 64k (4.5) | $5 / $25 | adaptive opt-in on ≥4.7; `enabled` on ≤4.6 |
| Sonnet 4.6 / 4.5 | `claude-sonnet-4-6`, `claude-sonnet-4-5` | 1M / 200k | 128k / 64k | $3 / $15 | mixed (see docs) |
| Fable 5 / Mythos 5 / Mythos Preview | `claude-fable-5`, … | — | 128k | $10 / $50 | adaptive, always on |

Notes:

- Batch API = 50% off; cache reads ≈ 10% (2.5% on Fable 5.1 / Mythos 5.1).
- `effort` defaults to `high` on current models; Haiku 4.5 doesn't support it.
- Output ceilings: 128k sync (300k on batches for Opus/Sonnet 5/4.6+ with the
  `output-300k-2026-03-24` beta); Haiku/Sonnet·Opus 4.5 cap at 64k.
- Retirements are announced per model (e.g. Haiku 4.5 no sooner than
  2026-10-15) — check the deprecations page before pinning.

## Naming & versioning rules

- Every id is a **pinned snapshot**. From the 4.6 generation on, dateless ids
  are snapshots themselves (no moving aliases); before 4.6, an alias resolved
  to a dated id like `claude-sonnet-4-5-20250929`.
- Prefer explicit current ids in production; verify availability before shipping.
- Platform id mapping (same model, different id):

| Platform | Pattern | Example |
|---|---|---|
| Claude API | `claude-…` | `claude-opus-5` |
| Amazon Bedrock | `anthropic.claude-…` | `anthropic.claude-opus-5` |
| Google Cloud | `claude-…` (+ `@date`) | `claude-haiku-4-5@20251001` |
| Microsoft Foundry | deployment name (defaults to Claude id) | `claude-sonnet-5` |
| Claude Platform on AWS | Claude ids (dateless form) | `claude-sonnet-5` |

## Selection guidance

- Default workhorse → **Opus 5**; demanding reasoning / long-horizon agents →
  **Fable 5.1**; speed+cost balance → **Sonnet 5**; cheapest fast tier →
  **Haiku 4.5**.
- Agent loops: prefer a model with `adaptive` thinking (+ interleaved) — Haiku
  can't interleave reasoning between tool calls.
- Cost-sensitive volume: Haiku 4.5 + batch, or cache everything reusable.

Snapshot — re-verify against `GET /v1/models` and the models overview before
finalizing choices. Pricing and ids drift monthly.
