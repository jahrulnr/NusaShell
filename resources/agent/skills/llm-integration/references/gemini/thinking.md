# Gemini — Thinking & thought signatures

Thinking is configured under
`generationConfig.thinkingConfig`. Gemini **2.5** and Gemini **3 / 3.1**
use different controls — mixing them → **400**.

Docs: https://ai.google.dev/gemini-api/docs/generate-content/thinking  
Gemini 3 guide: https://ai.google.dev/gemini-api/docs/generate-content/gemini-3

## thinkingConfig by family

| Family | Controls | Notes |
|---|---|---|
| Gemini **2.5** | `thinkingBudget` (int), `includeThoughts` | Budget `0` rejected on some 2.5 Pro builds — omit or use dynamic. Adaptive ≈ `thinkingBudget: -1` (OpenClaw). Do **not** send `thinkingLevel`. |
| Gemini **3 / 3.1** (+ many `*-latest`) | `thinkingLevel`, `includeThoughts` | Levels: `minimal` \| `low` \| `medium` \| `high` (support varies; Pro often `low`/`high` only). Do **not** send `thinkingBudget` together with `thinkingLevel`. |
| **Gemma** | — | Omit `thinkingConfig` entirely → 400 if present. |

```json
"generationConfig": {
  "thinkingConfig": {
    "includeThoughts": true,
    "thinkingLevel": "low"
  }
}
```

`includeThoughts: true` surfaces thought summary parts (`"thought": true`)
in the response for debugging/UX. Internal reasoning may still run when
summaries are hidden.

## Thought signatures (multi-turn + tools)

Gemini returns opaque `thoughtSignature` strings on parts (especially
Gemini 3). The API is **stateless** — you must echo signatures back on the
same parts in subsequent `contents`.

| Scenario | Rule |
|---|---|
| Function calling (Gemini 3) | **Required** — missing signature on tool replay → 400 / broken follow-up |
| Image generation / edit turns | **Strict** — missing signatures on model parts → 400 |
| Plain text chat | Strongly recommended; omitting degrades reasoning quality |
| Official SDK + unmodified history | Usually automatic |
| Raw REST / custom history editors | Copy signatures exactly; do not merge/split signed parts |

Hermes stores signatures in OpenAI-shaped side metadata
(`extra_content.google.thought_signature`) and replays them on native
translate. OpenClaw preserves them in the Google transport / `@google/genai`
path. Both strip signatures when falling back to non-Gemini models.

## Mapping from effort enums (agent runtimes)

When a product only exposes coarse effort (`none`/`low`/`medium`/`high`):

- Gemma → send nothing.
- Gemini 2.5 → prefer `includeThoughts` only (avoid inventing budgets).
- Gemini 3 Flash → map to `thinkingLevel` low/medium/high.
- Gemini 3 Pro → clamp to low/high.
- Adaptive / dynamic → omit fixed level (G3) or `thinkingBudget: -1` (G2.5).

## Edge cases

- Sending **both** `thinkingLevel` and `thinkingBudget` → 400.
- Gemini 2.5 Pro + `thinkingBudget: 0` → often 400; strip zero budgets.
- Thought tokens bill under `usageMetadata.thoughtsTokenCount`.
- Do not concatenate two parts that each carry a signature into one part.

## Error handling

Signature / thinking validation failures are **400** — fix the history or
config; never blind-retry. See [errors.md](errors.md).
