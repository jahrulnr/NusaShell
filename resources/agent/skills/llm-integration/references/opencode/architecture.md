# OpenCode — Architecture (AI SDK vs native)

## Endpoint + auth

Not a single API host. OpenCode routes by the model’s SDK package id
(`model.api.npm` in its catalog):

| SDK package id | Default session path | Native protocol route |
|---|---|---|
| `@ai-sdk/google` | **AI SDK** stream | Native Gemini protocol exists in the LLM client library, but session native runtime is **not** opted in for Google yet |
| `@ai-sdk/openai` / Azure | AI SDK, or native when experimental native LLM is on | OpenAI Responses / Chat |
| `@ai-sdk/anthropic` | AI SDK, or native when flag on | Anthropic Messages |
| `@ai-sdk/openai-compatible` | AI SDK (native only for allowed providers) | Reuses OpenAI Chat protocol |

Google catalog auth env (first present wins): `GOOGLE_API_KEY`,
`GOOGLE_GENERATIVE_AI_API_KEY`, `GEMINI_API_KEY`.

## Request contract (conceptual)

```text
session messages (SDK-shaped)
  → message / providerOptions transforms
  → either:
      (A) AI SDK streamText          # default for google today
      (B) native LLM client stream   # opt-in; openai/anthropic/opencode only
```

Only the native-request bridge constructs canonical LLM request objects from
session/SDK shapes. Keep provider quirks in protocol lowerers — not in the
session message store.

## Response contract

Both paths normalize to a shared event stream: text / reasoning / tool
deltas, finish, usage. AI SDK path converts provider stream parts; native
path emits events from each protocol’s stream state machine.

## Workflow

1. Identify `providerID` + SDK package id for the active model.
2. If Google → assume AI SDK unless you are extending native opt-in.
3. For wire field questions → [reasoning.md](reasoning.md), then
   [openai-chat.md](openai-chat.md) or [gemini-protocol.md](gemini-protocol.md).
4. For outbound headers → [headers.md](headers.md).

## Edge cases

- Experimental native LLM + Google → native runtime reports unsupported
  (only openai / anthropic / opencode are opted in) and **falls back to AI
  SDK**. Do not assume the native Gemini protocol is live in a normal session.
- Native Gemini options key is `providerOptions.gemini`; AI SDK Google key is
  `providerOptions.google`. Bridging without translation drops thinkingConfig
  ([provider-transform.md](provider-transform.md)).

## Error handling

Runtime selection should log which path ran (`native` vs `ai-sdk`) and any
native-unsupported reason. Treat AI SDK fallback as intentional, not a silent
protocol swap mid-debug.
