# OpenRouter — Benchmarks

Benchmark-backed model ranking via `GET /api/v1/benchmarks`.
Auth: any valid `OPENROUTER_API_KEY` (not management-only).
Rate limits: ~30 RPM / key, ~500 / day / account.

Ported from `openrouter-benchmarks` (+ `references/benchmarks-api.md`).

For pricing/context/params without rankings → `model-list.md` instead.

## Positive case

```bash
curl -sS 'https://openrouter.ai/api/v1/benchmarks?source=artificial-analysis&task_type=coding&max_results=10' \
  -H "Authorization: Bearer $OPENROUTER_API_KEY"
```

Always preserve `meta.citation`, `meta.source_url`, `meta.as_of` when reporting.

## Query params

| Param | Values | Notes |
|---|---|---|
| `source` | `artificial-analysis`, `design-arena` | Omit = both (different scales — don't merge into one leaderboard) |
| `task_type` | `coding`, `intelligence`, `agentic` | Maps to AA indices / DA categories |
| `arena` | `models`, `builders`, `agents` | Design Arena; default `models` |
| `category` | `codecategories`, `uicomponent`, `gamedev`, `3d`, `dataviz`, `image`, `video`, `svg`, … | Design Arena |
| `max_results` | int ≥ 1 | Cap rows |

## Response shape

```ts
{
  data: Array<ArtificialAnalysisItem | DesignArenaItem>,
  meta: {
    as_of: string,
    citation: string | null,
    model_count: number,
    source: "artificial-analysis" | "design-arena" | null,
    source_url: string | null,
    task_type: string | null,
    version: "v1"
  }
}
```

### Artificial Analysis item

`intelligence_index`, `coding_index`, `agentic_index` (higher better) +
`model_permaslug`, `display_name`, optional `pricing` (per-token strings).

### Design Arena item

`elo`, `win_rate`, `avg_generation_time_ms`, `arena`, `category`,
`tournament_stats` (+ permaslug / pricing). Higher elo/win_rate better;
lower generation time faster.

## Availability gate (mandatory before recommend)

Benchmark `model_permaslug` is for attribution — **not always** a routable
OpenRouter `id`.

1. Exact `id` match on `GET /api/v1/models` → OK candidate.
2. Else if some model has `canonical_slug == permaslug` → family evidence only;
   use that model's real `id` after verification.
3. Check endpoints (`model-list.md`) — prefer ≥1 usable endpoint.
4. Exclude degraded / `uptime_last_30m: 0` / empty endpoints / routing errors
   from primary recommendations; say why.
5. If signals disagree, state ambiguity — don't crown the benchmark leader.

## Interpreting

- Don't blend AA indices and Design Arena ELO into one absolute ranking.
- Pricing ×1e6 for $/MTok display.
- `meta.model_count` can differ from `data.length` when multiple DA categories return.
- **No writing-quality benchmark** today — for prose apps, AA `intelligence_index`
  is a weak proxy; don't claim Design Arena measures writing.

## Edge cases

- Recommending permaslug verbatim in chat → 404 model not found.
- Citing without `meta.citation` / `as_of` → policy miss for republished ranks.
- Creative-writing selection with only Design Arena visual categories → wrong signal.

## Error handling

401 bad key; 429 rate limit — see `errors.md`.

Full field reference: [benchmarks-api.md](benchmarks-api.md)
