# Azure OpenAI / Microsoft Foundry guides

Language-agnostic HTTP guidance for **Azure OpenAI Service** (the hosted
OpenAI data-plane on Azure) and **Microsoft Foundry** (the unified Azure AI
platform that hosts the same OpenAI endpoints plus non-OpenAI models).

**This is the Azure-hosted surface**, not public OpenAI (`api.openai.com`).
The request/response JSON is OpenAI-shaped, but the host, auth, URL path, and
content-filtering behavior differ. See "Diffs vs public OpenAI" below.

Verified 2026-09-11 against `learn.microsoft.com` (Azure OpenAI reference,
Responses API how-to updated 2026-08-18, content-filter docs) and cross-checked
against LiteLLM `litellm/llms/azure/**` and the OpenClaw `microsoft-foundry`
extension.

## Auth (two methods, never mix in one client path)

| Method | Header | Env var | When |
|---|---|---|---|
| **API key** | `api-key: $AZURE_OPENAI_API_KEY` | `AZURE_OPENAI_API_KEY` | Simplest; resource-scoped key from Azure portal/Foundry |
| **Microsoft Entra ID** | `Authorization: Bearer $AZURE_OPENAI_AUTH_TOKEN` | `AZURE_OPENAI_AUTH_TOKEN` (or token provider) | Recommended for production; RBAC, managed identity, no long-lived key |

- Entra ID token scope: `https://cognitiveservices.azure.com/.default`
  (resource-scoped) or `https://ai.azure.com/.default` (Foundry). Acquire via
  `az login`, `DefaultAzureCredential`, managed identity, or OIDC client
  credentials (`AZURE_CLIENT_ID` + `AZURE_TENANT_ID` + client assertion).
- **Never send both `api-key` and `Authorization`** in one request.
- Never log/echo keys or bearer tokens.

## Host + URL model

Resource host: `https://{resource}.openai.azure.com` (replace `{resource}` with
your Azure OpenAI resource name). Foundry may also expose
`https://{resource}.services.ai.azure.com` for non-OpenAI models (out of scope
here; this tree covers the OpenAI-compatible endpoints).

Two path styles coexist:

| Style | Path | `api-version` | Status |
|---|---|---|---|
| **Deployment-based (date-versioned)** | `/openai/deployments/{deployment}/chat/completions?api-version=YYYY-MM-DD` | date string, **required** | Legacy + still widely used |
| **v1 stable (Foundry Models API)** | `/openai/v1/chat/completions?api-version=v1` | `v1` (or `preview`); optional, defaults `v1` | New GA surface; Responses API lives here |

- `api-version` is a **required query param** on the date-versioned path and on
  most operations. The v1 path makes it optional (defaults to `v1`).
- The deployment-based path puts the **deployment name** in the URL; the v1 path
  puts the **model/deployment name** in the JSON `model` field (like public
  OpenAI).
- Current GA data-plane: `v1`. Current preview: `v1 preview`. Control-plane GA:
  `2025-06-01`; preview `2025-07-01-preview`. Re-verify live before pinning.

## Diffs vs public OpenAI (`../openai/`)

| Concern | Public OpenAI | Azure OpenAI |
|---|---|---|
| Host | `api.openai.com` | `{resource}.openai.azure.com` |
| Auth | `Authorization: Bearer` | `api-key` header **or** `Authorization: Bearer` (Entra) |
| Versioning | URL path `/v1/`, no query | `api-version` query param **required** (or `/openai/v1/` + `v1`) |
| Model identity | `model` in body = model id | deployment name in URL path (date style) **or** `model` = deployment name (v1 style); deployment != model |
| Content filtering | optional, server-side | **always on** by default; can return 400 `content_filter` + `innererror`; 200 carries `prompt_filter_results`/`content_filter_results` |
| Quota model | org/project tiers (TPM/RPM) | PTU (provisioned) vs standard (token-based) per deployment |
| Responses API | `/v1/responses` | `/openai/v1/responses` (v1) or `/openai/responses?api-version=...` (date); no `context_management`/compaction; flattened tools; no `status` field on items |
| `store` / persisted responses | supported | Responses API supported but `store` semantics differ; Foundry validates encrypted reasoning replay against item id |

## Router

| Need | Read |
|---|---|
| Chat Completions (deployment-based or v1) | [chat-completions.md](chat-completions.md) |
| Responses API (new work, v1) | [responses.md](responses.md) |
| SSE streaming (both APIs) | [stream.md](stream.md) |
| Function calling / tools | [tools.md](tools.md) |
| Deployments, model catalog, PTU vs standard | [deployments-and-models.md](deployments-and-models.md) |
| Errors / content_filter / retries / rate limits | [errors.md](errors.md) |

Read **only** the file for the API you need (split-read rule). New builds on
Azure -> prefer the **Responses API** on the `/openai/v1/` path, mirroring the
public OpenAI decision default.

Optional implementation cross-check: `../_shared/sample-implementations.md`
(LiteLLM `llms/azure/`).
Parent skill routing: `../../SKILL.md`. Provider matrix: `../_shared/provider-matrix.md`.
Public OpenAI counterpart (different host/auth): `../openai/`.
