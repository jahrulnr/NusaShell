# OpenCode — Message / options transforms

Request-side rewrites before the runtime call (interleaved reasoning hoist,
Google thinking options, Gemini tool-schema sanitize).

## Endpoint + auth

N/A — pure message/options rewrite.

## Request contract (message rewrite)

### Interleaved reasoning hoist

When the model advertises `capabilities.interleaved` as an object with
`field` (and is not using the OpenRouter SDK package id):

1. Find assistant messages with array `content`.
2. Collect parts with `type === "reasoning"` → join text.
3. Remove those parts from `content`.
4. Set `providerOptions.openaiCompatible[field] = reasoningText`
   (**always set**, even if empty string).

Typical `field`: `"reasoning_content"`. That feeds
[openai-chat.md](openai-chat.md) via openai-compatible native metadata.

### Google thinking options

For Google / Vertex AI SDK package ids, when the model has reasoning
capability, options include:

```json
{
  "thinkingConfig": {
    "includeThoughts": true,
    "thinkingLevel": "high"
  }
}
```

- Gemini **3** ids → `thinkingLevel` (allowed efforts depend on flash / pro /
  image).
- Gemini **2.5** variants → `thinkingBudget` (+ `includeThoughts`).

These land under the AI SDK namespace key **`google`** (or `vertex`) — not
under native `gemini`.

### Tool schema sanitization

For Google / Gemini models: stringify enums, rewrite `type` arrays into
`anyOf`, prune invalid `required`, and similar fixes so Gemini (or proxies)
accept function declarations.

## Response contract

Transform is request-side only. Responses are handled by AI SDK or native
protocol parsers.

## Workflow

1. Check catalog `interleaved` / `reasoning` flags for the model.
2. Missing thinking on DeepSeek-style hosts → confirm interleaved hoist + any
   host-specific enable flags.
3. Gemini 3 thinking on AI SDK → confirm `thinkingConfig.thinkingLevel` under
   `providerOptions.google`.

## Edge cases

- OpenRouter SDK package id skips this interleaved hoist branch (different
  path).
- Enabling native Google without mapping `google` → `gemini` options keys
  drops thinkingConfig silently.

## Error handling

Bad schemas after sanitize still → provider 400. Fix the schema; see
`../gemini/tools.md` for the native schema subset.
