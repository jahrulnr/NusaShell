# Gemini — Live / realtime voice

Bidirectional voice via the **Gemini Live** WebSocket
(`BidiGenerateContent` / Constrained variant), not generateContent HTTP.

Auth: API key on the Google AI Studio Live endpoint.  
OpenClaw: `extensions/google` realtime voice provider + Control UI Talk
(`realtime-talk-google-live*`). Hermes: **no** Live client in the audited
tree — use HTTP TTS ([tts.md](tts.md)) or another realtime provider.

## Endpoint (AI Studio)

```text
wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1alpha.GenerativeService.BidiGenerateContentConstrained
```

(Exact path/version moves — confirm against current Live docs before ship.)

## Capabilities

- Streaming audio in/out, optional video frames (model-dependent).
- Tool calls over the live session (name rules stricter than HTTP tools).
- Thinking / affective-dialog fields vary by model generation — Gemini 3.1
  Live drops some older NON_BLOCKING / affective fields (OpenClaw notes).

## Practical rules (from OpenClaw)

1. Prefer current Live preview model (e.g. `gemini-3.1-flash-live-preview` —
   verify live list).
2. `temperature: 0` can yield transcripts **without** audio — avoid for
   spoken agents.
3. Tool names: leading letter/underscore, max length ~128.
4. Session setup is a first WS message (setup/model/voice/modalities); then
   realtime client content / tool responses.
5. Idle / connect timeouts still apply; tear down WS on user stop to halt
   billing.

## When not to use Live

- One-shot “read this string aloud” → [tts.md](tts.md).
- Text agent with tools → [generate-content.md](generate-content.md) +
  [tools.md](tools.md).
- Vertex-only environments → confirm Live availability on Vertex before
  depending on the AI Studio WS URL ([vertex.md](vertex.md)).

## Error handling

Auth/setup failures close the socket — surface the setup error payload.
Do not retry setup in a tight loop on 401/403. Quota → [errors.md](errors.md).
