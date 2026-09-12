# OpenAI — Embeddings

`POST /v1/embeddings` returns vectors for search, clustering, RAG retrieval,
recommendations, and classification. Auth: `Authorization: Bearer
$OPENAI_API_KEY`.

## Positive case

```bash
curl https://api.openai.com/v1/embeddings \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-3-small",
    "input": "the quick brown fox",
    "encoding_format": "float"
  }'
```

Success shape:

```json
{
  "object": "list",
  "data": [
    { "object": "embedding", "index": 0, "embedding": [0.01, -0.02, "..."] }
  ],
  "model": "text-embedding-3-small",
  "usage": { "prompt_tokens": 4, "total_tokens": 4 }
}
```

Sort / zip results by `data[].index` when batching — response order should
match, but never assume without checking.

## Request contract

| Field | Required | Notes |
|---|---|---|
| `model` | yes | `text-embedding-3-small` (cheap/default), `text-embedding-3-large` (best quality), legacy `text-embedding-ada-002` |
| `input` | yes | string, array of strings, or token arrays. **Empty string is invalid.** |
| `dimensions` | no | Shorten output dims on `text-embedding-3*` only (e.g. 256, 1024). Default: 1536 (`small`) / 3072 (`large`). |
| `encoding_format` | no | `float` (default) or `base64` |
| `user` | no | End-user tag for abuse tracking |

Limits (enforce client-side):

- Per input: **8192 tokens** (OpenAI embedding models)
- Per request batch: **≤ 2048 inputs** and **≤ ~300,000 tokens** summed
- One oversized input fails the **entire** batch (HTTP 400)

## Response contract

- `data[].embedding`: `float[]` (or base64 blob if requested)
- Vectors are **L2-normalized** (including after `dimensions` shortening) →
  cosine similarity ≡ dot product for ranking
- `usage.prompt_tokens` / `total_tokens` for billing

## Workflow

1. Pick model: `3-small` for most retrieval; `3-large` when quality matters.
2. If the vector DB caps dims, set `dimensions` at request time (preferred
   over truncating manually). Manual truncate ⇒ re-normalize.
3. Batch inputs under token/count caps; truncate/chunk long documents before
   embed (keep chunking strategy stable so re-indexes match).
4. Persist `(model, dimensions)` with the vectors — mixing models/dims in one
   index silently breaks similarity.
5. Track `usage` tokens.

## Edge cases

- **Empty / whitespace-only input** → 400. Filter before calling.
- **Token overflow** → 400 with a message that often includes actual vs max
  counts. Shrink that input (and optionally retry the batch); do not retry
  unchanged.
- **`dimensions` on ada-002** → 400 / ignored incorrectly — only `3*`.
- **Mixing dimensions in one collection** → garbage nearest-neighbor results.
  Treat `(model, dimensions)` as part of the index schema.
- **Assuming list order without `index`** — always order by `index` when
  mapping back to inputs.
- Compatible gateways (OpenRouter, local servers) may use smaller context
  caps (512–2k). Discover the real max; a conservative pre-trim avoids
  failing whole batches.
- Base64 encoding reduces JSON size but you must decode to floats before
  similarity math.

## Error handling

400 = empty/overflow/bad dims — fix inputs; 401/403 auth; 429 TPM/RPM — see
`errors.md`. Embeddings are usually safe to retry on 429/5xx **only if** the
caller can accept duplicate work (indexing pipelines should be idempotent by
document id).
