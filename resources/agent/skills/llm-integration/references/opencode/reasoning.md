# OpenCode — Reasoning vs content (canonical)

## Endpoint + auth

N/A — this is the **in-process** part model shared by all protocols.
Wire fields differ per protocol; never assume one JSON key for all providers.

## Request contract (canonical parts)

Assistant turns use typed parts:

| Part `type` | Meaning |
|---|---|
| `text` | User-visible answer |
| `reasoning` | Thinking / scratch (may carry `providerMetadata`) |
| `tool-call` | Function invocation (+ optional `providerMetadata`) |

`providerMetadata.google.thoughtSignature` (and siblings) must survive
round-trip for Gemini tool/image turns.

## Response contract (events)

Stream normalizes to:

- `reasoning-start` / `reasoning-delta` / `reasoning-end`
- `text-start` / `text-delta` / `text-end`
- `tool-call` …

First visible `text` or tool event ends the open reasoning span.

## Workflow

1. Keep reasoning and text as **separate parts** in session history.
2. When lowering to a provider, pick the protocol map:
   - OpenAI Chat / openai-compatible → [openai-chat.md](openai-chat.md)
     (`reasoning_content` vs `content`)
   - Gemini native → [gemini-protocol.md](gemini-protocol.md)
     (`thought` + `thoughtSignature` vs plain `text`)
3. Never copy Gemini thought parts into `reasoning_content`, and never strip
   signatures when the next turn is still Google.

## Edge cases

| Provider family | Replay rule |
|---|---|
| DeepSeek-style openai-compatible | `capabilities.interleaved.field: "reasoning_content"` → hoist out of `content[]` into `providerOptions.openaiCompatible.reasoning_content` (field always set, even if empty) |
| Gemini 3 | Signatures required on tool-call parts |
| Cross-provider model switch mid-session | Re-lower; do not reuse the previous provider’s wire fields |

## Error handling

Mixing wires surfaces as provider **400** (unknown field / missing signature)
or silent quality loss. Fix the lowerer; do not blind-retry.
