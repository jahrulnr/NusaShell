# OpenCode — Native Gemini protocol lowering

How OpenCode lowers canonical parts onto Gemini’s **native** wire.
The HTTP API itself (endpoints, quotas, Google-side edge cases) lives in
`../gemini/` — read that for curl-level contracts.

## Endpoint + auth

- URL: `{baseURL}/models/{model.id}:streamGenerateContent?alt=sse`
- Default base: `https://generativelanguage.googleapis.com/v1beta`
- Auth header: `x-goog-api-key` (from API key options / Google catalog envs)
- Framing: SSE

## Request contract (lowering)

| Canonical | Gemini wire |
|---|---|
| `system` parts | `systemInstruction.parts[].text` |
| user `text` / `media` | `contents[]` role `user` — `text` or `inlineData` |
| assistant `text` | `parts[].text` |
| assistant `reasoning` | `parts[].text` + `thought: true` + optional `thoughtSignature` from `providerMetadata.google` |
| assistant `tool-call` | `parts[].functionCall` + optional `thoughtSignature` |
| tool results | `contents[]` role `user` with `functionResponse` (+ optional `inlineData` for files) |
| mid-turn `system` update | Appended as wrapped user text (`<system-update>…`) |

Thinking options (native protocol namespace):

```json
"providerOptions": {
  "gemini": {
    "thinkingConfig": {
      "includeThoughts": true,
      "thinkingBudget": 0
    }
  }
}
```

Native extract currently honors **`thinkingBudget` / `includeThoughts` only**.
`thinkingLevel` is used on the AI SDK Google path via `providerOptions.google`
for Gemini 3 — bridge before enabling native Google.

## Response contract (stream)

- `thought: true` text → reasoning deltas (signature kept in metadata)
- Non-thought text → end reasoning → text deltas
- `functionCall` → end reasoning → tool-call event with
  `providerMetadata.google.thoughtSignature` when present
- Usage: `thoughtsTokenCount` → `reasoningTokens`; inclusive output tokens =
  visible candidates + thoughts when both are reported

## Workflow

1. Raw curl / API semantics → `../gemini/generate-content.md` +
   `../gemini/thinking.md`.
2. Session history replay → keep signatures on reasoning / tool-call
   `providerMetadata`.
3. Do not lower Google through [openai-chat.md](openai-chat.md).

## Edge cases

- Session default for Google is still **AI SDK**, not this protocol
  ([architecture.md](architecture.md)).
- `providerOptions.google` (AI SDK) ≠ `providerOptions.gemini` (native) —
  translate before enabling native Google.
- Avoid illegal role alternation when constructing `contents` (user/model
  only; merge same-role turns upstream if needed).

## Error handling

Unsupported part types → invalid request locally. Google HTTP errors →
`../gemini/errors.md`.
