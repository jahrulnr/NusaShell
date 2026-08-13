# Providers

Providers are the LLM backends the agent chats through. A provider is
defined by its **API wire format**, not by a vendor: **Messages**,
**Responses** or **Chat**.

## Kinds

- `messages` — the Anthropic Messages wire format (`/v1/messages`); supports
  prompt caching via `cache_control`.
- `responses` — the OpenAI Responses wire format (`/v1/responses`); supports
  function calling and reports cached input tokens.
- `chat` — the OpenAI Chat Completions wire format (`/chat/completions`);
  works with OpenAI, DeepSeek, Ollama, LM Studio, vLLM and any compatible
  endpoint.

**Base URL is required.** The UI suggests a per-kind default
(`https://api.anthropic.com` for Messages, `https://api.openai.com/v1` for
Responses and Chat) — replace it with any endpoint or AI gateway that speaks
the format. The operation path is appended to the Base URL **verbatim**:
whatever version your endpoint uses (v1, v4, …) must live in the Base URL
and is never injected. For Messages, a bare compat root (e.g.
`https://open.bigmodel.cn/api/anthropic`) gets the Anthropic-compatible
convention suffix `/v1/messages`, and a full endpoint pasted as Base URL is
used as-is.

AI gateways (TokenRouter, LiteLLM, one-api and similar) can
serve all three formats on one endpoint; configure one provider per format:

```text
Messages  → https://gateway.example.com        (→ /v1/messages)
Responses → https://gateway.example.com/v1     (→ /v1/responses)
Chat      → https://gateway.example.com/v1     (→ /v1/chat/completions)
```

## API keys

Keys are stored in the SQLite credential store (`credentials.db`) inside the
data directory, never in the JSON files. Messages and Responses providers
need a key; a Chat provider without a key still works against local
endpoints that need no auth.

## Models

After saving a provider, use **Import models** to fetch its model list
(`GET /models`). The agent only offers imported models. Messages providers
bundle Claude model metadata (context window, pricing); imported models keep
the provider's own ids.

## Test connection

**Test connection** probes connectivity only: it lists models with
`GET /models` (responses/chat) or `GET /v1/models` (messages) and reports
latency and the model count. No completion is sent, so the probe never
costs tokens, works before importing models, and does not trip
model-routing failures on the upstream — a broken model only surfaces when
you actually chat with it.
