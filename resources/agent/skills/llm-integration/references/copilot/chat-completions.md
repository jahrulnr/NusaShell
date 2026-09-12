# Copilot — Chat Completions

OpenAI Chat Completions shape via the Copilot API. Covers GPT non-responses
models and Gemini models. Claude models use a separate Anthropic Messages
transport — see [models.md](models.md) for routing.

> Derived from LiteLLM `litellm/llms/github_copilot/chat/transformation.py`
> and OpenClaw `extensions/github-copilot/{stream,model-metadata}.ts`.
> May change without notice. **Derived 2026-09-11.**

## Endpoint + auth

| Field | Value | Source |
|---|---|---|
| Base URL | `https://api.githubcopilot.com` (default) or account-specific `endpoints.api` | litellm `common_utils.py`, openclaw `runtime-auth.ts` |
| Path | `POST /chat/completions` | litellm `chat/transformation.py` |
| Auth | `Authorization: Bearer <copilot_token>` | litellm, openclaw |
| Env vars (GitHub token) | `COPILOT_GITHUB_TOKEN` > `GH_TOKEN` > `GITHUB_TOKEN` | openclaw `auth.ts`, hermes `__init__.py` |

## Request contract

```json
{
  "model": "gpt-5-mini",
  "messages": [
    {"role": "user", "content": "Hello"}
  ],
  "stream": true,
  "max_tokens": 4096
}
```

The body is standard OpenAI Chat Completions. LiteLLM's
`GithubCopilotConfig` extends `OpenAIConfig` directly — no field renames.

### System message handling

LiteLLM converts `system` messages to `assistant` by default for compatibility
(`disable_copilot_system_to_assistant = False`). OpenClaw does not do this.
Current Copilot endpoints accept system prompts for all model families
(litellm comment: "GitHub Copilot API now supports system prompts for all
models"). Prefer sending system messages as-is and only fall back to the
conversion if a model rejects them.

### Extra body: reasoning effort

Hermes sends reasoning effort via `extra_body.reasoning.effort` for models
that support it, gated by the live model catalog's supported efforts list
(hermes `copilot/__init__.py`).

## Response contract

Standard OpenAI Chat Completions response with `choices[].message` and
`usage`. For Claude models served via this endpoint (older path), the response
may arrive in Anthropic-native shape without a `choices` array — LiteLLM
synthesizes choices from `content` blocks, `stop_reason`, and `usage`
(`_synthesize_choices_for_anthropic_native` in `chat/transformation.py`).

## Dynamic headers per request

| Header | When | Source |
|---|---|---|
| `X-Initiator: agent` | Any message has role `tool` or `assistant` | litellm `_determine_initiator`, openclaw `stream.ts` |
| `X-Initiator: user` | All messages are `user`/`system` | same |
| `Copilot-Vision-Request: true` | Any message contains `image_url` content | litellm `_has_vision_content`, openclaw `stream.ts` |

## Transport selection

| Model family | Transport | Endpoint | Source |
|---|---|---|---|
| GPT (non-responses) | `openai-completions` | `/chat/completions` | openclaw `model-metadata.ts` |
| Gemini | `openai-completions` | `/chat/completions` | openclaw `resolveCopilotTransportApi` |
| GPT-5.x / Codex / o-series | `openai-responses` | `/responses` | openclaw, litellm `responses/transformation.py` |
| Claude | `anthropic-messages` | `/v1/messages` | openclaw, litellm `messages/transformation.py` |

## Workflow

1. Resolve GitHub token (device flow or env var) — see [README.md](README.md).
2. Exchange/validate → get Copilot token + base URL.
3. Build headers: editor headers + `X-Initiator` + vision if needed.
4. `POST /chat/completions` with standard OpenAI body.
5. Parse OpenAI response; track `usage`.
6. On 401 → re-exchange token, retry once.

## Edge cases

- **Gemini compat:** openclaw sets `supportsStore: false`,
  `supportsDeveloperRole: false`, `supportsUsageInStreaming: false`,
  `maxTokensField: "max_tokens"` for Gemini models on Copilot
  (`model-metadata.ts` `COPILOT_CHAT_COMPLETIONS_COMPAT`).
- **Tool IDs:** Anthropic tool IDs must match `^[a-zA-Z0-9_-]{1,64}$`; openclaw
  normalizes invalid IDs before sending (`stream.ts`).
- **Claude 4.5 eager tool streaming:** Copilot's Claude 4.5 endpoints reject
  Anthropic's eager tool extension; 4.6+ accepts it (openclaw
  `resolveCopilotModelCompat`).
- **No WebSocket:** Copilot Responses API does not support native WebSocket
  (litellm `supports_native_websocket` returns `False`).

## Error handling

See [errors.md](errors.md) for the full error envelope, token refresh, and
retry policy.

## Related files

- [README.md](README.md) — router + auth chain
- [models.md](models.md) — model catalog + transport selection
- [stream.md](stream.md) — SSE streaming
- [errors.md](errors.md) — error handling
- Public OpenAI Chat: [`../openai/chat-completions.md`](../openai/chat-completions.md)
