# OpenRouter guides (ported)

Language-agnostic HTTP guidance ported from
[OpenRouterTeam/skills](https://github.com/OpenRouterTeam/skills)
(`openrouter-*` API skills). Read **only** the file for the API you need.

TS SDK / agent scaffolding skills (`openrouter-typescript-sdk`,
`openrouter-agent-migration`, `create-*-agent`, `*-ori-*`) are **not** ported —
use upstream if you need those. The **Open Responses** standard
(provider-agnostic `/v1/responses` protocol, upstream `open-responses` skill,
openresponses.org) is also upstream-only — consult it when implementing or
consuming a compliant endpoint.

## Router

| Need | Read |
|---|---|
| Chat completions + OR-only params | [chat-completion.md](chat-completion.md) |
| SSE streaming quirks | [stream.md](stream.md) |
| Tool calling | [tools.md](tools.md) |
| Model discovery / endpoints / pricing | [model-list.md](model-list.md) |
| Image generate/edit | [images.md](images.md) |
| Speech → text (JSON+base64 STT) | [stt.md](stt.md) |
| Text → speech (raw bytes TTS) | [tts.md](tts.md) |
| Async video | [video.md](video.md) |
| Browser PKCE sign-in | [oauth.md](oauth.md) |
| Per-request cost/latency/content | [generations.md](generations.md) |
| Usage / spend (management key) | [analytics.md](analytics.md) |
| Benchmark rankings | [benchmarks.md](benchmarks.md) → [benchmarks-api.md](benchmarks-api.md) |
| Errors / 402 / retries | [errors.md](errors.md) |

Parent skill routing: `../../SKILL.md`.
OpenAI counterparts: `../openai/`.
Provider matrix: `../_shared/provider-matrix.md`.
Optional implementation cross-check: `../_shared/sample-implementations.md`
(LiteLLM `llms/openrouter/` + cookbook notebook).
