# OpenAI — Model selection

**Snapshot: 2026-09.** Model catalogs change monthly — always verify live:

```bash
curl https://api.openai.com/v1/models -H "Authorization: Bearer $OPENAI_API_KEY"
```

## The models API is minimal

`GET /v1/models` returns only `{id, object: "model", created, owned_by}` per
entry — **no pricing, context window, modalities, or supported parameters**.
There is no server-side filtering (no `?supported_parameters=` or modality
query params like OpenRouter) and no per-provider performance data (single
provider). Capability discovery lives in the docs, not the API:

- Pricing/context/modalities → the model docs pages (developers.openai.com),
  not the API.
- `owned_by` distinguishes `openai` vs `system` vs org-owned (fine-tuned)
  models — org-owned entries are your fine-tunes.
- Fine-tuning limits are queryable: `GET /v1/fine_tuning/model_limits`.
- For API-based model comparison (pricing/context/params), OpenRouter's
  `/models` is richer — see `_shared/provider-matrix.md`.

## Model-type mapping (manual)

Because `/models` exposes no modality/type field, **you must classify models
yourself** by id naming convention before picking an API surface. Sending an
embedding model to chat (or vice versa) is a 404/400, not a graceful error.

| Id pattern | Type | API surface |
|---|---|---|
| `gpt-*` (no audio/realtime/tts/transcribe marker), `o3*` | LLM (text/chat) | Responses API / Chat Completions |
| `*-codex*` | LLM (coding-tuned) | Responses API |
| `text-embedding-*` | Embeddings | `POST /v1/embeddings` → `embeddings.md` |
| `gpt-image-*`, `dall-e-*` | Image gen | `/v1/images/generations` → `images.md` |
| `gpt-realtime-*`, `gpt-live-transcribe`, `*-realtime-*` | Realtime | WebSocket / WebRTC → `realtime.md` |
| `gpt-audio-*`, `*-audio-preview` | Audio in/out chat | Chat/Responses parts → `multimodal.md` |
| `gpt-4o-mini-tts*`, `tts-1`, `tts-1-hd` | TTS | `/v1/audio/speech` → `tts.md` |
| `gpt-transcribe*`, `*transcribe*`, `whisper-*` | STT | `/v1/audio/transcriptions` → `stt.md` |
| `omni-moderation-*` | Moderation | `/v1/moderations` |
| `sora-*` | Video generation | `/v1/videos` (async, deprecated) → `video.md` |

Mapping rules:

- Match the **most specific** pattern first: `gpt-4o-mini-tts-...` is TTS, not
  a chat LLM, despite the `gpt-` prefix; `gpt-realtime-*` beats the generic
  `gpt-*` LLM rule.
- `transcribe`/`tts`/`realtime`/`embedding`/`image` substrings are the
  reliable discriminators; `gpt-4o-*` alone is ambiguous (chat, audio,
  realtime, and transcribe variants all share the prefix).
- When unsure, verify with a minimal request against the intended endpoint —
  a wrong-type model on the wrong endpoint fails fast (404/400), which is
  also the cheapest probe.
- OpenRouter needs no such mapping (`architecture.output_modalities` in the
  API) — see `_shared/provider-matrix.md`.

## Catalog snapshot

| Model id | Class | Notes |
|---|---|---|
| `gpt-6-astra` | Frontier flagship | Hardest end-to-end work; Responses API |
| `gpt-5.6` / `gpt-5.6-sol` | Flagship | alias `gpt-5.6` routes to `sol` |
| `gpt-5.6-terra` | Balanced | capability/cost middle ground |
| `gpt-5.6-luna` | Cost-efficient | high-volume workloads |
| `gpt-5.5`, `gpt-5.5-pro` | Frontier | pro is Responses-only, heavier compute |
| `gpt-5.4` / `-mini` / `-nano` | Fast tier | mini for subagents, nano for cheap volume |
| `gpt-5.3-chat-latest` | Chat snapshot | ChatGPT Instant-style, both APIs |
| `gpt-5`, `gpt-5-mini`, `gpt-5-nano` | GPT-5 family | reasoning, `effort: minimal` supported |
| `o3-pro` | Deep reasoning | Responses-only |
| `gpt-oss-120b` / `-20b` | Open weights | self-host option |
| `gpt-realtime-2` / `gpt-audio-1.5` | Voice/audio | Realtime API, different contract |
| `gpt-image-2` | Image gen | Images API |
| `text-embedding-3-small` / `-large` | Embeddings | Embeddings API |

## Selection heuristics

- Complex reasoning, agents, coding → flagship tier (`gpt-6-astra`, `gpt-5.6`).
- High-volume, cost-sensitive → `*-luna` / `*-mini` / `*-nano`.
- Simple classification/extraction → nano class + `reasoning.effort: minimal`.
- Voice → realtime family (separate API — do not reuse chat code).
- Structured outputs + tools: verify per-model support before committing.

## Edge cases

- **Aliases** (`gpt-5.6`) silently route to a snapshot — pin the full id
  (`gpt-5.6-sol`) in production so upgrades are deliberate.
- **API-surface exclusivity**: some models are Responses-only (e.g.
  `o3-pro`, `gpt-5.5-pro`); Chat-Completions requests fail or degrade.
- Deprecations carry sunset dates in the model documentation and provider
  announcements; the minimal `/models` response is not a lifecycle-status
  source. Check those sources before pinning.
- Context window and max output differ per model; do not copy limits between
  models — read them from the model entry.
- Reasoning models bill hidden reasoning tokens; compare cost by total usage,
  not visible output length.
- **The API cannot answer capability questions** — pricing, context window,
  and feature support are not in `/models` responses; consult the model docs
  page and verify with a real request before committing.
- Rate limits are per-model and tier-based — a model switch can change your
  effective TPM (see `errors.md`).

## Error handling

Unknown/typo'd model id → 404 `model_not_found`. Retired model → 404 with
message naming the retirement; see `errors.md`.
