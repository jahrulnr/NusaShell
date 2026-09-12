# OpenRouter — Errors, retries, credits

## Error envelope

Often OpenAI-like:

```json
{ "error": { "message": "...", "code": ..., "metadata": { } } }
```

Image/STT validation may return Zod-shaped bodies:
`{"success":false,"error":{"name":"ZodError","message":"[...]"}}` — parse nested
`message` for the bad path.

| HTTP | Meaning | Action |
|---|---|---|
| 400 | Bad request / unsupported param / Zod | Fix body — never blind-retry |
| 401 | Bad key | Fix credentials |
| 402 | **Insufficient credits** | Top up — not a pacing 429 |
| 403 | Forbidden (e.g. analytics without management key; generation not yours) | Fix key type / ownership |
| 404 | Model/route/generation not found | Fix slug / id |
| 408 | Analytics / long query timeout | Narrow range |
| 413 | Payload too large (STT base64) | Compress/trim |
| 429 | Rate limit (key/model/free tier) | Backoff; honor `Retry-After` within the configured wait budget |
| 5xx | Upstream / OpenRouter | Backoff + retry; consider `models[]` fallback |

## Fallbacks

`models: ["primary", "backup1", "backup2"]` + `provider` prefs reduce blast
radius of single-provider outages. Inspect `provider_responses` on the
generation afterward (`generations.md`).

## Credits vs rate limits

- **402** = billing/credits — retrying never helps.
- **429** = rate — backoff; free `:free` models have tight daily caps.

## Retry policy

Same as the skill's cross-provider rules: retry 408/429/5xx/network only; cap
`Retry-After`; discard failed attempt side effects before retry.

## Related

Provider differences → `../references/_shared/provider-matrix.md`.
OpenAI tier limits → `../references/openai/errors.md`.
