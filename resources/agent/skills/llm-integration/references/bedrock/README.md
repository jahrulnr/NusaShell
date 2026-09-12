# AWS Bedrock guides

Language-agnostic HTTP guidance for **Amazon Bedrock** — the AWS-hosted
multi-model inference platform. Bedrock is **not** an OpenAI-shaped API: it
uses AWS SigV4 signing (or a Bedrock bearer token), region-scoped endpoints,
and a normalized **Converse** surface that works across Anthropic Claude,
Amazon Nova, Meta Llama, Mistral, Cohere, AI21, Stability, and DeepSeek
models. Verified against live AWS docs (verified 2026-09-11) plus LiteLLM,
OpenClaw, and Hermes reference implementations.

Prefer the **Converse** / **ConverseStream** API for new agent and chat work —
it is the only surface that gives a consistent request/response shape across
all Bedrock models. Use **InvokeModel** only when you need a model's native
raw body (e.g. provider-specific params Converse does not expose).

Read **only** the file for the API you need.

## Router

| Need | Read |
|---|---|
| Converse request/response contract | [converse.md](converse.md) |
| ConverseStream framing + parsing | [stream.md](stream.md) |
| Tool use (toolConfig / toolUse / toolResult) | [tools.md](tools.md) |
| Model discovery / id patterns / snapshot | [model-list.md](model-list.md) |
| Errors / exceptions / retry policy | [errors.md](errors.md) |

Parent skill routing: `../../SKILL.md`.
OpenAI / Anthropic counterparts: `../openai/`, `../anthropic/`.
Provider matrix: `../_shared/provider-matrix.md`.
Optional implementation cross-check: `../_shared/sample-implementations.md`
(LiteLLM `llms/bedrock/`).

## Endpoint + auth

| Item | Value |
|---|---|
| Runtime base | `https://bedrock-runtime.{region}.amazonaws.com` |
| Control plane base | `https://bedrock.{region}.amazonaws.com` (ListFoundationModels, GetInferenceProfile) |
| Mantle base (OpenAI-compat / Anthropic-native) | `https://bedrock-mantle.{region}.api.aws` |
| Converse path | `/model/{modelId}/converse` |
| ConverseStream path | `/model/{modelId}/converse-stream` |
| InvokeModel path | `/model/{modelId}/invoke` |
| Anthropic Messages passthrough | `/model/{modelId}/invoke` (raw) or `/anthropic/v1/messages` on runtime |
| OpenAI-compat | `/openai/v1/...` on runtime or mantle |

**Auth (one of):**

| Method | How | Notes |
|---|---|---|
| **AWS SigV4** (default) | Sign request with service name `bedrock`, region from `AWS_REGION` / `AWS_DEFAULT_REGION`. curl: `--aws-sigv4 "aws:amz:{region}:bedrock" --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY"` (+ `X-Amz-Security-Token` for STS). | Uses the AWS SDK default credential chain: env vars, shared config (`AWS_PROFILE`), SSO, instance role (IMDS), web identity. **Recommended for production.** |
| **Bedrock bearer token** | `Authorization: Bearer $AWS_BEARER_TOKEN_BEDROCK` header. | Bedrock API key — **short-term** (up to 12h, session-bound) or **long-term** (configured expiry; IAM-user-backed, AWS recommends exploration only). No SigV4 signing needed. Limited to Bedrock runtime actions; not for Agents/Automation APIs. |
| **AWS SDK** | `boto3.client("bedrock-runtime", region_name=...)` etc. | Handles signing + credential refresh automatically. Preferred when not hand-rolling HTTP. |

Env vars (names only, never values): `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_REGION`,
`AWS_DEFAULT_REGION`, `AWS_PROFILE`, `AWS_BEARER_TOKEN_BEDROCK`.

Required IAM permissions: `bedrock:InvokeModel` (Converse),
`bedrock:InvokeModelWithResponseStream` (ConverseStream),
`bedrock:ListFoundationModels` + `bedrock:ListInferenceProfiles` (discovery),
`bedrock:ApplyGuardrail` (if guardrails).

## Diffs vs OpenAI / Anthropic native

| Concern | OpenAI / Anthropic | Bedrock Converse |
|---|---|---|
| Auth | `Authorization: Bearer` / `x-api-key` | **SigV4** or Bedrock bearer token (not a plain API key) |
| Base URL | Single global host | **Region-scoped** `bedrock-runtime.{region}.amazonaws.com` |
| Request body | OpenAI `messages`+`tools` / Anthropic `messages`+`system` | `messages` + top-level `system` + `inferenceConfig` + `toolConfig` |
| Model id | In body (`model`) | **In URL path** (`/model/{modelId}/converse`) |
| Streaming | SSE (`text/event-stream`) | **AWS binary event-stream** (`application/vnd.amazon.eventstream`) — NOT SSE |
| Tools | `tool_calls` / `tool_use` | `toolUse` block + `toolResult` block; schema under `toolSpec.inputSchema.json` |
| Thinking | `reasoning` / `thinking` blocks | `reasoningContent` blocks with `signature` (replay unmodified) |
| Caching | `cache_control` / automatic | Prompt caching via `cachePoint` blocks (Anthropic models on Bedrock) |
| Errors | HTTP status + JSON body | AWS error envelope (`__type` exception class + `message`) |

## Runtime notes (OpenClaw / Hermes)

| Runtime | Provider id | API | Notes |
|---|---|---|---|
| OpenClaw | `amazon-bedrock` | `bedrock-converse-stream` | AWS SDK default chain; auto-discovery via ListFoundationModels + ListInferenceProfiles; inference profiles inherit backing model capabilities |
| Hermes | `bedrock` (aliases `aws`, `aws-bedrock`, `amazon`) | `bedrock_converse` | No REST `/v1/models`; model listing requires AWS SDK, returns `None` from fetch_models |

How OpenCode lowers providers -> `../opencode/`. Cross-runtime hooks -> `../agent-hooks/`.

## Decision defaults

- New Bedrock build -> **Converse** API (`converse.md`), not InvokeModel.
- User-facing chat -> **ConverseStream** (`stream.md`); remember it is binary
  event-stream framing, not SSE — do not parse with an SSE reader.
- Need Claude with Anthropic-native tool types (computer/bash/text_editor) ->
  Anthropic Messages passthrough on `/anthropic` (see `converse.md` edge cases).
- Need OpenAI SDK against Bedrock -> `/openai/v1` compat on runtime or mantle
  (SigV4 or bearer token; not the Converse shape).
- Cross-region capacity -> use an **inference profile** id (`us.`, `eu.`,
  `apac.`, `global.` prefix) as the modelId (`model-list.md`).
