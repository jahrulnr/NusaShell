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
- Every custom provider uses `infrastructure/ai/openrouter`; its editor
  supports `responses`, `chat`, and `messages`. There is no custom-provider
  count limit.

The three built-in cards remain visible before they are configured. Configure a
card with its base URL and key, then import models. OpenRouter and custom
providers can each use a different API kind and base URL.

## Kinds

- `messages` — the Anthropic Messages wire format (`/v1/messages`); supports
  prompt caching via `cache_control`.
- `responses` — the OpenAI Responses wire format (`/v1/responses`); supports
  function calling and reports cached input tokens.
- `chat` — the OpenAI Chat Completions wire format (`/chat/completions`);
  works with OpenAI, DeepSeek, LM Studio, vLLM and any compatible endpoint.
  OpenRouter and custom providers use the OpenRouter implementation for this
  kind, including its provider options and attribution headers. Providers
  without an explicit driver retain host-detected routing.

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
without a key against local endpoints that need no auth, and the OpenRouter
adapter (the default for chat-kind hosts) accepts keyless providers — the
upstream decides (e.g. OpenCode/Zen free tier); when a key is present it is
sent normally. Keys are stored in the SQLite credential store
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

Learned overrides are inferred from errors and can be wrong (a false
positive). A second, **manual override** layer exists for corrections with
direct evidence. The background review agent can record one via its local
`model_override` tool when a transcript shows the catalog metadata is wrong
for a specific provider+model (e.g. a model marked text-only that actually
served images). Manual overrides are stored per provider+model in
`learning/model_overrides.json`, survive catalog re-imports and process
restarts, and are applied at model resolution time **after** learned
overrides — so a manual correction always wins over both the catalog and an
auto-learned value. Precedence: catalog → learned → manual. Models tagged as
embedding-capable appear in the
Embedding model setting for skill and memory search. Models tagged as image
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
