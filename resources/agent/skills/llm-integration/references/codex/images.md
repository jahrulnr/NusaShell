# Codex / ChatGPT — Image generation & edits

Dedicated image endpoints on the **Codex provider base URL** (not chat/Responses).
Auth and host follow `chatgpt-backend.md`; request/response shapes differ from
platform Image API in `../openai/images.md`.

Sources: [endpoint/images.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/images.rs).

**Hosts**

| Auth | Base | Headers |
|---|---|---|
| ChatGPT session token | `https://chatgpt.com/backend-api/codex` | `Authorization: Bearer <ChatGPT-token>`, `ChatGPT-Account-ID: <account-id>` |
| OpenAI API key | `https://api.openai.com/v1` | `Authorization: Bearer $OPENAI_API_KEY` |

Clients typically also send Codex `originator` / `User-Agent`. Optional turn
correlation: request header `x-codex-image-turn-id`.

## Positive case — generate

```bash
curl https://chatgpt.com/backend-api/codex/images/generations \
  -H "Authorization: Bearer $CHATGPT_TOKEN" \
  -H "ChatGPT-Account-ID: $CHATGPT_ACCOUNT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-image-1.5",
    "prompt": "a red fox in a field",
    "background": "opaque",
    "quality": "medium",
    "size": "1024x1536"
  }'
```

Decode `data[0].b64_json`. Capture response header `x-codex-imagegen-request-id`
(distinct from `x-request-id`) for support/billing correlation.

## Positive case — edit (JSON data URLs)

```bash
curl https://chatgpt.com/backend-api/codex/images/edits \
  -H "Authorization: Bearer $CHATGPT_TOKEN" \
  -H "ChatGPT-Account-ID: $CHATGPT_ACCOUNT_ID" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-image-1.5",
    "prompt": "add a red hat",
    "images": [{"image_url": "data:image/png;base64,Zm9v"}]
  }'
```

Reference images are **JSON** `images[].image_url` data URLs — not multipart
files (contrast `../openai/images.md` edits).

## Request contract

Both ops: `POST` relative to the provider base → `images/generations` or
`images/edits`. Body is JSON (Codex DTOs in [images.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/images.rs)).

| Field | Generations | Edits | Notes |
|---|---|---|---|
| `prompt` | required | required | Text instruction |
| `model` | required | required | e.g. `gpt-image-1.5`, `gpt-image-2` |
| `images` | — | required | `[{ "image_url": "data:<mime>;base64,..." }]` |
| `background` | optional | optional | `transparent` \| `opaque` \| `auto` |
| `quality` | optional | optional | `low` \| `medium` \| `high` \| `auto` |
| `size` | optional | optional | e.g. `1024x1024`, `1024x1536`, or `auto` |
| `n` | optional | optional | Often omitted; single-image clients clamp to 1 |

No Codex DTO fields for `output_format`, `output_compression`, `response_format`,
or multipart `mask`.

## Response contract

```json
{
  "created": 1778832973,
  "data": [{ "b64_json": "<base64>" }],
  "background": "opaque",
  "quality": "medium",
  "size": "1024x1536",
  "usage": { "total_tokens": 2846 }
}
```

- **`data` is required** — missing `data` fails decode even on HTTP 200.
- Images are always `b64_json` (no URL mode in this surface).
- Wire may include `usage` / `output_format`; typed Codex clients keep
  `created` + `data` (+ optional `background`/`quality`/`size`).
- Response header **`x-codex-imagegen-request-id`**: non-empty value is the
  imagegen correlation id; empty/absent → treat as unset.

## Differences vs `../openai/images.md`

| | Codex / ChatGPT images | Platform OpenAI Image API |
|---|---|---|
| Path prefix | `{codex-base}/images/...` | `/v1/images/...` |
| Auth | ChatGPT-token + account id **or** API key | API key only |
| Edits | JSON + data-URL `images[]` | Multipart `image` / `mask` files |
| Output knobs | background / quality / size (+ `n`) | + `output_format`, compression, DALL·E `response_format` |
| Correlation | `x-codex-imagegen-request-id` | Standard request ids / usage only |
| Chat tool path | N/A here — see Responses `image_generation` on openai refs | Optional Responses tool |

## Edge cases

- **403 on ChatGPT host** without `ChatGPT-Account-ID` / Codex originator metadata — fix auth headers, do not rewrite the body.
- **Empty `x-codex-imagegen-request-id`** is ignored; do not invent an id.
- **`n` omitted or forced to 1** in many clients — each call may be billable; do not assume multi-image batches.
- **Oversized `b64_json`** (~32 MiB decoded cap in Codex executors) — fail before full decode.
- **`usage_limit_reached` 429** — hard failure with optional `resets_at`; do not blind-retry. Generic 429 may still be rate-limit; 5xx retriable.
- **Edits without `images`** or with bare paths (not data URLs) — wrong contract; do not fall back to platform multipart on the Codex base.
- Switching base URL (ChatGPT ↔ `api.openai.com/v1`) keeps the same path suffix but **must** switch auth accordingly (`chatgpt-backend.md`).

## Related

- Auth / ChatGPT backend surface: `chatgpt-backend.md`
- Public OpenAI Image API: `../openai/images.md`
