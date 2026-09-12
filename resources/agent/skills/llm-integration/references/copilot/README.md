# GitHub Copilot backend compatibility guides

Language-agnostic HTTP guidance for the **GitHub Copilot API**
(`api.githubcopilot.com`), derived from open-source clients. This is an
**unofficial, undocumented compatibility surface** that proxies OpenAI,
Anthropic, and Google models through a single Copilot-token-authenticated
endpoint.

> **Scope warning:** This is NOT the public OpenAI API (`api.openai.com`), NOT
> the public GitHub REST API, and NOT the ChatGPT/Codex backend
> (`chatgpt.com/backend-api`). Never mix Copilot-token auth with platform
> API-key contracts (`../openai/`), ChatGPT OAuth (`../codex/`), or GitHub REST
> in one client path. Every claim below is derived from source code, not
> official docs, and may change without notice. **Derived 2026-09-11.**

For building an application with GitHub Copilot, prefer the supported
[Copilot SDK](https://docs.github.com/en/copilot/how-tos/copilot-sdk) and its
documented authentication modes. Use this reference only when the user
explicitly requests direct backend compatibility, migration from an existing
unofficial client, or forensic debugging. Do not present these endpoints as a
stable public API, and do not ask the user to paste GitHub or Copilot tokens.

## Sources

| Source | Path | What it proves |
|---|---|---|
| LiteLLM (MIT, BerriAI) | `litellm/llms/github_copilot/{authenticator,common_utils,chat/transformation,messages/transformation,responses/transformation,embedding/transformation}.py` | Auth chain, headers, chat/responses/messages/embeddings transforms |
| OpenClaw | `extensions/github-copilot/{auth,login,models,stream,runtime-auth,domain,runtime-identity,model-metadata,replay-policy,usage}.ts` + `src/agents/copilot-dynamic-headers.ts` + `docs/providers/github-copilot.md` | Device flow, runtime auth, model catalog, streaming, replay policy |
| Hermes (Nous Research) | `plugins/model-providers/{copilot,copilot-acp}/__init__.py` | Provider profile, env-var chain, per-model api_mode routing |

## Auth chain

```
GitHub OAuth (device flow or PAT)
  │  env: COPILOT_GITHUB_TOKEN > GH_TOKEN > GITHUB_TOKEN
  ▼
GitHub access token (long-lived, user-scoped, read:user)
  │  GET /copilot_internal/v2/token  (legacy: returns short-lived Copilot token)
  │  GET /copilot_internal/user      (current: validates + returns endpoints.api)
  ▼
Copilot API token (short-lived ~30min, or direct GitHub token on current path)
  │  Authorization: Bearer <token>
  ▼
https://api.githubcopilot.com  (or account-specific endpoints.api URL)
```

| Step | Endpoint | Auth | Source |
|---|---|---|---|
| Device code | `POST https://github.com/login/device/code` | none; `client_id=Iv1.b507a08c87ecfe98`, `scope=read:user` | litellm `authenticator.py`, openclaw `login.ts` |
| Poll access token | `POST https://github.com/login/oauth/access_token` | none; `grant_type=urn:ietf:params:oauth:grant-type:device_code` | litellm, openclaw |
| Token exchange (legacy) | `GET https://api.github.com/copilot_internal/v2/token` | `Authorization: token <github_token>` | litellm `authenticator.py` |
| Token validation (current) | `GET https://api.github.com/copilot_internal/user` | `Authorization: Bearer <github_token>` | openclaw `runtime-auth.ts` |
| Inference | `POST https://api.githubcopilot.com/{chat/completions,responses,v1/messages}` | `Authorization: Bearer <copilot_token>` | all sources |

**Token refresh:** 401 on inference → re-exchange via `/copilot_internal/v2/token`
(litellm: `APIKeyExpiredError` → `_refresh_api_key`). The exchange response
includes `expires_at` (unix timestamp) and `endpoints.api` (account-specific
base URL). openclaw's current path skips the exchange and sends the GitHub
token directly as Bearer, resolving the base URL from `/copilot_internal/user`.

**GHE data residency:** domain `*.ghe.com` → device flow at
`https://{domain}/login/device/code`, inference at
`https://copilot-api.{domain}`. Override via `COPILOT_GITHUB_DOMAIN` env
(openclaw `domain.ts`).

## Required editor headers

| Header | Example value | Source |
|---|---|---|
| `Editor-Version` | `vscode/1.107.0` | openclaw `copilot-dynamic-headers.ts`; litellm uses `vscode/1.95.0` |
| `Editor-Plugin-Version` | `copilot-chat/0.35.0` | openclaw; litellm uses `copilot-chat/0.26.7` |
| `Copilot-Integration-Id` | `vscode-chat` (litellm, openclaw SDK) / `copilot-developer-cli` (openclaw runtime) | litellm `common_utils.py`, openclaw `runtime-identity.ts` |
| `User-Agent` | `GitHubCopilotChat/0.35.0` | openclaw; litellm uses `GitHubCopilotChat/0.26.7` |
| `X-Github-Api-Version` | `2025-04-01` | litellm, openclaw |
| `Openai-Intent` | `conversation-panel` (chat) / `messages-proxy` (messages) | litellm `common_utils.py`, `messages/transformation.py` |
| `X-Initiator` | `agent` or `user` (based on message roles) | litellm `chat/transformation.py`, openclaw `stream.ts` |
| `Copilot-Vision-Request` | `true` (when input contains images) | litellm, openclaw |
| `Openai-Organization` | `github-copilot` | openclaw `runtime-identity.ts` |

Header values drift across client versions; the exact strings are not
contract. Use recent values and verify live.

## Router

| Need | Read |
|---|---|
| OpenAI Chat Completions shape (GPT, Gemini) | [chat-completions.md](chat-completions.md) |
| Model catalog + transport selection | [models.md](models.md) |
| SSE streaming (all three transports) | [stream.md](stream.md) |
| Error envelope + token refresh + retry | [errors.md](errors.md) |

Parent routing: `../../SKILL.md`. Public OpenAI: `../openai/`.
ChatGPT/Codex backend: `../codex/`. Anthropic native: `../anthropic/`.
Optional implementation cross-check: LiteLLM `litellm/llms/github_copilot/` — see
`../_shared/sample-implementations.md`.
