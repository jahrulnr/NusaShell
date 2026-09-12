# Providers

Providers are the LLM backends the agent chats through. A provider is
defined by its **API wire format**, not by a vendor: **Messages**,
**Responses**, **Chat**, **Gemini**, or the **Codex** backend.

## Drivers

The provider driver selects the implementation package while the provider kind
selects the wire format:

- The persistent **Anthropic** card uses
  `infrastructure/ai/anthropic` with `messages`.
- The persistent **OpenAI** card uses `infrastructure/ai/openai` with
  `responses`.
- The persistent **OpenRouter** card uses
  `infrastructure/ai/openrouter`; its editor supports `responses`, `chat`, and
  `messages`.
- The persistent **Gemini** card uses
  `infrastructure/ai/gemini` with `gemini`: Google AI Studio's
  `generateContent` / `streamGenerateContent` wire
  (`https://generativelanguage.googleapis.com`, `x-goog-api-key` auth). It
  requires an AI Studio API key and lists models through `GET /v1beta/models`.
- A **Codex** provider uses the ChatGPT Codex Responses endpoint with the
  `codex` driver. Prefer **Sign in with ChatGPT** (OAuth PKCE) or **Import
  from Codex CLI** on the Providers → Codex detail page. Pasting an OAuth
  access token in the edit dialog is an optional fallback only. Tokens are
  stored in the SQLite credential store under the provider ID and under
  `{providerID}:account:{accountID}` for multi-account failover. The Codex
  transport owns its required headers, Responses stream decoding, and remote
  v2 compaction. That compaction stores the opaque checkpoint separately,
  retains real user turns for the next request, and archives the replaced
  assistant/tool transcript locally for room scroll-back.
- Custom `messages`, `responses`, and `chat` providers default to
  `infrastructure/ai/openrouter`; custom `codex` entries use the Codex
  transport and custom `gemini` entries use the Gemini wire. The editor
  supports all five kinds. There is no custom-provider
  count limit. The OpenRouter implementation is used as the
  compatibility/profile parent against the custom Base URL, including when
  that URL is not hosted at `openrouter.ai`. This keeps the endpoint-specific
  OpenRouter mappings available to unpredictable gateways (for example,
  `video_url` on Chat, `input_video` on delegated Responses, and an explicit
  rejection for video on delegated Messages). A custom record with an
  explicit direct driver still opts into that driver's wire behavior.

The built-in cards remain visible before they are configured. Configure a
card with its base URL and credential, then import models when the provider
supports model listing. The built-in Anthropic, OpenAI, OpenRouter, Gemini,
and Codex cards keep their existing direct/native adapter paths; custom
providers use the OpenRouter compatibility/profile parent by default.
OpenRouter, Gemini, and custom providers can each use a different API kind
and base URL.

**Chat routing:** a genuine OpenRouter host (`*.openrouter.ai`) is detected
for legacy/automatic Chat routing. A provider whose selected driver is
`openrouter` uses the OpenRouter compatibility/profile implementation even
when its Base URL is a custom gateway such as TokenRouter, 9Router, OpenCode,
one-api, LiteLLM, LM Studio, or vLLM. The custom Base URL remains the target,
so the gateway must accept the selected OpenRouter-compatible request shape.
Unsupported fields or response shapes surface as provider errors. NusaShell
does not silently retry the same request through vanilla Chat, Responses, or
Messages.

## Kinds

- `messages` — the Anthropic Messages wire format (`/v1/messages`); supports
  prompt caching via `cache_control`.
- `responses` — the OpenAI Responses wire format (`/v1/responses`); supports
  function calling and reports cached input tokens.
- `chat` — the Chat Completions wire format (`/chat/completions`), using
  the OpenRouter compatibility/profile implementation for the built-in
  OpenRouter card and custom providers with the `openrouter` driver. The
  custom Base URL remains the destination, and the gateway must accept that
  profile. Direct/automatic Chat providers can still use the vanilla OpenAI
  Chat wire (`reasoning_effort`, `reasoning_content`, `max_tokens`).
- `gemini` — Google's `generateContent` wire format
  (`POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent`
  with `alt=sse`). Messages become `contents` parts with an optional
  `systemInstruction`; tools become `functionDeclarations` with an
  OpenAPI-subset schema; structured output uses `responseMimeType` +
  `responseSchema`; thinking uses `thinkingConfig` (`thinkingBudget` on
  Gemini 2.x, `thinkingLevel` on Gemini 3). Function calls carry a
  `thoughtSignature` that must be replayed with the call on the next turn.
  Prompt caching is implicit server-side (reported as
  `cachedContentTokenCount`), so there is no cache TTL chip.
- `codex` — the ChatGPT Codex Responses wire format
  (`/backend-api/codex/responses`); it uses OAuth access tokens (Sign in /
  Import from CLI; optional paste fallback), Codex session headers, multi-
  account failover with circuit breakers, and the remote v2 compaction flow.
  **Import models** discovers the account-aware catalog via the Codex CLI
  app-server `model/list` JSON-RPC method using the OAuth account selected in
  NusaShell—not whichever account happens to be active in `~/.codex/auth.json`
  (NusaShell downloads the managed Codex runtime binary when needed).
  Explicitly switching accounts also clears existing sticky conversation
  bindings, so Retry and later turns use the selected account. ChatGPT plan
  image models (`gpt-image-2`,
  `gpt-image-1.5`) are seeded after import because `model/list` omits them.
  The same OAuth token and base URL also drive the Codex image backend used
  by `generate_image` (`/images/generations` | `/images/edits`). Image
  generation shares the multi-account router: the sticky account for the
  conversation is picked per call, and a usage-limit 429 (with its
  `resets_at`) or a 403 entitlement rejection opens that account's circuit
  and fails over to the next available account (one attempt per account —
  image calls are billable, so there is no blind retry). A 403 is reported
  as a likely missing image entitlement (e.g. ChatGPT Free). The router
  persists the last-used account per provider, so after a backend restart it
  resumes from that account instead of blindly defaulting to the first
  registered one. When the active chat provider is Codex, `web_search`
  tries the Codex standalone `/alpha/search` backend first and falls back to
  searchwire when the Codex search is unavailable or fails. Results are
  normalized to the normal `web_search` shape; access to the standalone
  endpoint remains dependent on the authenticated Codex backend/account.
  Tool results that carry media (e.g. `generate_image` or `read_media` outputs) are split on the Codex wire: the
  `function_call_output` stays text-only and the media is reinjected as the
  next user message with `input_image` items — `core.ImageBlock` is never
  serialized into a text-only tool output.

## Streaming completion and request-shape recovery

OpenAI-compatible SSE providers do not all emit the `[DONE]` sentinel. A clean
EOF after a final choice with `finish_reason` is treated as a completed
response; the sentinel is a transport convention, not the completion signal.
A clean EOF without a semantic finish reason remains an incomplete stream and
is sent through the shared retry policy. Network cuts and idle timeouts follow
the same policy.

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

Do not classify this 400 as a transient outage: resending the same assistant-
ended request cannot succeed. A 429 without a usable `Retry-After` is likewise
hard-failed; a retry is only automatic when the shared domain policy says the
provider supplied a safe retry window.

## Prompt cache TTL

Settings → **Prompt caching** is the master switch. The **Cache TTL** chips
on a provider's detail pane pick what that provider sends, or turn cache
off for that provider only:

- `messages` (Anthropic `cache_control`): `5m` or `1h`. Default `5m`.
- `responses` (OpenAI `prompt_cache_options.ttl`): `30m`.
- `chat` using the OpenRouter profile, including custom gateways with the
  `openrouter` driver (`cache_control`): `5m` or `1h`. Default `5m`.
  OpenRouter's profile does not accept `30m` as a `cache_control` TTL. Custom
  gateways may still reject the profile, in which case the provider error is
  returned without a silent vanilla-wire retry.
- direct/automatic vanilla `chat` hosts (`prompt_cache_key` plus
  `prompt_cache_options`): `30m`. Putting 5m/1h on a Chat system breakpoint
  is rejected locally.
- `off` skips prompt-cache markers for this provider even when the Settings
  switch is on.
- `gemini` shows no chips: caching is implicit server-side and there is no
  cache key, block, or TTL on the wire. Cached tokens still appear in usage
  (`cachedContentTokenCount` → cache read).

Empty stored `cache_ttl` still means the first duration above. `off` is
stored explicitly and applied on the next turn. Registry cards show the
selected value, including `off`.

## Codex thinking summary

The **Thinking summary** chips on a Codex provider's detail pane control the
`reasoning.summary` value sent on later Codex turns. The choice is stored per
provider:

- `auto` lets Codex choose the appropriate summary detail and is the default.
- `concise` requests a shorter visible summary.
- `detailed` requests a more verbose visible summary.
- `none` suppresses the visible summary. It does not disable model reasoning
  or encrypted reasoning replay.

This option is available only for the `codex` kind. It controls how much
thinking Codex exposes in the transcript; it does not guarantee that every
turn produces a summary.

## Prompt-cache keys and sessions

When prompt caching is enabled and the provider Cache TTL is not `off`,
NusaShell creates one stable 32-character ASCII key per
provider/model/conversation. The visible prefix separates agent workloads
without increasing the key length:

- `nusashell_cv_` + 19 hexadecimal characters — normal conversation turns.
- `nusashell_bg_` + 19 hexadecimal characters — headless/background and
  learning-job turns.

The key is sent through the wire only where the selected adapter has a useful
path:

- OpenAI Responses and direct/automatic vanilla OpenAI Chat:
  `prompt_cache_key` in the request body, as documented by OpenAI.
- Anthropic Messages: no synthetic `prompt_cache_key`; caching remains native
  `cache_control` breakpoints because the Messages API does not document a
  caller-supplied cache-key field.
- OpenRouter-profile Chat, including custom Chat gateways: both
  `prompt_cache_key` and `session_id` are sent in the body. Reusing the key as
  `session_id` enables OpenRouter-style sticky routing and session grouping
  where the gateway implements it. OpenCode also receives the documented
  `x-opencode-session` header.
- OpenRouter-profile Messages and Responses: the same session value is sent as
  the documented `x-session-id` header; Responses also retains
  `prompt_cache_key` in the body through its OpenAI-compatible adapter.

An unknown HTTP header is normally ignored by an HTTP server, but that is not
a cross-gateway contract and it does not make an unknown JSON body field safe.
Strict gateways can reject unsupported body parameters: LiteLLM documents that
unsupported OpenAI parameters raise by default, while provider-specific
parameters are forwarded to the upstream body. Therefore NusaShell sends the
stable key only on the selected adapter's documented/known path, uses the
OpenRouter-profile `x-session-id` header for OpenRouter-profile providers, and
uses OpenCode's documented `x-opencode-session` header for OpenCode hosts. It
does not inject an arbitrary `X-NusaShell-*` header or an undocumented
cache-key field into Anthropic or unrelated gateways.

OpenRouter's current prompt-caching guide documents `prompt_cache_key`,
`session_id`/`x-session-id`, the 256-character session limit, and Sessions-view
grouping. OpenAI documents `prompt_cache_key` as a routing/cache hint (not a
guaranteed cache hit). Anthropic documents `cache_control` TTLs and breakpoint
rules. OpenCode Go documents `x-opencode-session` as the prompt-cache
affinity header. See [OpenRouter prompt caching](https://openrouter.ai/docs/guides/best-practices/prompt-caching),
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

Custom OpenCode entries use the OpenRouter compatibility/profile path. Chat
requests therefore use the OpenRouter-style `reasoning` and provider-option
shape rather than an automatic downgrade to `reasoning_content`. This is an
intentional compatibility choice for an undocumented/unpredictable gateway:
if a particular OpenCode deployment accepts only the vanilla Chat shape, its
provider error is surfaced and NusaShell does not retry through another API
kind or wire profile. The OpenCode-specific `x-opencode-session` header is
still attached from the conversation prompt-cache key. Cache TTL chips are
`5m`, `1h`, or `off`; Console Go rejects `prompt_cache_options.ttl=30m`, but
the selected OpenRouter profile may also send cache fields that the gateway
must support.

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
and keeps only entries whose `supportedGenerationMethods` include
`generateContent` **and** whose model ID is not an image, TTS, or embedding
variant (e.g. `gemini-3.1-flash-lite-image`), so embedding, image, TTS, and
media-only models never reach the chat picker.

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
returns `thoughtSignature`. NusaShell persists a tool call's signature with
that call (`ToolCall.Opaque`) and replays it on the next turn; a text part's
signature is replayed as opaque reasoning state. Gemini 3 rejects a replayed
`functionCall` part without a signature, so `gemini-3*` history that has none
for a batch (for example a conversation migrated from another provider) is
backfilled with Google's documented placeholder sentinel. Dropping signatures
degrades or breaks multi-turn reasoning, so do not strip them when editing the
conversation backend.

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

The `gemini` kind intentionally has **no** chat TTL chip, embedding endpoint,
image endpoint, TTS, or video endpoint: those capabilities come from the
OpenAI-compatible kinds. `Test connection` on a Gemini card lists
`GET /v1beta/models`, so the probe costs nothing.

## API keys

API keys are optional for `messages`, `responses`, and `chat`; the `gemini`
kind requires a Google AI Studio key (there is no anonymous keyless tier on
the Generative Language API). The Codex kind
authenticates with ChatGPT OAuth: use **Sign in with ChatGPT** or **Import
from Codex CLI** on the Providers → Codex detail page (primary paths). Pasting
an OAuth access token in the provider edit dialog is an optional fallback
only. For optional-key kinds, a blank key is stored as no credential and
NusaShell still lists, tests, imports, and chats. Wire adapters skip
`Authorization` / `x-api-key` when no key is present; the upstream decides
(e.g. OpenCode, LM Studio, Ollama, Zen free tier). When a key is present it
is sent normally. Credentials are stored in the SQLite credential store
(`credentials.db`) inside the data directory, never in the JSON files.

### Codex accounts, usage, and runtime

On the Codex provider detail page:

- **ChatGPT Accounts** lists signed-in accounts with plan and session/weekly
  usage bars. **Sign in with ChatGPT** runs OAuth PKCE (`ai.codex.login`).
  **Import from Codex CLI** reads `~/.codex/auth.json` (`ai.codex.import`).
  **Refresh** polls circuits (`ai.codex.refresh-circuits`) then reloads usage
  (`ai.codex.usage`). Switch / Remove call `ai.codex.accounts.switch` and
  `ai.codex.logout`. When usage is unavailable, the UI falls back to
  `ai.codex.accounts.list` for identity-only rows.
- **Codex Runtime** shows the managed official Codex CLI binary via
  `ai.codex.runtime.status` / `ai.codex.runtime.download` (ACP and tooling;
  chat compaction uses the remote v2 path, not a subprocess compact UI).
- In the Agent composer, the routing control becomes an account picker for a
  Codex model. **Auto** keeps the existing sticky quota/cooldown rotation.
  Selecting an account strictly pins that room to the chosen credential; it
  does not fail over to a different plan behind the user's choice. The
  selection is persisted in the conversation backend, so room switches and
  reloads cannot inherit another room's account. The provider detail page's
  **Switch** action remains the default preference used by Auto.

```text
# GOOD — primary auth paths on the Codex detail card
Sign in with ChatGPT
Import from Codex CLI

# BAD — treating paste as the only way to configure Codex
Edit → paste OAuth access token → Save  (fallback only)
```

### Seeding keys from the environment (explicit)

Keys normally come from the UI form. For headless or containerized setups,
the binary also offers an **explicit, opt-in** subcommand that copies keys
from environment variables into `credentials.db`:

```bash
nusashell seed-providers
```

This runs **only when you invoke it** — the server never reads these
variables on its own during normal startup, so there is no hidden behavior.
It respects `NUSASHELL_DATA_DIR`, prints a line per action, and then exits.
It is idempotent and non-destructive: it creates the provider (enabled) on
first run and, on later invocations, only rewrites the stored key when the
variable's value changed (secret rotation). A provider's name, base URL, and
enabled state that you edited in the UI are never overwritten, and the
variable is never re-read into the wire config. After seeding, use **Import
models** (or wait for the periodic auto-import) to populate the model list.

| Env variable | Provider | Kind | Base URL |
| --- | --- | --- | --- |
| `OPENROUTER_API_KEY` | OpenRouter | `chat` | `https://openrouter.ai/api/v1` |

## Models

After saving a provider, use **Import models** to fetch its model list
(`GET /models`; the `gemini` kind reads `GET /v1beta/models`). OpenRouter also
exposes `GET /images/models`; those ids
are merged and tagged `kind: image`. The agent only offers imported models.
Messages providers bundle Claude model metadata (context window, pricing);
imported models keep the provider's own ids. If a chat request is rejected
with an explicit "maximum context length" or "context window" limit, NusaShell
learns that limit for the provider+model and updates the model's effective
context window, so the catalog does not overstate the actual window available
on that gateway. Likewise, if a model 400s because it is text-only or does
not support a modality (vision, audio, video, document), NusaShell learns
that and disables the modality for the provider+model. Future turns apply
these learned overrides at model resolution time, so requests are built with
the real capabilities instead of waiting for another 400.

The same learned registry can record request-shape constraints, such as a
gateway requiring a user message at the end of the request. These rules are
scoped to provider+model and applied only on a retry after the matching 400;
they do not change the persisted conversation transcript.

Learned overrides are inferred from errors and can be wrong (a false
positive). A second, **manual override** layer exists for corrections with
direct evidence. The unified background learning agent can record one via
its local `model_override` tool when a transcript shows the catalog metadata
is wrong
for a specific provider+model (e.g. a model marked text-only that actually
served images). Manual overrides are stored per provider+model in
`learning/model_overrides.json`, survive catalog re-imports and process
restarts, and are applied at model resolution time **after** learned
overrides — so a manual correction always wins over both the catalog and an
auto-learned value. Precedence: catalog → learned → manual. For direct Codex
chat, the public catalog context window supersedes the app-server discovery
value; discovery is retained only when the catalog is unavailable, and
learned/manual overrides still apply afterward. Models tagged as
embedding-capable appear in the
Embedding model setting for skill and memory search. Embedding requests use
the same OpenRouter app attribution as chat when the selected provider points
at `*.openrouter.ai`: `HTTP-Referer`, `X-OpenRouter-Title`, and the app
categories are sent on `POST /v1/embeddings`, so embedding usage is attributed
to NusaShell instead of appearing as an unknown app. Those router-specific
headers are not sent to other OpenAI-compatible hosts. Models tagged as image
generators (`kind: image`, including `gpt-image-*` and `dall-e-*` even when
`/models` omits a kind) appear in Settings → Image generation and back the
`generate_image` tool. Image generation uses the
dedicated OpenAI `/images/generations` (and `/images/edits`) endpoints, the
ChatGPT Codex images endpoints on the codex provider base URL
(`.../codex/images/generations` and `.../codex/images/edits` — `gpt-image-2`
and `gpt-image-1.5` are seeded into the codex model list and tagged i2i), or
OpenRouter `POST /images` (JSON body, including `images[].image_url` data
URLs for edits — not OpenAI multipart). The Codex backend sends
`background`/`quality`/`size` with `auto` defaults, always requests a single
image (no `n`), sends the `x-codex-image-turn-id` turn-correlation header,
and surfaces image-quota 429s (`error.type=usage_limit_reached`) as hard
usage-limit failures instead of blind retries. Anthropic Messages is not an
image backend; it can still orchestrate `generate_image` when a supported
image provider is configured.

## Test connection

**Test connection** probes connectivity only: it lists models with
`GET /models` (responses/chat), `GET /v1/models` (messages), or
`GET /v1beta/models` (gemini) and
reports latency and the model count. No completion is sent, so the probe
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
- Refresh catalog opens a throwaway `session/new` to import modes and models.
  Agents in the OpenCode generation return v1 `configOptions` instead of
  legacy `modes`/`models`: select-type mode/model options fold into the same
  catalogs, and mode/model switching still goes through the legacy
  `session/set_mode` / `session/set_model` methods those agents keep serving.
- Mode IDs stay vendor-specific; NusaShell maps them onto internal risk
  tiers (`read_only`, `edit_confirmed`, `bypass`). Unknown modes are
  read-only. New sessions start on the strictest advertised mode. Bypass is
  never the default — promote it from the live subagent UI.
  `edit_confirmed` auto-allows workspace-contained edits; slash-rooted
  paths are absolute (even on Windows) and prompt instead of auto-allow.
- Env values stay in `config/acp-agents.json`. List/get RPC returns **keys only**.

The parent agent spawns these binaries with `subagent` (optional `count` for
parallel sessions). You can peek, steer, stop, change mode, and answer
permission prompts from the Agent dock, drawer, or popup.

Stdio is newline-delimited JSON-RPC (`\n` between messages, no embedded
newlines). NusaShell does **not** send LSP `Content-Length` headers — the
ACP spec parses each line with `JSON.parse`.
Logs from the agent belong on stderr.

### Common CLIs (install + auth)

| CLI | Command | Auth methods (typical) | Not logged in |
| --- | --- | --- | --- |
| Cursor | `curl https://cursor.com/install \| bash` then `agent acp` | `cursor_login` | `initialize` succeeds; `session/new` returns `Authentication required`. Run `agent login` locally, then **Authenticate** with `cursor_login` in Providers. |
| OpenCode | install per [opencode.ai/docs](https://opencode.ai/docs/acp/) → `opencode acp` | `opencode-login` | `initialize` works. Real login is CLI-side (`opencode auth login`) — **Authenticate** with `opencode-login` only validates the method id, it does not log in. Missing provider keys surface as JSON-RPC `-32000` on `session/new` or the first prompt. |

If you register a CLI without logging in, **Probe** still works (it only runs
`initialize`). **Refresh catalog** and `subagent` spawn call `session/new` and
fail until you complete **Authenticate** with one of the advertised method ids.
NusaShell surfaces a clear error naming those ids instead of a bare JSON-RPC
`Authentication required`.

**One login only (lazy auth):** After you **Authenticate** once, NusaShell
does not re-call `authenticate` on subsequent probes, catalog refreshes, or
subagent spawns. It tries `session/new` first and only calls `authenticate`
when the agent reports an auth-required error. Agents that persist their own
auth (e.g. Devin storing tokens in `~/.devin/auth.json`) will not
re-trigger the browser login flow on every new connection.

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
  raw limit, but any partially consumed window blocks it and backoff drains
  far slower than the window, so retries spin uselessly.

Structural and dominant rejections are handled identically: the agent does
**not** burn provider attempts on them — it bails to an **emergency
compaction** (the transcript is summarized down to the compaction budget)
and retries the round with the smaller context, the same safety net that
fires on a context-window overflow 400. The live context badge uses a
provider-visible preflight estimate: text is approximated by character
density, provider replay items are adjusted for opaque encrypted content, and
image, audio, and video data URLs are charged as modality units rather than
as base64 text. It remains a heuristic until provider usage arrives, so the
provider's own `Limit`/`Used`/`Requested` numbers are trusted as proof of
overflow.

A dominant rejection also teaches a durable rule: NusaShell derives a
context-window cap from the provider's per-minute budget (half the budget
minus the completion budget, floored at a quarter) and records it in the
learned-param registry for that provider+model. Every conversation on that
provider+model then compacts against the smaller window, so requests stay
within the per-minute budget instead of colliding with it every round (the
`learning` log stream shows the recorded cap, e.g. `learned TPM context cap
for openai/gpt-5.6-luna`). This applies to OpenAI official accounts that
report `Limit`/`Requested` numbers; other gateways' rate-limit text never
parses and behavior is unchanged.

On the Responses API, TPM rejections arrive mid-stream as an SSE
`event: error` (the request is accepted with HTTP 200 first), so the
provider classifies them as rate-limit errors with an assumed 1-minute
window instead of generic provider errors. On Chat Completions they arrive
as HTTP 429. Both paths surface a message naming the token numbers (limit,
already-used, requested) instead of the requests-per-minute one.

## Encrypted reasoning replay (Responses / Codex)

OpenAI Responses, Codex, and some OpenRouter/compat OpenAI routes return
opaque reasoning state (often `encrypted_content` inside a reasoning item)
alongside the visible thinking summary. Wire adapters already request
`include: ["reasoning.encrypted_content"]` and keep that JSON on
`ReasoningBlock.Extra` for in-memory replay.

NusaShell also persists that opaque payload on the assistant message as
`ReasoningExtra` (omitted when empty) and reattaches it on the next turn so
multi-turn ChatGPT/Codex/OpenRouter Responses-style models do not lose
reasoning continuity. The UI continues to show only plaintext `Reasoning`.
This is distinct from conversation-level `CompactionBlob`, which stores
server-side compaction items, not per-message reasoning Extra.

When the active provider kind cannot replay that opaque state, NusaShell
strips `ReasoningExtra` from the assembled request and keeps plaintext
`Reasoning` only:

- **Chat direct/vanilla OpenAI:** never forwards Extra — Chat Completions
  reject signed, redacted, or provider-extra reasoning blocks.
- **Chat OpenRouter profile (including custom gateways):** keeps Extra only
  when it is a JSON array (`reasoning_details`); Codex encrypted objects are
  stripped.
- **Messages (Anthropic):** never forwards Extra (Anthropic uses
  signature / redacted thinking, not Responses-style Extra).
- **Responses / Codex:** forwards Extra unchanged for continuity.

`CompactionBlob` is already kind-gated the same way (Responses/Codex only).
A Codex room continued on DeepSeek Chat therefore replays the post-
compaction transcript with plaintext thinking and does not send the
encrypted checkpoint or per-message Extra.

## Server-side compaction (OpenAI Responses)

For eligible OpenAI Responses models, NusaShell uses server-side compaction
via the `context_management` parameter in `POST /responses`. When the
rendered token count crosses the configured `compact_threshold`, the server
triggers a compaction pass in-stream, emits an encrypted compaction item in
the response output, and prunes context before continuing inference. No
separate `/responses/compact` call is required.

Eligible models (context window >= 200k):

- `gpt-5.x` family (gpt-5, 5.1, 5.2, 5.3-codex, 5.4, 5.5, 5.6-sol/terra/luna,
  mini, nano, pro, codex) — 400k–1M context
- `gpt-4.1` family (gpt-4.1, 4.1-mini, 4.1-nano) — 1M context
- `o-series` (o1, o3, o3-mini, o4-mini, o1-pro, o3-pro, codex-mini-latest) —
  200k context

Models below the 200k floor (gpt-4o at 128k, gpt-*-chat-latest at 128k) and
non-OpenAI models (Anthropic, OpenRouter) use the client-side multi-pass
summarization path.

Key behaviors:

- **Threshold:** `max(context_window * 0.9, 120_000)` — the server compacts
  when the rendered token count crosses this threshold. The floor ensures
  the server does not wait longer than the client-side path would have.
- **Compaction item capture:** when the server emits a compaction item in
  the response stream, NusaShell captures it and stores it on the
  conversation as `CompactionBlob`. The next turn replays it as a prefix
  of the request's `input` array via the `compaction_items` provider
  option. The server then truncates context before the last compaction
  item automatically.
- **No fallback:** server-side compaction runs in-stream; there is no
  separate endpoint call that can fail. If the server does not trigger
  compaction (context stays under threshold), the conversation continues
  normally. The client-side summarization path is only used for models
  that are not server-side eligible.
- **Compaction model override:** when the chat model is server-side
  eligible, the `settings.compaction_model` override is skipped. The
  compaction item is encrypted for the chat model and only that model can
  read it, so switching to a different model for compaction would
  invalidate it.
- **Token estimation:** the backend preflight estimator includes the
  provider-visible `CompactionBlob` items, with encrypted reasoning sized by
  their model-visible approximation rather than their encoded byte length.
  The context badge marks this value as provisional until provider usage is
  returned.

## Codex remote v2 compaction

Codex compaction is a separate pre-turn streaming request, selected by
provider kind. It is not OpenAI `context_management`.

- **Request:** NusaShell sends a normal streaming `POST /responses` request
  whose final input item is `{"type":"compaction_trigger"}`. The resulting
  history records an explicit checkpoint boundary: retained user context is
  before the opaque `CompactionBlob`, while later user, assistant, reasoning,
  tool-call, and tool-result items remain after it in causal order.
- **Result:** the stream must contain exactly one `compaction` output item.
  Its encrypted content is stored unchanged in `CompactionBlob`, and the
  conversation starts a new transcript epoch without losing the boundary
  needed by later turns, tool rounds, reloads, or another compaction.
- **Model:** Codex compaction uses the same provider and model as the active
  turn. `settings.compaction_model` is not used for this path.
- **Failure and account routing:** remote compaction has no fallback to the text
  `summary()` path. Before this separate request, NusaShell reselects the
  conversation's non-circuit-open Codex account and rebuilds its adapter when
  needed. A usage-limit 429 opens the failed account's circuit and retries
  once on another available account. Transport failures use at most two
  remote-v2 stream retries; a Codex remote-compaction stream may remain idle
  for five minutes between SSE events, matching Codex upstream rather than the
  one-minute interactive-stream watchdog.
- **Wire boundary:** Codex sends its own authentication and session headers
  and does not receive OpenAI `context_management`.

## Upstream provider routing (OpenRouter)

Aggregator gateways (OpenRouter) may serve one model from several upstream
providers, and by default load-balance across them per request. That causes
silent provider switching between turns and prompt-cache misses. NusaShell
lets the user pin one upstream per model (fail-closed) or leave routing to
the gateway (Auto).

- **Data source:** `GET /models/{slug}/endpoints` (one request per model;
  there is no bulk endpoint). The lookup slug is the imported
  `canonical_slug` (falling back to the model ID) plus any request
  variant on the model ID (`:free`, `:batch`, …). OpenRouter's
  `canonical_slug` is the undated identity shared by paid and free
  siblings, so listing endpoints without the variant returns the paid
  route list. Response `tag` fields are the routing slugs used in
  `provider.order`.
- **RPC:** `ai.models.endpoints {provider_id, model_id}` returns
  `{routes:[{slug,name,quantization,status,latency,throughput,input_cost,
  output_cost}], cached, fetched_at}`. `input_cost` and `output_cost` are
  USD per 1M input/output tokens from the endpoint's
  `pricing.prompt`/`pricing.completion`; omitted values mean the gateway did
  not provide usable pricing, while zero is an explicit free price. Routes
  are cached on disk under the data dir (`endpoints_cache.json`, TTL 24h),
  keyed per provider+model because each gateway serves models with its own
  upstream set. The cache schema version changes when route fields change so
  stale entries are refetched. `latency` is the rolling 30m **p50 in
  milliseconds** and `throughput` the p50 tokens/sec (the gateway serves
  these as percentile objects `{"p50":...}` when authenticated, or plain
  numbers/null; the parser accepts both shapes).
- **Direct providers** (Anthropic, OpenAI, local chat) have no route
  concept: the handler returns an empty list without fetching, and the
  frontend shows a non-interactive home icon next to the model picker.
- **Hosts without an endpoints API** (OpenCode, some aggregators) may still
  advertise `route_support` when their driver is OpenRouter. Listing then
  returns HTTP 4xx HTML; NusaShell skips that body and shows an empty list
  titled "No provider in this model". HTTP 5xx is also skipped without
  caching so a later refresh can recover. Non-HTTP fetch errors still
  surface as `PROVIDER_ERROR`.
- **Pinning wire:** when a conversation has a non-empty `provider_route`
  and the provider is a chat-kind OpenRouter gateway, the adapter sends
  `provider: {order: [route], allow_fallbacks: false}` — a hard pin: if
  the upstream is unavailable or blocked at the account level, the request
  fails (404 "No endpoints found") instead of silently falling back to
  another provider. Auto (empty route) sends no provider object, so
  OpenRouter's load balancing applies.
- **Persistence:** `provider_route` is stored on the conversation (like
  `effort`) and sent in `agent.turns.start` / `agent.turns.retry`. For Codex
  it contains the selected account ID; for OpenRouter it contains the
  upstream slug. The frontend keeps this state per room, never as a global
  browser preference. Switching models resets it to Auto because both route
  slugs and account applicability are model/provider-specific.
- **Blocked upstreams:** account-level ignored providers still appear in
  the route list (the endpoints API is unaware of account privacy
  settings); pinning one yields a 404 the UI surfaces as a fetch hint.
