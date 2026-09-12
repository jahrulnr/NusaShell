# Bedrock — Model discovery & id patterns

Bedrock has **no `GET /models` on the runtime endpoint**. Model discovery uses
the **control plane** (`bedrock.{region}.amazonaws.com`) with
`ListFoundationModels` / `ListInferenceProfiles`, or the `bedrock-mantle`
OpenAI-compatible `/models` endpoint. Verified 2026-09-11; model list marked
**verify live** — Bedrock adds and deprecates models monthly.

## Endpoint + auth

| Action | Endpoint | Auth |
|---|---|---|
| List foundation models | `GET https://bedrock.{region}.amazonaws.com/foundation-models` | SigV4 (service `bedrock`) or bearer token |
| Get one model | `GET https://bedrock.{region}.amazonaws.com/foundation-model/{modelId}` | SigV4 / bearer |
| List inference profiles | `GET https://bedrock.{region}.amazonaws.com/inference-profiles` | SigV4 / bearer |
| Mantle OpenAI list | `GET https://bedrock-mantle.{region}.api.aws/v1/models` | Bearer token or SigV4 (service `bedrock-mantle`) |

curl (control plane, SigV4):
```bash
curl "https://bedrock.us-east-1.amazonaws.com/foundation-models" \
  --aws-sigv4 "aws:amz:us-east-1:bedrock" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY"
```

`ListFoundationModels` returns `FoundationModelSummary` objects: `modelId`,
`modelArn`, `modelName`, `providerName`, `inputModalities`,
`outputModalities`, `responseModalities`, `modelLifecycleStatus. It does
**not** return token-limit metadata — maintain a lookup table for context
windows / max output tokens (OpenClaw ships one; LiteLLM uses
`model_prices_and_context_window.json`).

## Request contract

Discovery is a GET with optional query params:
- `byProvider` — filter by provider (`anthropic`, `amazon`, `meta`, `mistral`,
  `cohere`, `ai21`, `stability`, `deepseek`, `writer`).
- `byInferenceType` — `ON_DEMAND` / `PROVISIONED`.
- `byOutputModality` / `byInputModality` — `TEXT` / `IMAGE` / `EMBEDDING`.

## Response contract (ListFoundationModels, abridged)

```json
{
  "modelSummaries": [
    {
      "modelId": "anthropic.claude-sonnet-5",
      "modelArn": "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-sonnet-5",
      "modelName": "Claude Sonnet 5",
      "providerName": "Anthropic",
      "inputModalities": ["TEXT", "IMAGE"],
      "outputModalities": ["TEXT"],
      "modelLifecycleStatus": "ACTIVE"
    }
  ]
}
```

## Model id patterns

Base id format: `{provider}.{model-name}-{version}:{iteration}`

| Provider | Prefix | Example |
|---|---|---|
| Anthropic | `anthropic.` | `anthropic.claude-sonnet-5`, `anthropic.claude-3-5-haiku-20241022-v1:0` |
| Amazon | `amazon.` | `amazon.nova-pro-v1:0`, `amazon.nova-lite-v1:0`, `amazon.nova-micro-v1:0`, `amazon.titan-text-premier-v1:0` |
| Meta | `meta.` | `meta.llama3-1-405b-instruct-v1:0`, `meta.llama3-2-90b-instruct-v1:0` |
| Mistral | `mistral.` | `mistral.mistral-large-3-675b-instruct` |
| Cohere | `cohere.` | `cohere.command-r-v1:0`, `cohere.command-r-plus-v1:0` |
| AI21 | `ai21.` | `ai21.jamba-1-5-large-v1:0` |
| DeepSeek | `deepseek.` | `deepseek.r1-v1:0` |
| Stability | `stability.` | `stability.stable-image-core-v1:0` (image) |

### Inference profiles (cross-region capacity)

Prefix the base id with a geo scope to route across regions for better
capacity / failover:

| Prefix | Scope | Example |
|---|---|---|
| `us.` | US cross-region | `us.anthropic.claude-sonnet-5` |
| `eu.` | EU cross-region | `eu.anthropic.claude-sonnet-5` |
| `apac.` | Asia Pacific cross-region | `apac.anthropic.claude-sonnet-5` |
| `ap.` / `jp.` / `au.` | Regional (some models) | `ap.anthropic.claude-sonnet-5` |
| `global.` | Global cross-region (limited models) | `global.anthropic.claude-sonnet-5` |

Application inference profiles are full ARNs
(`arn:aws:bedrock:{region}:{account}:inference-profile/...`) — pass the ARN as
`modelId`. LiteLLM detects ARN-shaped ids and keeps the region from the ARN.

## Snapshot — popular models (verify live)

> **verify live** — re-check `ListFoundationModels` before finalizing. Region
> availability varies; not all models are enabled in every account/region.

| Model | Base id | Context | Max output | Modalities | Notes |
|---|---|---|---|---|---|
| Claude Sonnet 5 | `anthropic.claude-sonnet-5` | 1,000,000 | 128,000 | text+image in, text out | Adaptive thinking always on; `bedrock-runtime` + `bedrock-mantle` |
| Claude Opus 5 | `anthropic.claude-opus-5` | 1,000,000 | 128,000 | text+image in | Rejects `temperature`; effort `xhigh`/`max` native |
| Claude Fable 5 | `anthropic.claude-fable-5` | 1,000,000 | 128,000 | text+image in | Requires `provider_data_share` opt-in; refusal-safe streaming |
| Claude 3.5 Haiku | `anthropic.claude-3-5-haiku-20241022-v1:0` | 200,000 | 8,192 | text+image in | Fast/cheap |
| Claude 3.5 Sonnet v2 | `anthropic.claude-3-5-sonnet-20241022-v2:0` | 200,000 | 8,192 | text+image in | Legacy |
| Nova Pro | `amazon.nova-pro-v1:0` | 300,000 | 8,192 | text+image+video in | Amazon flagship |
| Nova Lite | `amazon.nova-lite-v1:0` | 300,000 | 8,192 | multimodal in | Cheaper Nova |
| Nova Micro | `amazon.nova-micro-v1:0` | 128,000 | 8,192 | text in | Smallest Nova |
| Llama 3.1 405B | `meta.llama3-1-405b-instruct-v1:0` | 128,000 | 4,096 | text in | Open weights |
| Mistral Large 3 | `mistral.mistral-large-3-675b-instruct` | 128,000 | 8,192 | text in | |
| DeepSeek R1 | `deepseek.r1-v1:0` | 128,000 | 32,768 | text in | Reasoning; emits `<think>` in text |

## Workflow

1. Resolve region from `AWS_REGION` / `AWS_DEFAULT_REGION` (default `us-east-1`).
2. Call `ListFoundationModels` (+ `byProvider` if you know the family).
3. Call `ListInferenceProfiles` for cross-region capacity options.
4. Confirm **model access** is enabled in the AWS console for your
   account/region (Bedrock requires explicit opt-in per model).
5. Pin the exact base id or inference profile id in production; use
   `-latest`-style aliases only in dev (Bedrock does not have `-latest`
   aliases — pin the full id including `:0` iteration).
6. Maintain a local context-window / max-output-token table since the API
   does not return them.

## Edge cases

- **No token-limit metadata from the API.** `ListFoundationModels` returns
  only id/name/modalities/lifecycle. Hardcode known limits or fall back to
  conservative defaults (OpenClaw: `defaultContextWindow: 32000`,
  `defaultMaxTokens: 4096`).
- **Model access is per-account-per-region.** A model appearing in
  `ListFoundationModels` does not mean you can invoke it — request access in
  the Bedrock console first. `AccessDeniedException` on invoke usually means
  access not enabled.
- **`:0` iteration suffix is significant.** `anthropic.claude-3-5-sonnet-...-v2:0`
  vs `:1` can be different model versions. Always include the iteration.
- **Deprecation.** `modelLifecycleStatus` can be `ACTIVE` / `LEGACY` /
  `DISCONTINUING`. Avoid `DISCONTINUING` for new builds.
- **Mantle vs Runtime model sets differ.** `bedrock-mantle` serves a subset
  (OpenAI-compat + Anthropic-native); `bedrock-runtime` serves all Converse
  models. Check the right endpoint for your surface.
- **Region availability.** A model id valid in `us-east-1` may not exist in
  `ap-southeast-4`. Use an inference profile to cross-region route.

## Error handling

See [errors.md](errors.md). `ValidationException` ("The provided model
identifier is invalid") often means a wrong region, a missing `:0` suffix, or
an unsupported `serviceTier` — not always a bad model id.
