# Providers

Providers are the LLM backends the agent chats through. A provider is
defined by its **API wire format**, not by a vendor: **Messages**,
**Responses**, or **Chat**.

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
- Every custom provider defaults to `infrastructure/ai/openrouter`; its
  editor supports `responses`, `chat`, and `messages`. There is no
  custom-provider count limit. For `chat` kind, host detection (below)
  routes genuine OpenRouter hosts to the OpenRouter implementation and all
  other hosts to the vanilla OpenAI Chat implementation.

The three built-in cards remain visible before they are configured. Configure a
card with its base URL and key, then import models. OpenRouter and custom
providers can each use a different API kind and base URL.

**Host detection for `chat`:** only a genuine OpenRouter host
(`*.openrouter.ai`) uses the OpenRouter chat implementation (OpenRouter wire
format — `reasoning` object, `reasoning_details`, cache retention, provider
options, attribution headers). Every other `chat` host — direct OpenAI,
OpenAI-compatible aggregators (TokenRouter, 9Router, OpenCode, one-api,
LiteLLM, local endpoints like LM Studio/vLLM) — uses the vanilla OpenAI Chat
Completions wire (`reasoning_effort`, `reasoning_content`, `max_tokens`).
Aggregators implement the OpenAI wire and reject OpenRouter-specific params:
for example TokenRouter returns HTTP 400 `Unknown parameter: 'reasoning'`
for the OpenRouter `reasoning` object, so they must not receive the OpenRouter
format. A provider with an explicit `openrouter` driver is always treated as
OpenRouter regardless of host.

## Kinds

- `messages` — the Anthropic Messages wire format (`/v1/messages`); supports
  prompt caching via `cache_control`.
- `responses` — the OpenAI Responses wire format (`/v1/responses`); supports
  function calling and reports cached input tokens.
- `chat` — the OpenAI Chat Completions wire format (`/chat/completions`);
  works with OpenAI, DeepSeek, LM Studio, vLLM and any compatible endpoint.
  Genuine OpenRouter hosts use the OpenRouter chat implementation for this
  kind, including its provider options and attribution headers; all other
  `chat` hosts use the vanilla OpenAI Chat wire (see host detection above).
  Providers without an explicit driver retain host-detected routing.

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

Settings → **Prompt caching** turns provider-side prompt cache on for a
turn. The **Cache TTL** chips on a provider's detail pane pick the duration
that NusaShell actually sends:

- `messages` (Anthropic `cache_control`): `5m` or `1h`. Default `5m`.
- `responses` (OpenAI `prompt_cache_options.ttl`): `30m`.
- `chat` on the OpenRouter driver (`cache_control`): `5m` or `1h`. Default
  `5m`. OpenRouter does not accept `30m` as `cache_control` TTL.
- other `chat` hosts (OpenAI Chat `prompt_cache_key` +
  `prompt_cache_options`): `30m`.

The selected value is stored on the provider (`cache_ttl`) and applied on
the next turn while prompt caching is enabled. Registry cards show the
selected TTL, not the full enum.

## Prompt-cache keys and sessions

When prompt caching is enabled, NusaShell creates one stable 32-character
ASCII key per provider/model/conversation. The visible prefix separates agent
workloads without increasing the key length:

- `nusashell_cv_` + 19 hexadecimal characters — normal conversation turns.
- `nusashell_bg_` + 19 hexadecimal characters — headless/background and
  learning-job turns.

The key is sent through the wire only where the selected adapter has a useful
path:

- OpenAI Responses and vanilla OpenAI Chat: `prompt_cache_key` in the request
  body, as documented by OpenAI.
- Anthropic Messages: no synthetic `prompt_cache_key`; caching remains native
  `cache_control` breakpoints because the Messages API does not document a
  caller-supplied cache-key field.
- Native OpenRouter Chat: both `prompt_cache_key` and `session_id` are sent in
  the body. Reusing the key as `session_id` enables OpenRouter sticky routing
  and Logs → Sessions grouping.
- OpenRouter Messages and Responses: the same session value is sent as the
  documented `x-session-id` header; Responses also retains
  `prompt_cache_key` in the body through its OpenAI-compatible adapter.

An unknown HTTP header is normally ignored by an HTTP server, but that is not
a cross-gateway contract and it does not make an unknown JSON body field safe.
Strict gateways can reject unsupported body parameters: LiteLLM documents that
unsupported OpenAI parameters raise by default, while provider-specific
parameters are forwarded to the upstream body. Therefore NusaShell sends the
stable key on the documented/known OpenAI-compatible paths and uses the
OpenRouter-specific session header only for OpenRouter. It does not inject an
arbitrary `X-NusaShell-*` header or an undocumented cache-key field into
Anthropic or unrelated gateways.

OpenRouter's current prompt-caching guide documents `prompt_cache_key`,
`session_id`/`x-session-id`, the 256-character session limit, and Sessions-view
grouping. OpenAI documents `prompt_cache_key` as a routing/cache hint (not a
guaranteed cache hit). Anthropic documents `cache_control` TTLs and breakpoint
rules. See [OpenRouter prompt caching](https://openrouter.ai/docs/guides/best-practices/prompt-caching),
[OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching),
[Claude prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching),
and [LiteLLM input parameters](https://docs.litellm.ai/docs/completion/input).

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

## API keys

`messages` and `responses` require a user-supplied API key. `chat` works
without a key against local endpoints that need no auth, and both chat wire
implementations accept keyless providers — the vanilla OpenAI Chat adapter
skips the `Authorization` header when no key is present, and the OpenRouter
adapter (used for genuine OpenRouter hosts) does the same; the upstream
decides (e.g. OpenCode/Zen free tier). When a key is present it is sent
normally. Keys are stored in the SQLite credential store
(`credentials.db`) inside the data directory, never in the JSON files.

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
(`GET /models`). OpenRouter also exposes `GET /images/models`; those ids
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
auto-learned value. Precedence: catalog → learned → manual. Models tagged as
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
dedicated OpenAI `/images/generations` (and `/images/edits`) endpoints or
OpenRouter `POST /images` (JSON body, including `images[].image_url` data
URLs for edits — not OpenAI multipart). Anthropic Messages is not an image
backend; it can still orchestrate `generate_image` when a supported image
provider is configured.

## Test connection

**Test connection** probes connectivity only: it lists models with
`GET /models` (responses/chat) or `GET /v1/models` (messages) and
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
fires on a context-window overflow 400. Image-heavy transcripts are
routinely undercounted by the local chars/4 token estimate, which is why
the provider's own `Limit`/`Used`/`Requested` numbers are trusted as proof
of overflow even when the local estimate is far below the compaction
trigger.

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
- **Token estimation:** `EstimateTokens` includes the `CompactionBlob`
  length so the context badge reflects the real request size after a
  server-side compaction.

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
- **Pinning wire:** when a conversation has a non-empty `provider_route`
  and the provider is a chat-kind OpenRouter gateway, the adapter sends
  `provider: {order: [route], allow_fallbacks: false}` — a hard pin: if
  the upstream is unavailable or blocked at the account level, the request
  fails (404 "No endpoints found") instead of silently falling back to
  another provider. Auto (empty route) sends no provider object, so
  OpenRouter's load balancing applies.
- **Persistence:** `provider_route` is stored on the conversation (like
  `effort`) and sent in `agent.turns.start` / `agent.turns.retry`.
  Switching models resets the route to Auto because slugs are
  model-specific.
- **Blocked upstreams:** account-level ignored providers still appear in
  the route list (the endpoints API is unaware of account privacy
  settings); pinning one yields a 404 the UI surfaces as a fetch hint.
