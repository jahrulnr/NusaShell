# Providers

Providers are the LLM backends the agent chats through. A provider is
defined by its **API wire format**, not by a vendor: **Messages**,
**Responses**, **Chat**, **Ollama**, or **Codex**.

## Kinds

- `messages` — the Anthropic Messages wire format (`/v1/messages`); supports
  prompt caching via `cache_control`.
- `responses` — the OpenAI Responses wire format (`/v1/responses`); supports
  function calling and reports cached input tokens.
- `chat` — the OpenAI Chat Completions wire format (`/chat/completions`);
  works with OpenAI, DeepSeek, LM Studio, vLLM and any compatible endpoint.
- `ollama` — a local Ollama instance. Reuses the OpenAI-compatible
  `/v1/chat/completions` endpoint with the `/v1` suffix appended
  automatically; the same provider also exposes native embeddings via
  `/api/embed` for skill and memory search. No API key needed.
- `codex` — the ChatGPT Codex backend (`https://chatgpt.com/backend-api/codex`).
  Uses OAuth (PKCE) login instead of a user-supplied API key; tokens are
  stored in the SQLite credential store under the provider ID and under
  `{providerID}:account:{accountID}` for multi-account failover.

**Base URL is required for `messages`, `responses`, and `chat`.** The UI
suggests a per-kind default (`https://api.anthropic.com` for Messages,
`https://api.openai.com/v1` for Responses and Chat) — replace it with any
endpoint or AI gateway that speaks the format. The operation path is
appended to the Base URL **verbatim**: whatever version your endpoint uses
(v1, v4, …) must live in the Base URL and is never injected. For Messages,
a bare compat root (e.g. `https://open.bigmodel.cn/api/anthropic`) gets the
Anthropic-compatible convention suffix `/v1/messages`, and a full endpoint
pasted as Base URL is used as-is. `ollama` defaults to
`http://localhost:11434` and `codex` defaults to
`https://chatgpt.com/backend-api/codex` when the field is left blank.

AI gateways (TokenRouter, LiteLLM, one-api and similar) can serve the first
three formats on one endpoint; configure one provider per format:

```text
Messages  → https://gateway.example.com        (→ /v1/messages)
Responses → https://gateway.example.com/v1     (→ /v1/responses)
Chat      → https://gateway.example.com/v1     (→ /v1/chat/completions)
```

## API keys

Only `messages` and `responses` require a user-supplied API key. `chat`
works without a key against local endpoints that need no auth. `ollama`
never needs a key (only set one when Ollama is behind an auth proxy).
`codex` uses OAuth tokens obtained via the Codex login flow — there is no
manual key field. Keys are stored in the SQLite credential store
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
(`GET /models`). The agent only offers imported models. Messages providers
bundle Claude model metadata (context window, pricing); imported models keep
the provider's own ids. Models tagged as embedding-capable appear in the
Embedding model setting for skill and memory search.

## Test connection

**Test connection** probes connectivity only: it lists models with
`GET /models` (responses/chat/ollama) or `GET /v1/models` (messages) and
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
newlines). NusaShell does **not** send LSP `Content-Length` headers — Gemini
CLI (`gemini --acp`) and the ACP spec parse each line with `JSON.parse`.
Logs from the agent belong on stderr.

### Common CLIs (install + auth)

| CLI | Command | Auth methods (typical) | Not logged in |
| --- | --- | --- | --- |
| Cursor | `curl https://cursor.com/install \| bash` then `agent acp` | `cursor_login` | `initialize` succeeds; `session/new` returns `Authentication required`. Run `agent login` locally, then **Authenticate** with `cursor_login` in Providers. |
| Codex (adapter) | `npm i @agentclientprotocol/codex-acp` → `codex-acp` | `api-key` (and ChatGPT login when browser available) | Same: probe works, spawn/refresh catalog fail until **Authenticate** (API key env or ChatGPT login). |
| Gemini | `npm i @google/gemini-cli` → `gemini --acp` | `oauth-personal`, `gemini-api-key`, … | Probe lists methods; session may still require **Authenticate** depending on local credentials. |

If you register a CLI without logging in, **Probe** still works (it only runs
`initialize`). **Refresh catalog** and `subagent` spawn call `session/new` and
fail until you complete **Authenticate** with one of the advertised method ids.
NusaShell surfaces a clear error naming those ids instead of a bare JSON-RPC
`Authentication required`.

**One login only (lazy auth):** After you **Authenticate** once, NusaShell
does not re-call `authenticate` on subsequent probes, catalog refreshes, or
subagent spawns. It tries `session/new` first and only calls `authenticate`
when the agent reports an auth-required error. Agents that persist their own
auth (e.g. Devin/Codex storing tokens in `~/.codex/auth.json`) will not
re-trigger the browser login flow on every new connection.
