# OpenRouter — Analytics (schema + query)

Usage / spend / latency analytics. **Requires a management (provisioning) key**,
not a normal inference key — create one at
[openrouter.ai/settings/management-keys](https://openrouter.ai/settings/management-keys).

Live schema is authoritative: always prefer `GET /api/v1/analytics/meta` (and
[llms.txt](https://openrouter.ai/docs/llms.txt)) over stale examples below.

Upstream:
[analytics-schema](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-analytics-schema),
[analytics-query](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-analytics-query).
Per-request drill-down: [generations.md](generations.md).

## Auth

```
Authorization: Bearer $OPENROUTER_API_KEY   # management key
```

Regular API keys → **403** on analytics endpoints.

## Schema discovery

```bash
curl -sS https://openrouter.ai/api/v1/analytics/meta \
  -H "Authorization: Bearer $OPENROUTER_API_KEY"
```

```json
{
  "data": {
    "metrics": [
      { "name": "request_count", "display_label": "Request Count", "is_rate": false, "display_format": "number" }
    ],
    "dimensions": [{ "name": "model", "display_label": "Model" }],
    "operators": [{ "name": "eq", "value_type": "scalar" }],
    "granularities": [{ "name": "day", "display_label": "Day" }]
  }
}
```

### Metric fields

| Field | Meaning |
|---|---|
| `name` | Use in query `metrics[]` |
| `display_label` | Human label |
| `is_rate` | Ratio/rate (averaged, not summed) |
| `display_format` | `number` \| `currency` \| `percent` \| `latency` \| `throughput` |

### Metric categories (illustrative — confirm via meta)

**Volume (≤365d unless noted):** `request_count`, `tokens_total`, `tokens_prompt`,
`tokens_completion`, `reasoning_tokens`, `cached_tokens`, `byok_request_count`;
31d: `guardrail_invoked_count`, `response_cached_count`.

**Cost:** `total_usage` (= credits + BYOK inference), `byok_usage`,
`credits_usage`, `usage_upstream`, `usage_cache`, `usage_data`, `usage_web`,
`usage_upstream_web` (≤365d); 31d: `openrouter_usage`, `byok_fees`,
`usage_file`, `usage_upstream_file`, `usage_web_fetch`, `usage_upstream_web_fetch`.

**Performance (31d):** `avg_latency`, `p50_latency`, `p90_latency`, `p99_latency`,
`avg_throughput`, `p50_throughput`, `p90_throughput`, `p99_throughput`.

**Efficiency:** `cache_hit_rate`, `guardrail_invoked_rate`, `response_cached_rate`.

### Dimensions

Max **2** dimensions per query.

**Any range:** `model`, `variant`, `api_key_id`, `user`, `workspace`, `app`.

**31d only:** `generation_id`, `provider`, `origin`, `country`, `finish_reason`,
`external_user`, `context_length_bucket`.

**Label-resolved in results** (filter with underlying IDs): `api_key_id` → key
name; `app` → title/URL; `user` → name/email; `workspace` → workspace name.
Empty `user` = org-level / unattributed traffic.

### Operators

| Op | Value | Meaning |
|---|---|---|
| `eq` `neq` `gt` `gte` `lt` `lte` | scalar | Comparisons |
| `in` `not_in` | array | Membership |

### Granularities

| Name | Typical window |
|---|---|
| `minute` | Last hours; **only if** time window ≤ 3 hours |
| `hour` | 1–3 days |
| `day` | Week–months |
| `week` | 3–12 months |
| `month` | Year-scale |

Omit `granularity` for aggregate totals (no time buckets).

### Classifier dimensions / filters

Group/filter by user-defined classifier labels (`classifier_dimensions` /
`classifier_filters`). Classifier must belong to the account. Always forces
**31-day** max range. Classifier filter ops: `eq` `neq` `in` `not_in` only.

### Question → query map

| Question | Metrics | Dimensions | Notes |
|---|---|---|---|
| Spend total | `total_usage` | — | + granularity for trends |
| Costliest models | `total_usage` | `model` | order desc |
| Request count | `request_count` | optional | |
| Fastest provider | `avg_latency`, `p90_latency` | `provider` | 31d |
| Cache hit rate | `cache_hit_rate` | `model` | |
| Key usage | `request_count`, `total_usage` | `api_key_id` | |
| Individual requests | `total_usage` | `generation_id` | 31d → [generations.md](generations.md) |
| BYOK vs credits | `byok_usage`, `credits_usage` | — | |
| Cost breakdown | `usage_upstream`, `usage_cache`, `usage_data` | — | |

### Filter value IDs

| Dimension | Filter with | Source |
|---|---|---|
| `api_key_id` | numeric id or 64-char hash | gen metadata / `GET /api/v1/keys` |
| `user` | Clerk `user_…` | org members — not display name |
| `workspace` | UUID | workspaces API — not name |
| `app` | numeric app id | gen `app_id` — not title |
| `model` | permaslug `author/slug` | `/models` — not display name |

### Schema constraints

- ≤2 dimensions; ≤20 filters; ≤10 classifier dims/filters
- ≤10 000 rows (default limit 1000); `group_limit` 1–10 000
- Volume/cost mostly 365d daily; latency / gen dims / classifiers → 31d
- Rate limit **64 RPM**

## Query execution

```
POST https://openrouter.ai/api/v1/analytics/query
Authorization: Bearer <management-key>
Content-Type: application/json
```

### Positive case

```bash
curl -sS -X POST https://openrouter.ai/api/v1/analytics/query \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "metrics": ["total_usage", "request_count"],
    "dimensions": ["model"],
    "granularity": "day",
    "time_range": {
      "start": "2026-05-13T00:00:00Z",
      "end": "2026-05-20T00:00:00Z"
    },
    "order_by": { "field": "total_usage", "direction": "desc" },
    "limit": 10,
    "group_limit": 7
  }'
```

### Request schema

**Required:** `metrics` (string[], ≥1).

| Field | Type | Default | Notes |
|---|---|---|---|
| `dimensions` | string[] | `[]` | ≤2 |
| `granularity` | string | none | `minute`\|`hour`\|`day`\|`week`\|`month` |
| `time_range` | `{start,end}` | last 7 days | ISO 8601 |
| `filters` | object[] | `[]` | ≤20; AND |
| `order_by` | `{field,direction}` | time desc if granular | field = metric, dimension, or `"date"` |
| `limit` | int | 1000 | 1–10000 |
| `group_limit` | int | auto on time+dims | max rows per dim combo |
| `classifier_dimensions` | object | — | see below |
| `classifier_filters` | object | — | see below |

Filter: `{ "field", "operator", "value" }` — scalar or array for `in`/`not_in`.

Classifier dimensions:

```json
{
  "classifier_id": "<uuid>",
  "dimension_names": ["category"],
  "include_nulls": false
}
```

Classifier filters: `filters[]` with ops `eq`/`neq`/`in`/`not_in` only; 1–10.

### Construction patterns

**Aggregate (no time series):** omit `granularity`.

**Time series:** set `granularity` → rows include `date__<granularity>`.

**Filtered:** multiple filters ANDed.

**Multi-dimension:** up to `["model","provider"]`.

### Response schema

```json
{
  "data": {
    "data": [
      {
        "date__day": "2026-05-19",
        "model": "anthropic/claude-sonnet-4",
        "request_count": "1523",
        "total_usage": 4.27
      }
    ],
    "metadata": {
      "query_time_ms": 142,
      "row_count": 2,
      "truncated": false
    },
    "cachedAt": 1747699200000,
    "warnings": ["Could not resolve api_key_id hash: abc123..."]
  }
}
```

| Field | Notes |
|---|---|
| `data.data` | Rows: metrics + dimensions + optional `date__*` / classifier cols |
| `metadata.truncated` | **Always check** — true ⇒ partial dataset at `limit` |
| `cachedAt` | Present when served from cache |
| `warnings` | Non-fatal (e.g. unresolvable key hash) |

**Types:** count metrics often **strings** (`"1523"`); costs/rates as numbers.
Parse counts before arithmetic.

## Edge cases

- Timeout (408): narrow range; drop latency metrics / `generation_id` / classifiers.
- Unresolvable `api_key_id` hash → sentinel / zero rows + warning, not hard error.
- Always re-check meta before asserting metric/dimension names.

## Errors

| HTTP | Meaning | Action |
|---|---|---|
| 400 | Bad query | Check meta; start &lt; end; ≤2 dims |
| 401 | Bad/missing key | Fix key |
| 403 | Not management key | Create management key |
| 408 | Timeout | Narrow / simplify |
| 429 | 64 RPM | Backoff |
| 500 | Server | Retry |

## Related

- [generations.md](generations.md) — inspect one `generation_id`
- [model-list.md](model-list.md) — permaslugs for filters
- [errors.md](errors.md) — shared API errors
- [README.md](README.md)
