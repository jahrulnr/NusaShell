# Providers

Providers are the LLM backends the agent chats through. A provider is
defined by its **API wire format**, not by a vendor: **Messages**,
**Responses**, **Chat**, **Gemini**, or the **Codex** backend.

## Protocols and wire implementation

A provider is configured with a **protocol** (its wire format) plus a Base
URL. The editor's Protocol select offers exactly five choices: **Chat**,
**Responses**, **Messages**, **Gemini**, and **Codex · ChatGPT OAuth**. The
implementation behind each provider is derived from the protocol and Base
URL — there is no separate vendor or driver choice:

- **Chat** always speaks the OpenRouter-compatible profile (reasoning
  object, `reasoning_details`, `cache_retention`, `provider.order`) against
  the configured Base URL — whether that is `openrouter.ai` itself or a
  custom gateway such as TokenRouter, 9Router, one-api, LiteLLM, LM Studio,
  or vLLM. Applying that profile to a custom Base URL keeps the
  endpoint-specific OpenRouter mappings available to unpredictable gateways
  (`video_url` on Chat, `input_video` on delegated Responses, video
  rejected on delegated Messages). The gateway must accept the selected
  request shape — NusaShell does not silently retry the same request
  through vanilla Chat, Responses, or Messages. OpenCode hosts
  (`opencode.ai`) are the one exception: their thinking-mode contract needs
  the vanilla Chat `reasoning_content` replay, so they keep that wire.
- **Messages** and **Responses** use the direct Anthropic/OpenAI wire, or
  the OpenRouter profile when the Base URL is `openrouter.ai`.
- **Gemini** uses Google AI Studio's `generateContent` /
  `streamGenerateContent` wire (`https://generativelanguage.googleapis.com`,
  `x-goog-api-key` auth).
- **Codex** uses the ChatGPT Codex Responses endpoint and ChatGPT OAuth
  (see API keys). Tokens are stored in the local credential store. Codex
  also owns its remote v2 compaction: the compacted checkpoint is kept
  separately, real user turns are retained for the next request, and the
  replaced transcript stays visible for room scroll-back.

The built-in cards remain visible before they are configured. Configure a
card with its base URL and credential, then import models when the provider
supports model listing. The editor supports all five protocols and there is
no custom-provider count limit.

## Kinds

- `messages` — the Anthropic Messages wire format (`/v1/messages`); supports
  prompt caching via `cache_control`.
- `responses` — the OpenAI Responses wire format (`/v1/responses`); supports
  function calling and reports cached input tokens.
- `chat` — the Chat Completions wire format (`/chat/completions`). Every
  Chat provider speaks the OpenRouter compatibility profile against its
  configured Base URL (the gateway must accept that profile), except
  OpenCode hosts, which use the vanilla OpenAI Chat wire
  (`reasoning_effort`, `reasoning_content`, `max_tokens`).
- `gemini` — Google's `generateContent` wire format
  (`POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent`
  with `alt=sse`). Messages become `contents` parts with an optional
  `systemInstruction`; tools become `functionDeclarations` with an
  OpenAPI-subset schema; structured output uses `responseMimeType` +
  `responseSchema`; thinking uses `thinkingConfig` (`thinkingBudget` on
  Gemini 2.x, `thinkingLevel` on Gemini 3). Function calls carry a
  `thoughtSignature` that must be replayed with the call on the next turn.
- `codex` — the ChatGPT Codex Responses wire format
  (`/backend-api/codex/responses`); it uses OAuth access tokens (Sign in /
  Import from CLI; optional paste fallback), Codex session headers, multi-
  account failover with circuit breakers, and the remote v2 compaction flow.
  **Import models** discovers the account-aware catalog via the Codex CLI
  app-server `model/list` using the OAuth account selected in NusaShell —
  not whichever account happens to be active in `~/.codex/auth.json`.
  Explicitly switching accounts also clears sticky conversation bindings,
  so Retry and later turns use the selected account. ChatGPT plan image
  models (`gpt-image-2`, `gpt-image-1.5`) are seeded after import because
  `model/list` omits them. The same OAuth token and base URL also drive the
  Codex image backend used by `generate_media(media_type=image)`
  (`/images/generations` | `/images/edits`). Image calls fail over across
  accounts on a usage-limit 429 or a 403 entitlement rejection — one attempt
  per account, since image
  calls are billable and there is no blind retry; a 403 is reported as a
  likely missing image entitlement (e.g. ChatGPT Free). When the active chat
  provider is Codex, `web_search` tries the Codex `/alpha/search` backend
  first and falls back to searchwire when it fails; results keep the normal
  `web_search` shape.
  Tool results that carry media (e.g. `generate_media` or `read_media`
  outputs) are split on the Codex wire: the `function_call_output` stays
  text-only and the media is reinjected as the next user message with
  `input_image` items — media is never serialized into a text-only tool
  output.

## Streaming completion and request-shape recovery

OpenAI-compatible SSE providers do not all emit the `[DONE]` sentinel, and
some hold the connection open after the last chunk. NusaShell treats
`finish_reason` as the completion signal, not the sentinel:

- A final choice with `finish_reason` completes the stream; the `[DONE]`
  trailer is optional and a clean EOF after it is a normal end.
- A trailing usage-only chunk after `finish_reason` is read as accounting
  but never required — token accounting is best-effort, not a success
  condition.
- A provider that holds the connection open after the last chunk is dropped
  after a one-second drain, so a completed turn never stalls or retries.
- Errors and resets after `finish_reason` are still completions; a cut
  before any `finish_reason` is an incomplete stream and goes through the
  shared retry policy.
- `finish_reason: "error"` and provider error payloads inside the SSE body
  are failures, not empty successful responses.

Some Claude 4.6-compatible gateways reject an assistant prefill when the last
request message has role `assistant`, returning a 400 that says the
conversation must end with a user message. NusaShell learns this constraint
for that provider+model and retries with an ephemeral minimal user turn. The
synthetic turn is never persisted. An existing `tool` result remains the last
message during an active tool cycle, so the normal sequence stays:

```text
user → assistant(tool_calls) → tool(result) → assistant
```

When a Chat-compatible tool returns media, the media is reinjected as a
user-content message because Chat tool messages carry text only. All tool
results for one assistant batch stay contiguous before that reinjection:

```text
good: assistant(tool_calls: read_media, memory) → tool(read_media) → tool(memory) → user(image)
bad:  assistant(tool_calls: read_media, memory) → tool(read_media) → user(image) → tool(memory)
```

The second shape is rejected by providers such as DeepSeek because every
tool_call_id must be answered before another role appears.

Do not classify this 400 as a transient outage — resending the same
assistant-ended request cannot succeed. A 429 without a usable `Retry-After`
is likewise hard-failed; retries are automatic only when the provider
supplied a safe retry window.

## Prompt cache TTL

Settings → **Prompt caching** is the master switch. The **Cache TTL** chips
on a provider's detail pane pick what that provider sends, or turn cache
off for that provider only:

- `messages` (Anthropic `cache_control`): `5m` or `1h`. Default `5m`.
- `responses` (OpenAI `prompt_cache_options.ttl`): `30m`.
- `codex`: `30m` or `off`. The Codex wire only sends `prompt_cache_key` — no
  TTL value is transmitted, so `30m` simply enables the cache key and `off`
  disables prompt caching for the provider.
- `chat` (OpenRouter profile, `cache_control`): `5m` or `1h`. Default `5m`;
  the OpenRouter profile does not accept `30m` as a `cache_control` TTL.
- OpenCode Chat hosts (vanilla Chat wire, `prompt_cache_key` plus
  `prompt_cache_options`): `30m`. Putting 5m/1h on a Chat system breakpoint
  is rejected locally.
- `off` skips prompt-cache markers for this provider even when the Settings
  switch is on.
- `gemini` shows no chips: caching is implicit server-side and there is no
  cache key, block, or TTL on the wire. Cached tokens still appear in usage
  (`cachedContentTokenCount` → cache read).

## Codex thinking summary

The **Thinking summary** chips on a Codex provider's detail pane control the
`reasoning.summary` value sent on later Codex turns, stored per provider:
`auto` (default — Codex picks the detail), `concise`, `detailed`, or `none`.
`none` suppresses the visible summary; it does not disable model reasoning
or encrypted reasoning replay. This option exists only for the `codex` kind
and does not guarantee every turn produces a summary.

## Prompt-cache keys and sessions

When prompt caching is enabled and the provider Cache TTL is not `off`,
NusaShell creates one stable 32-character ASCII key per unchanged
provider/model/conversation request contract. The digest includes the current
system prompt and top-level `tools[]`; a new binary, user instruction, or
runtime tool enable/disable therefore gets a fresh cache/session shard. The
key prefix marks the workload: `nusashell_cv_` for normal conversation
turns, `nusashell_bg_` for headless/background turns.

The key is sent through the wire only where the selected adapter has a useful
path:

- OpenAI Responses and OpenCode vanilla Chat: `prompt_cache_key` in the
  request body.
- Anthropic Messages: none — caching stays native `cache_control`
  breakpoints; the Messages API has no caller-supplied cache-key field.
- OpenRouter-profile Chat, including custom Chat gateways: `prompt_cache_key`
  and `session_id` in the body — reusing the key as `session_id` gives
  OpenRouter-style sticky routing and session grouping where the gateway
  supports it. OpenCode also gets the documented `x-opencode-session` header.
- OpenRouter-profile Messages and Responses: the same session value as the
  `x-session-id` header; Responses also keeps `prompt_cache_key` in the body.

Strict gateways can reject unknown body parameters, so NusaShell sends the
stable key only on each adapter's documented path — no undocumented
cache-key field or extra header is injected into Anthropic or unrelated
gateways.

See [OpenRouter prompt caching](https://openrouter.ai/docs/guides/best-practices/prompt-caching),
[OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching),
[Claude prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching),
[OpenCode Go](https://opencode.ai/docs/go/),
[OpenCode Zen](https://opencode.ai/docs/zen/),
and [LiteLLM input parameters](https://docs.litellm.ai/docs/completion/input).

## OpenCode Zen and Go

OpenCode does not publish a full OpenAPI catalog like OpenAI, Anthropic, or
OpenRouter. The authoritative pages are [Zen](https://opencode.ai/docs/zen/)
and [Go](https://opencode.ai/docs/go/). Add OpenCode as a custom provider and
match **kind + Base URL** to the model endpoint in those tables:

| Kind | Base URL | Typical models |
| --- | --- | --- |
| `chat` | `https://opencode.ai/zen/go/v1` (Go) or `https://opencode.ai/zen/v1` (Zen) | DeepSeek, GLM, Kimi, MiMo, Hy, Omen |
| `messages` | same root; operation is `/v1/messages` | MiniMax, Qwen on Go |
| `responses` | same root; operation is `/v1/responses` | Grok, GPT 5.6 Luna, Muse Spark on Go |

Model lists: `GET https://opencode.ai/zen/go/v1/models` (Go) and
`GET https://opencode.ai/zen/v1/models` (Zen). There is no
`/models/{id}/endpoints` route picker.

OpenCode hosts use the vanilla Chat `reasoning_content` replay rather than
the OpenRouter-style `reasoning` shape. A deployment that accepts a
different request shape surfaces a provider error — there is no retry
through another API kind or wire profile. The `x-opencode-session` header is
still attached from the conversation prompt-cache key. Cache TTL chips are
`5m`, `1h`, or `off`; Console Go rejects `prompt_cache_options.ttl=30m`, so
the OpenRouter cache enum stays selected and the gateway must support those
values.

**Base URL is required for all three kinds.** The UI suggests a per-kind
default (`https://api.anthropic.com` for Messages, `https://api.openai.com/v1`
for Responses and Chat) — replace it with any endpoint or AI gateway that
speaks the format. The operation path is appended to the Base URL
**verbatim**: whatever version your endpoint uses (v1, v4, …) must live in
the Base URL and is never injected. For Messages, a bare compat root
(e.g. `https://open.bigmodel.cn/api/anthropic`) gets the
Anthropic-compatible convention suffix `/v1/messages`, and a full endpoint
pasted as Base URL is used as-is.

AI gateways (TokenRouter, LiteLLM, one-api and similar) can serve the three
formats on one endpoint; configure one provider per format:

```text
Messages  → https://gateway.example.com        (→ /v1/messages)
Responses → https://gateway.example.com/v1     (→ /v1/responses)
Chat      → https://gateway.example.com/v1     (→ /v1/chat/completions)
```

## Gemini (AI Studio)

The Gemini card talks to Google AI Studio with the native Generative Language
wire. Configure it with a Google AI Studio API key; the key is sent as
`x-goog-api-key` (never in the query string, so it cannot leak through
request logs). The default base URL is
`https://generativelanguage.googleapis.com`; a version segment (`/v1beta`) is
appended when the configured base carries none, and a base that already
carries one is used verbatim so Gemini-compatible gateways keep working.

```text
# GOOD — operations used by the wire
POST {base}/v1beta/models/gemini-2.5-flash:generateContent
POST {base}/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse
GET  {base}/v1beta/models?pageSize=200

# BAD — the Gemini wire is not the OpenAI shape
POST {base}/v1beta/openai/chat/completions   (use a `chat` kind provider instead)
```

Model IDs may be written as `gemini-2.5-flash`, `models/gemini-2.5-flash`, or
`gemini/gemini-2.5-flash`; the prefix is stripped because the operation path
already carries the `models` collection. **Import models** lists
`GET /v1beta/models` (paginated through `nextPageToken`, capped at ten pages)
and keeps every entry whose `supportedGenerationMethods` includes
`generateContent`. Image and TTS models that also advertise `generateContent`
(e.g. `gemini-3.1-flash-lite-image`, `gemini-2.5-flash-tts`) are kept and
tagged by kind downstream — the chat picker filters by kind, so they never
appear as chat models.

**Thinking.** `thinkingConfig` is filled from the composer's effort control:

- Gemini 2.x and older take a token budget: `minimal` 1–512 (model specific),
  `low` 1024, `medium` 2048, `high` 4096.
- Gemini 3 takes a level instead, with per-model support (source:
  ai.google.dev/gemini-api/docs/thinking):
  - `gemini-3.8-flash`, `gemini-3.7-flash`: `low`, `medium`, `high`
  - `gemini-3.6-flash`, `gemini-3.5-flash`, `gemini-3.5-flash-lite`,
    `gemini-3.1-flash-lite`, `gemini-3-flash-preview`: `minimal`, `low`,
    `medium`, `high`
  - `gemini-3.1-pro-preview`: `low`, `medium`, `high`
  - `gemini-3-pro-preview`: `low`, `high`
  - `gemini-3.1-flash-lite-image`: `minimal`, `high`
  - Unknown/new Gemini 3 models default to `low`, `medium`, `high` (minimal is
    never assumed valid).
  A requested level that the model does not support is clamped to the nearest
  supported level (the higher one wins on a tie) and reported as a
  `gemini.thinking_level_clamped` warning. A requested budget is replaced by
  the matching level and reported as a `gemini.thinking_budget_unsupported`
  warning.
- `none` disables thinking with a zero budget on 2.x. Gemini 3 cannot fully
  disable thinking, so the lowest supported level for the model is used with
  `includeThoughts: false`.

`includeThoughts` is on for enabled thinking, which is what surfaces the
reasoning stream in the transcript. Reasoning tokens are billed through
`usageMetadata.thoughtsTokenCount`.

**Thought signatures.** Gemini signs tool calls (and sometimes text parts) and
returns `thoughtSignature`. Signatures replay automatically with the call on
the next turn; dropping them degrades or breaks multi-turn reasoning. Gemini
3 rejects a replayed `functionCall` part without a signature, so `gemini-3*`
history that has none for a batch (for example a conversation migrated from
another provider) is backfilled with Google's documented placeholder
sentinel.

**Tools and structured output.** Tool schemas are converted to the OpenAPI
subset Gemini accepts: upper-case types, `items` forced on arrays,
`additionalProperties`/`$schema`/`strict` and other unsupported keywords
dropped, empty `enum` values removed, and `propertyOrdering` added for
`response_format` schemas. `tool_choice` maps onto
`functionToolConfig.functionCallingConfig.mode` (`AUTO`/`ANY`/`NONE`), with
`allowedFunctionNames` for a named tool. Multimodal tool results are nested
inside `functionResponse.parts`. `presence_penalty` and `frequency_penalty`
are dropped on Gemini 3, which rejects them.

**Media.** Bytes you already hold (attachments, `read_media` output) are sent
as `inlineData`; Files API URIs, `gs://` URIs, and public media URLs pass
through as `fileData`. Audio input uses `inlineData` with the normalized
`audio/*` type.

**Image generation.** Gemini image models (the Nano Banana family:
`gemini-3-pro-image`, `gemini-3.1-flash-image`, `gemini-3.1-flash-lite-image`,
legacy `gemini-2.5-flash-image`, and the preview alias
`nano-banana-pro-preview`) generate images through `generateContent` with
`responseModalities: ["TEXT", "IMAGE"]` — not an OpenAI `/images` endpoint.
`generate_media(media_type=image)` routes to this surface
when the configured image model belongs to a `gemini` kind provider. Image
bytes are decoded from `candidates[].content.parts[].inlineData` (base64
`data` + `mimeType`).

```text
# GOOD — generateContent with responseModalities
POST {base}/v1beta/models/gemini-3-pro-image:generateContent
{
  "contents": [{"role": "user", "parts": [{"text": "a watercolor otter"}]}],
  "generationConfig": {"responseModalities": ["TEXT", "IMAGE"]}
}

# GOOD — image-to-image edit: inlineData reference + text prompt in one turn
{
  "contents": [{"role": "user", "parts": [
    {"inlineData": {"mimeType": "image/png", "data": "<base64>"}},
    {"text": "make it night"}
  ]}],
  "generationConfig": {"responseModalities": ["TEXT", "IMAGE"]}
}

# BAD — the Gemini wire is not the OpenAI Images shape
POST {base}/v1beta/images/generations   (use a chat/responses kind provider instead)

# BAD — v1beta2 is 404 live on generateContent; use v1beta
POST {base}/v1beta2/models/gemini-3-pro-image:generateContent   (404)
```

The Nano Banana family accepts reference images for editing
(image-to-image): pass `referenced_image_paths` to `generate_media` and the
images are sent as `inlineData` parts in the same user turn. These models are
tagged `Vision: true` at import and read time so the image-to-image media
gate does not block edits. `imagen-*` is text-to-image only and is not
tagged i2i.

The `size` option from `generate_media` (`1024x1024`, `1536x1024`,
`1024x1536`) is mapped to `generationConfig.imageConfig.aspectRatio`
(`1:1`, `3:2`, `2:3` respectively); `auto` omits the field so the model
picks the default. Explicit pixel size (`1K`/`2K`), `quality`, and
`background` are not supported on this `generateContent` surface — they are
accepted by the tool but ignored by the Gemini backend.

`-image-preview`-style ids (e.g. `gemini-3.0-pro-image-preview`) are not
classified as image models — only the canonical ids above and the AI Studio
alias `nano-banana-pro-preview` are; such an id surfaces as a chat model.

The `gemini` kind has **no** chat TTL chip or embedding endpoint. It does
have an image endpoint (the generateContent surface above), a video endpoint
(the Veo `:predictLongRunning` surface below), and a TTS endpoint (the
generateContent AUDIO surface below). `Test connection` on a Gemini card
lists `GET /v1beta/models`, so the probe costs nothing.

**Video generation (Veo).** Gemini video models (the Veo family:
`veo-3.1-generate-preview`, `veo-3.1-fast-generate-preview`,
`veo-3.1-lite-generate-preview`, `veo-3.0-generate-001`,
`veo-3.0-fast-generate-001`) generate videos through the
`:predictLongRunning` long-running operation surface — not an OpenAI
`/videos` endpoint and not the Interactions API.
`generate_media(media_type=video)` routes to this surface when the
configured video model belongs to a `gemini` kind provider.

```text
# GOOD — :predictLongRunning submit, poll, download
POST {base}/v1beta/models/veo-3.1-generate-preview:predictLongRunning
{
  "instances": [{"prompt": "A cinematic shot of a lion in the savannah."}],
  "parameters": {"durationSeconds": "8", "resolution": "720p"}
}
# Response: {"name": "operations/..."}  ← capture .name as the poll handle

GET  {base}/{operation_name}           ← poll until "done": true
# Success: .response.generateVideoResponse.generatedSamples[0].video.uri

GET  {video_uri}                        ← download with x-goog-api-key, follow redirects

# BAD — the Veo wire is not the OpenAI /videos shape
POST {base}/v1beta/videos              (use a chat/responses kind provider instead)

# BAD — v1beta2 is 404 live on Veo; use v1beta
POST {base}/v1beta2/models/veo-3.1-generate-preview:predictLongRunning   (404)
```

`durationSeconds` accepts `"4"`, `"6"`, `"8"` and **must** be `"8"` for
1080p/4K, reference images, or video extension. `resolution` accepts
`"720p"` (default), `"1080p"`, `"4k"` (4K is not on Lite). The tool's
`480p` has no Veo equivalent — it is omitted so Veo defaults to 720p
rather than rejecting the request. Image-to-video sends the first
reference as `instances[0].image` (inlineData starting frame); additional
references become `referenceImages` with `referenceType: "asset"` (Veo
3.1 only). Provider minimums (e.g. "durationSeconds must be 8 for 1080p")
are surfaced verbatim on rejection.

Generated videos are stored on the server for **2 days** only — download
within that window or lose the artifact. All Veo videos carry a SynthID
watermark. EU/UK/CH/MENA regions force `personGeneration: allow_adult`
(not exposed by this tool). The operation may return `done: true` with an
`error` block instead of a `response` — both are inspected before
assuming success.

`gemini-omni-*` ids appear in the Gemini model list but are **not**
classified as video models — the `:predictLongRunning` surface is documented
for `veo-*` only, so omni ids surface as chat models.

**Speech generation (TTS).** Gemini TTS models (`gemini-3.1-flash-tts-preview`,
`gemini-2.5-flash-preview-tts`, `gemini-2.5-pro-preview-tts`) synthesize
speech through `generateContent` with `responseModalities: ["AUDIO"]` +
`speechConfig.voiceConfig.prebuiltVoiceConfig.voiceName` (single-speaker) —
not an OpenAI `/audio/speech` endpoint.
`generate_media(media_type=speech)` routes to this surface when the
configured TTS model belongs to a `gemini` kind provider. Audio is decoded
from `candidates[].content.parts[].inlineData` (base64 `data` + `mimeType`).

```text
# GOOD — generateContent with responseModalities AUDIO + speechConfig
POST {base}/v1beta/models/gemini-3.1-flash-tts-preview:generateContent
{
  "contents": [{"role": "user", "parts": [{"text": "Say cheerfully: hello."}]}],
  "generationConfig": {
    "responseModalities": ["AUDIO"],
    "speechConfig": {"voiceConfig": {"prebuiltVoiceConfig": {"voiceName": "Kore"}}}
  }
}

# GOOD — decode inlineData (base64 PCM) → wrap into WAV
# candidates[0].content.parts[0].inlineData.mimeType = "audio/L16;rate=24000"
# candidates[0].content.parts[0].inlineData.data = "<base64 PCM>"

# BAD — the Gemini TTS wire is not the OpenAI /audio/speech shape
POST {base}/v1beta/audio/speech   (use a chat/responses kind provider instead)

# BAD — v1beta2 is 404 live on generateContent; use v1beta
POST {base}/v1beta2/models/gemini-3.1-flash-tts-preview:generateContent   (404)

# BAD — using a text (non-TTS) model with responseModalities ["AUDIO"] → 400
POST {base}/v1beta/models/gemini-2.5-flash:generateContent   (400 Invalid value)
```

The inlineData MIME is PCM-family (`audio/L16;rate=24000`, 16-bit, mono by
default). The raw PCM is wrapped into a WAV container so the persisted
artifact is playable. The tool's `mp3` and `opus` formats are **not**
produced by this surface — the artifact is always WAV regardless of the
requested format (documented deviation; never label PCM as mp3). `speed`
has no Gemini TTS equivalent and is ignored on this surface.

The default voice when none is specified is **`Kore`** (the documented
runtime default). Gemini TTS exposes 30 prebuilt voices; the full table is
in the bundled skill reference (`resources/agent/skills/llm-integration/references/gemini/tts.md`).

Multi-speaker (`speechConfig` voice arrays, up to 2 speakers) is not
supported — only single-speaker `prebuiltVoiceConfig`.

Streaming TTS (3.1+) is not used — this surface is the non-streaming
`generateContent` path. TTS may be billable, so 429 responses are surfaced
as hard failures (not retried); 5xx stay retriable for the caller's policy.

## API keys

API keys are optional for `messages`, `responses`, and `chat`; the `gemini`
kind requires a Google AI Studio key (the Generative Language API has no
anonymous tier). The Codex kind authenticates with ChatGPT OAuth: use
**Sign in with ChatGPT** or **Import from Codex CLI** on the Providers →
Codex detail page; pasting an OAuth access token in the edit dialog is a
fallback only. For optional-key kinds, a blank key is stored as no
credential and NusaShell still lists, tests, imports, and chats — the
`Authorization` / `x-api-key` header is skipped and the upstream decides
(e.g. OpenCode, LM Studio, Ollama, Zen free tier). Credentials are stored
in the local credential store inside the data directory, never in the JSON
files.

### Codex accounts, usage, and runtime

On the Codex provider detail page:

- **ChatGPT Accounts** lists signed-in accounts with plan and session/weekly
  usage bars. **Sign in with ChatGPT** uses device-code login: open the displayed verification link on any device, enter the one-time code, and keep NusaShell open while it checks approval. The code expires after 15 minutes; Cancel stops further checks. The existing `ai.codex.login` browser PKCE RPC remains available explicitly. **Import from Codex
  CLI** reads `~/.codex/auth.json`. **Refresh** reloads usage and account
  availability; **Switch** and **Remove** manage accounts.
- Account usage includes used and available percentages for both returned windows, with reset date and time. Period labels follow the supplied duration (5-hour, Weekly, or Monthly); a missing duration uses a neutral window label. Missing usage is shown as unavailable, never as zero usage.
- **Codex Runtime** shows/downloads the managed official Codex CLI binary
  used for account-aware model listing and tooling.
- In the Agent composer, the routing control becomes an account picker for a
  Codex model. **Auto** keeps the sticky quota/cooldown rotation; selecting
  an account strictly pins that room to the chosen credential with no
  failover behind the user's choice. The pin is stored per room (room
  switches and reloads cannot inherit another room's account), the detail
  page's **Switch** sets the account Auto prefers, and follow-up turns after
  delegated work reuse the room's pinned account.

```text
# GOOD — primary auth paths on the Codex detail card
Sign in with ChatGPT
Import from Codex CLI

# BAD — treating paste as the only way to configure Codex
Edit → paste OAuth access token → Save  (fallback only)
```

### Device-code RPC flow

`ai.codex.device.start` accepts `provider_id` and immediately returns
`device_auth_id`, `user_code`, `verification_url`, `interval_seconds`, and
`expires_at` (Unix seconds). `ai.codex.device.poll` accepts the same provider
plus `device_auth_id` and `user_code`. `status=pending` means wait at least
the supplied interval; `status=complete` includes the connected `account_id`.
Only completion writes tokens to the existing active/account credential keys.
Each RPC is bounded to 30 seconds. There is no background poller or callback
listener; callers stop on cancellation, expiry, or error. A fresh attempt
requests a new code. The browser PKCE endpoint is a separate explicit flow,
never an automatic fallback for device authorization errors.

```json
{"method":"ai.codex.device.start","payload":{"provider_id":"codex"}}
{"method":"ai.codex.device.poll","payload":{"provider_id":"codex","device_auth_id":"returned-device-id","user_code":"returned-user-code"}}
```

Good: show the returned link/code, wait `interval_seconds` between completed
polls, stop at `expires_at`, then reload accounts on completion.
Bad: call `ai.codex.login` expecting it to open the phone browser, poll in a
busy loop, or keep polling after the user cancels.

### Seeding keys from the environment (explicit)

Keys normally come from the UI form. For headless or containerized setups,
the binary also offers an **explicit, opt-in** subcommand that copies keys
from environment variables into the credential store:

```bash
nusashell seed-providers
```

It runs **only when invoked** — the server never reads these variables on
its own during normal startup. It is idempotent and non-destructive: it
creates the provider (enabled) on first run and later only rewrites the
stored key when the variable's value changed (secret rotation); name, base
URL, and enabled state edited in the UI are never overwritten. It respects
`NUSASHELL_DATA_DIR`. After seeding, use **Import models** (or the periodic
auto-import) to populate the model list.

| Env variable | Provider | Kind | Base URL |
| --- | --- | --- | --- |
| `OPENROUTER_API_KEY` | OpenRouter | `chat` | `https://openrouter.ai/api/v1` |

## Models

After saving a provider, use **Import models** to fetch its model list
(`GET /models`; the `gemini` kind reads `GET /v1beta/models`). OpenRouter
also exposes `GET /images/models`; those ids are merged and tagged
`kind: image`. The agent only offers imported models.
Messages providers bundle Claude model metadata (context window, pricing);
imported models keep the provider's own ids. Catalog lookup identity is
**gateway/model**, not vendor/model: an OpenCode model imported as
`deepseek-v4.1-flash` resolves as `opencode/deepseek-v4.1-flash`, while an
OpenRouter model imported as `deepseek/deepseek-v4.1-flash` resolves as
`openrouter/deepseek/deepseek-v4.1-flash`. The provider API always receives
the imported model ID unchanged; the gateway namespace is only used to find
catalog metadata. If a chat request is rejected
with an explicit "maximum context length" or "context window" limit, NusaShell
learns that limit for the provider+model and updates the model's effective
context window, so the catalog does not overstate the actual window available
on that gateway. Likewise, if a model 400s because it is text-only or does
not support a modality (vision, audio, video, document), NusaShell learns
that and disables the modality for the provider+model. Future turns apply
these learned overrides at model resolution time, so requests are built with
the real capabilities instead of waiting for another 400.

NusaShell can also learn request-shape constraints, such as a
gateway requiring a user message at the end of the request. These rules are
scoped to provider+model and applied only on a retry after the matching 400;
they do not change the persisted conversation transcript.

Learned overrides are inferred from errors and can be wrong (a false
positive). A **manual override** can be recorded when catalog metadata is
proven wrong for a specific provider+model (e.g. a model marked text-only
that actually served images). Manual overrides survive catalog re-imports
and are applied **after** learned overrides — a manual correction always
wins over both the catalog and an auto-learned value. Precedence: catalog →
learned → manual. For Codex, catalog data wins over CLI discovery.
Embedding-capable models appear in the Embedding model setting (skill and
memory search); embedding requests on `*.openrouter.ai` carry the same
app-attribution headers as chat, other OpenAI-compatible hosts get none.
Models tagged as image generators (`kind: image`, including `gpt-image-*`
and `dall-e-*` even when `/models` omits a kind) appear in Settings →
Image generation and back `generate_media(media_type=image)`. Image
generation uses the
dedicated OpenAI `/images/generations` (and `/images/edits`) endpoints, the
ChatGPT Codex images endpoints on the codex provider base URL
(`.../codex/images/generations` and `.../codex/images/edits` — `gpt-image-2`
and `gpt-image-1.5` are seeded into the codex model list and tagged i2i), or
OpenRouter `POST /images` (JSON body, including `images[].image_url` data
URLs for edits — not OpenAI multipart). The Codex backend sends
`background`/`quality`/`size` with `auto` defaults, always requests a single
image (no `n`), and surfaces image-quota 429s
(`error.type=usage_limit_reached`) as hard usage-limit failures instead of
blind retries. Anthropic Messages is not an
image backend; it can still orchestrate `generate_media` when a supported
image provider is configured.

## Test connection

**Test connection** probes connectivity only: it lists models with
`GET /models` (responses/chat), `GET /v1/models` (messages),
`GET /v1beta/models` (gemini), or the Codex CLI app-server `model/list`
(codex — the first probe may download the managed Codex binary, so allow up
to ~90s), and reports latency and model count. No completion is sent, so
the probe
never costs tokens, works before importing models, and does not trip
model-routing failures on the upstream — a broken model only surfaces when
you actually chat with it.

## ACP subagents

ACP (Agent Client Protocol) agents are **not** chat providers. They never
appear in the Agent composer model picker. You register them in Providers
as a generic subprocess: **command**, **args**, **env**, **transport**, and
a label. Transport is `stdio` (default, local subprocess) or `remote`
(WebSocket URL to a cloud agent). For `remote`, Command holds the WebSocket
URL and Args/Env are ignored.

- Command is immutable after save (delete and recreate to change the binary).
- Probe runs `initialize` and caches advertised auth methods.
- Authenticate uses a method id the agent advertised — nothing is hardcoded
  per vendor.
- Refresh catalog imports modes and models. OpenCode-generation agents
  return v1 `configOptions` instead of legacy `modes`/`models`; select-type
  mode/model options fold into the same catalogs.
- Mode IDs stay vendor-specific; NusaShell maps them onto risk
  tiers (`read_only`, `edit_confirmed`, `bypass`). Unknown modes are
  read-only. New sessions start on the strictest advertised mode. Bypass is
  never the default — promote it from the live subagent UI.
  `edit_confirmed` auto-allows workspace-contained edits; slash-rooted
  paths are absolute (even on Windows) and prompt instead of auto-allow.
  `bypass` on a live local-stdio run may read/write absolute host paths via
  FS callbacks; remote ACP cannot use that escape at any tier.
- Env values stay in `config/acp-agents.json`; read-backs show **keys only**.

Spawn these binaries with the `subagent` tool (optional `count` for parallel
sessions). You can peek, steer, stop, change mode, and answer permission
prompts from the Agent dock, drawer, or popup.

Stdio is newline-delimited JSON-RPC (`\n` between messages, no embedded
newlines, **no** LSP `Content-Length` headers). Logs from the agent belong
on stderr.

### Common CLIs (install + auth)

| CLI | Command | Auth methods (typical) | Not logged in |
| --- | --- | --- | --- |
| Cursor | `curl https://cursor.com/install \| bash` then `agent acp` | `cursor_login` | `initialize` succeeds; `session/new` returns `Authentication required`. Run `agent login` locally, then **Authenticate** with `cursor_login` in Providers. |
| OpenCode | install per [opencode.ai/docs](https://opencode.ai/docs/acp/) → `opencode acp` | `opencode-login` | `initialize` works. Real login is CLI-side (`opencode auth login`) — **Authenticate** with `opencode-login` only validates the method id, it does not log in. Missing provider keys surface as JSON-RPC `-32000` on `session/new` or the first prompt. |

If you register a CLI without logging in, **Probe** still works (it only
runs `initialize`), but **Refresh catalog** and `subagent` spawns fail until
you complete **Authenticate** with one of the advertised method ids —
NusaShell names those ids in the error instead of a bare JSON-RPC
`Authentication required`.

**One login only (lazy auth):** After you **Authenticate** once, NusaShell
does not re-call `authenticate` on subsequent probes, catalog refreshes, or
subagent spawns — it tries `session/new` first and only authenticates when
the agent reports an auth-required error. Agents that persist their own auth
will not re-trigger the browser login flow on every new connection.

## Rate limits and emergency compaction

Providers enforce per-minute budgets: **RPM** (requests per minute) and
**TPM** (tokens per minute). NusaShell treats the failure shapes
differently:

- **Transient rate limit** — the request is modest (`Requested * 2 <=
  Limit`) and other traffic consumed the window, or it is a plain RPM 429.
  The agent waits out the window (honoring `Retry-After` when present) and
  retries.
- **Structural TPM overflow** — one request needs more tokens than the
  entire per-minute budget (`on tokens per min (TPM): Limit 200000,
  Requested 333331`). Waiting can never help: the same request fails in
  every window.
- **Dominant TPM request** — one request needs more than half the
  per-minute budget (`Limit 500000, ... Requested 355391`). It "fits" the
  raw limit, but any partially consumed window blocks it, so retries spin
  uselessly.

Structural and dominant rejections are handled identically: a structural TPM
overflow triggers an **emergency compaction** (the transcript is summarized
down to the compaction budget) and the round retries with the smaller
context — the same safety net that fires on a context-window overflow
returned as HTTP 400 or a Codex SSE error event. The live context badge is a
heuristic estimate until provider usage arrives, so the provider's own
`Limit`/`Used`/`Requested` numbers are trusted as proof of overflow.

A dominant rejection also teaches a durable rule: a learned per-model
context cap prevents repeats — every conversation on that provider+model
then compacts against a smaller window sized to the per-minute budget. This
applies to OpenAI official accounts that report `Limit`/`Requested` numbers;
other gateways' rate-limit text does not parse and behavior is unchanged.

On Responses these rejections arrive mid-stream as an SSE `event: error`
(after HTTP 200), classified as rate-limit errors with an assumed 1-minute
window; on Chat Completions they arrive as HTTP 429. Both surface a message
naming the token numbers (limit, already-used, requested).

## Encrypted reasoning replay

OpenAI Responses, Codex, and some OpenRouter/compat OpenAI routes return
opaque reasoning state (often `encrypted_content` inside a reasoning item)
alongside the visible thinking summary; Anthropic Messages returns thinking
signatures and `redacted_thinking` data instead. NusaShell persists that
payload and reattaches it on the next turn so multi-turn
ChatGPT/Codex/OpenRouter Responses-style and Claude thinking models keep
reasoning continuity; the UI continues to show only plaintext thinking.

Each persisted payload is stamped with the provider family that produced it
(`anthropic`, `openai`, `openrouter`, `gemini`, or `codex`). On the next
turn the payload replays only to a wire that family interprets — a
provider switch never echoes a foreign blob. Messages without a recorded
producer keep the per-kind shape rules below.

Assistant messages also record provenance next to the payload: the
configured provider that served the turn, the gateway-reported upstream
(OpenRouter's top-level `provider` name such as `Nvidia`), and the served
model slug — OpenRouter aliases like `openrouter/auto` or `openrouter/free`
resolve to a different actual model per request, and that slug is stored
separately so the room keeps displaying and re-resolving the requested
model. Replay drops the opaque payload (never the visible thinking text)
when any check fails:

- **Provider instance:** the payload replays only to the NusaShell provider
  that produced it — two credentials on the same family do not share
  encrypted state. Messages captured before this stamp, or requests without
  one, keep replaying.
- **Pinned upstream route:** when the room pins an OpenRouter upstream and
  the payload was produced by a different upstream, it is dropped. Auto
  routing never gates — the serving upstream is only knowable after the
  response.
- **OpenRouter format:** `reasoning_details` items may declare their
  upstream wire family in `format`. A known format that mismatches the
  requested model's vendor prefix is dropped (`anthropic-claude-v1` replays
  only to `anthropic/*` models, `openai-responses-v1`/
  `azure-openai-responses-v1` to `openai/*`, `xai-responses-v1` to
  `x-ai/*`, `google-gemini-v1` to `google/*`, `meta-responses-v1` to
  `meta-llama/*`). Items with `unknown`, no `format`, or an unrecognized
  value replay unchanged, as do extras on routing aliases where no vendor
  can be derived.

- **Chat direct/vanilla OpenAI:** never forwards the opaque state — Chat
  Completions reject signed, redacted, or provider-extra reasoning blocks.
- **Chat OpenRouter profile (including custom gateways):** keeps it only
  when it is a JSON array (`reasoning_details`); Codex encrypted objects are
  stripped.
- **Messages (Anthropic):** forwards Anthropic's own opaque payload
  (thinking signatures and `redacted_thinking` data), so signed thinking
  blocks replay during tool loops. OpenAI-family extras and unsigned
  plaintext reasoning are dropped — an unsigned thinking block would fail
  the request.
- **Responses / Codex:** forwards it unchanged for continuity.

The server-side compaction checkpoint is kind-gated the same way
(Responses/Codex only): a Codex room continued on DeepSeek Chat replays the
post-compaction transcript with plaintext thinking — no encrypted
checkpoint or per-message opaque state — and when the retained suffix holds
no user message a synthetic user nudge keeps a user anchor in the request.

## Server-side compaction (OpenAI Responses)

Large-context OpenAI Responses models (context window >= 200k — the gpt-5.x,
gpt-4.1, and o-series families) compact **server-side and in-stream**: past
the context threshold the server emits an encrypted checkpoint that later
turns replay, continuing the conversation with pruned context. Automatic;
no configuration. A compacted conversation still saves and stays in the
Agent room list even when the retained suffix holds no user message. Models
below the 200k floor and non-OpenAI providers use the client-side
multi-pass summarization path, and the compaction-model setting is skipped
for server-side-eligible chat models — only that model can read its own
encrypted checkpoint.

## Codex remote v2 compaction

Codex compacts through its own backend flow — a separate pre-turn request,
not the OpenAI in-stream compaction above. It is automatic and uses the same model as
the active turn, producing the same encrypted-checkpoint replay described
above; a usage-limit 429 during it fails over to another available Codex
account.

## Upstream provider routing (OpenRouter)

Aggregator gateways (OpenRouter) may serve one model from several upstreams
and load-balance across them per request — silent provider switching and
prompt-cache misses between turns. The user can pin one upstream per model
(fail-closed) or leave routing to the gateway (Auto).

- **Data source:** `GET /models/{slug}/endpoints` on the gateway (one
  request per model; no bulk endpoint). The lookup slug is the imported
  `canonical_slug` plus any request variant on the model ID (`:free`,
  `:batch`, …); without the variant the paid route list is returned.
- **Route fields:** slug, name, quantization, status, p50 latency (ms), p50
  throughput (tokens/sec), and USD per-1M-token input/output costs — an
  omitted cost means no usable pricing, zero is an explicit free price.
  Route lists are cached on disk per provider+model (each gateway serves its
  own upstream set).
- **Direct providers** (Anthropic, OpenAI, local chat) have no route
  concept — the UI shows a non-interactive home icon next to the model
  picker.
- **Hosts without an endpoints API** (OpenCode, some aggregators) may still
  show a route picker; listing then fails quietly and the picker shows an
  empty "No provider in this model" list, while transient fetch failures
  surface as provider errors.
- **Pinning:** a pinned route on a chat-kind OpenRouter gateway is sent as
  `provider: {order: [route], allow_fallbacks: false}` — a hard pin: if
  the upstream is unavailable or blocked at the account level, the request
  fails (404 "No endpoints found") instead of silently falling back to
  another provider. Auto sends no provider object, so OpenRouter's load
  balancing applies.
- **Per room:** the pin is stored per room, never as a global preference —
  for OpenRouter it holds the upstream slug, for Codex the account ID.
  Switching models resets it to Auto because both route slugs and account
  applicability are model/provider-specific.
- **Blocked upstreams:** account-level ignored providers still appear in
  the route list (the endpoints API is unaware of account privacy
  settings); pinning one yields a 404 the UI surfaces as a fetch hint.
