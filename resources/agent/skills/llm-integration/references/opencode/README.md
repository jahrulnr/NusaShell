# OpenCode runtime appendix

How the **OpenCode** coding-agent client prepares LLM requests: outbound
headers, dual runtimes (AI SDK vs native protocols), and how canonical
message parts lower onto provider wires (`reasoning_content` vs Gemini
`thought`, etc.).

This tree is an **optional client/runtime reference** — not a provider HTTP
API and not part of the default API integration path.
For Google’s Generative Language contract, use `../gemini/`.
For public OpenAI / OpenRouter APIs, use `../openai/` / `../openrouter/`.

Read [the shared hook reference](../_shared/hooks.md) or
the provider tree first when the task is about an API contract. Read **only**
the file for the OpenCode concern you need. Do not open an OpenCode
checkout to follow these guides — everything needed is in this tree (plus
sibling provider refs above).

## Router

| Need | Read |
|---|---|
| Dual runtime (AI SDK vs native) | [architecture.md](architecture.md) |
| Outbound HTTP headers | [headers.md](headers.md) |
| Canonical reasoning vs visible text | [reasoning.md](reasoning.md) |
| OpenAI Chat protocol (`reasoning_content`) | [openai-chat.md](openai-chat.md) |
| Native Gemini protocol lowering | [gemini-protocol.md](gemini-protocol.md) |
| Message / options transforms | [provider-transform.md](provider-transform.md) |

Parent skill: `../../SKILL.md`.
Agent hooks (Codex/OpenClaw/Hermes): `../agent-hooks/`.
