# Gemini — OpenAI-compatible endpoint

Google exposes an OpenAI-shaped shim:

```text
https://generativelanguage.googleapis.com/v1beta/openai/
```

Useful for drop-in OpenAI SDK experiments. **Not** the recommended path for
multi-turn agent/tool loops in Hermes or OpenClaw.

## When to use

- Prototyping with an existing OpenAI client by swapping `base_url`.
- Features that only exist in your OpenAI SDK wrapper and are known to work
  on the shim.

## When not to use

- Production agent loops with tools + Gemini 3 thought signatures.
- Streaming + complex tool replay (Hermes explicitly prefers native because
  the compat path was brittle).
- Any code path that already speaks native generateContent.

Hermes: if `GEMINI_BASE_URL` ends with `/openai`, it uses a normal OpenAI
client + `extra_body.google.thinking_config`. Set base URL back to
`https://generativelanguage.googleapis.com/v1beta` for the native adapter.

OpenClaw: strips `/openai` for native Gemini HTTP callers; keeps it only when
`api: "openai-completions"` is intentional.

## Auth

Often `Authorization: Bearer $GEMINI_API_KEY` on the compat host (OpenAI SDK
default). Do **not** assume the same header works on the native host —
native wants `x-goog-api-key` ([errors.md](errors.md)).

## Thinking on compat

```json
{
  "model": "gemini-3.6-flash",
  "messages": […],
  "extra_body": {
    "google": {
      "thinking_config": {
        "include_thoughts": true,
        "thinking_level": "low"
      }
    }
  }
}
```

Still subject to 2.5 vs 3 field rules ([thinking.md](thinking.md)).

## Recommendation

Default stack: **native** [generate-content.md](generate-content.md) +
[stream.md](stream.md) + [tools.md](tools.md). Treat `/openai` as an escape
hatch, not the architecture.
