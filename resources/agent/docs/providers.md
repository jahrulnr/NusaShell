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
