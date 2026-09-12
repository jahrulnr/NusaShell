# Gemini — Embeddings

Dedicated embed endpoints (not generateContent).

- Single: `POST /v1beta/models/{model}:embedContent`
- Batch: `POST /v1beta/models/{model}:batchEmbedContents`
- Auth: `x-goog-api-key: $GEMINI_API_KEY`
- Docs: https://ai.google.dev/gemini-api/docs/embeddings

## Positive case

```bash
curl "https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-2:embedContent" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "models/gemini-embedding-2",
    "content": {
      "parts": [{"text": "What is the meaning of life?"}]
    },
    "outputDimensionality": 768
  }'
```

Vector lives in `embedding.values` (float array).

## Models (snapshot)

| Model | Role | Notes |
|---|---|---|
| `gemini-embedding-2` | Latest multimodal embedding | Text+image+video+audio+docs; task hints go **in the prompt**, not `taskType` |
| `gemini-embedding-001` | Text-only | Supports `taskType`; OpenClaw memory path still references this family |

Both support Matryoshka / `outputDimensionality` shortening (e.g. 768, 1536,
3072 — verify allowed dims per model).

### taskType (`gemini-embedding-001` only)

Examples: `RETRIEVAL_QUERY`, `RETRIEVAL_DOCUMENT`, `SEMANTIC_SIMILARITY`,
`CLASSIFICATION`, `CLUSTERING`, … — set for asymmetric retrieval.

### Embedding 2 aggregation

Multiple parts/inputs may produce **one aggregated** vector (unlike
001’s per-string list behavior). Use Batch API when you need many independent
vectors.

## Runtime notes

OpenClaw memory embeddings provider id `gemini` authenticates via the
`google` provider keys. Hermes does **not** ship a first-class Gemini
embedding client in the audited paths — use this REST contract or another
provider.

## Workflow

1. Choose 001 (text+taskType) vs embedding-2 (multimodal / prompt tasks).
2. Normalize dimensions across the corpus — never mix dims in one index.
3. For retrieval, embed queries with the query task form and documents with
   the document form.
4. Batch with size/token caps; one bad input can fail the batch.

## Edge cases

- Empty text → 400.
- `taskType` on embedding-2 → ignore or error — use prompt prefixes instead.
- Do not send OpenAI `{input: "…"}` body shape here.

## Error handling

[errors.md](errors.md).
