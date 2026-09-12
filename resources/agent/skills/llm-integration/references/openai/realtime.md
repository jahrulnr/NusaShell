# OpenAI — Realtime API (live voice / transcription / translation)

Persistent low-latency sessions for live audio. **Not** a drop-in for
file STT (`stt.md`) or one-shot TTS (`tts.md`).

Auth for trusted servers: `Authorization: Bearer $OPENAI_API_KEY`.
Browsers/mobile must use **ephemeral client secrets** — never ship the
main API key to a client.

## When to use which path

| Goal | Start here |
|---|---|
| Speech-to-speech voice agent + tools | `gpt-realtime-2.1` conversation session on `/v1/realtime` |
| Live translation | Translation session on `/v1/realtime/translations` |
| Live transcript deltas only | Transcription session (`gpt-live-transcribe`) |
| File / bounded audio → text | `stt.md` |
| Text → audio file | `tts.md` |

## Transports

| Transport | Use when |
|---|---|
| **WebRTC** | Browser / mobile capturing or playing mic audio |
| **WebSocket** | Server already has a media pipeline / worker / telephony bridge |
| **SIP** | Phone voice agents (confirm model support for translate/transcribe) |

## Positive case — mint ephemeral client secret (GA)

Beta `POST /v1/realtime/sessions` is retired. Use:

```bash
curl -s https://api.openai.com/v1/realtime/client_secrets \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "session": {
      "type": "realtime",
      "model": "gpt-realtime-2.1",
      "audio": {
        "output": { "voice": "marin" }
      }
    }
  }'
```

Response shape (GA):

- Ephemeral token at top-level **`value`** (prefix `ek_…`), **not**
  `client_secret.value`
- Effective `session` object echoed back
- `expires_at` — mint a fresh secret when expired; a secret can open multiple
  sessions until expiry

Browser then connects with WebRTC (typically via `/v1/realtime/calls`) using
`value` as the client credential.

## Positive case — server WebSocket (trusted backend)

Connect to `wss://api.openai.com/v1/realtime?model=…` with the standard API
key (or an ephemeral secret). **Do not** send `OpenAI-Beta: realtime=v1` on
the GA interface.

Session config belongs in the GA shape: `session.type`, audio under
`session.audio.output`, modern event names (see below).

## Session types

1. **Voice-agent (`type: "realtime"`)** — model listens, reasons, speaks,
   calls tools; normal conversation lifecycle (`response.create`, etc.).
2. **Translation** — dedicated continuous translate endpoint; do **not**
   drive it with the normal assistant turn/`response.create` loop.
3. **Transcription** — stream transcript deltas without spoken model replies;
   use for captions / live notes.

## GA event / config deltas (beta → GA)

If migrating an old beta integration:

- Remove `OpenAI-Beta: realtime=v1`
- Replace `/v1/realtime/sessions` → `/v1/realtime/client_secrets`
- Require `session.type`
- Move output voice/format under `session.audio.output`
- Prefer newer event names such as:
  - `response.output_text.delta`
  - `response.output_audio.delta`
  - `response.output_audio_transcript.delta`
- WebRTC establishment uses `/v1/realtime/calls` in current docs

## Safety identifier

If the app has stable end-user ids, send `OpenAI-Safety-Identifier` (hashed
internal id) on the **server** request that creates the client secret (or on
trusted WebSocket/WebRTC connect). It does **not** inherit from Responses API
`safety_identifier` automatically — pass the same value explicitly.

## Workflow

1. Classify: voice agent vs translate vs transcribe vs file STT/TTS.
2. Choose transport (WebRTC client vs WebSocket server).
3. Server mints `client_secrets` with locked-down session defaults (model,
   voice, tools, VAD).
4. Client connects; handle audio deltas + tool calls; idle-timeout the
   session on silence/disconnect.
5. Never log ephemeral secrets or API keys.

## Edge cases

- **Calling retired `/realtime/sessions`** → `Invalid URL` / 404 on some
  regions — migrate to `client_secrets`.
- **Reading `client_secret.value`** on GA responses → undefined token —
  use `value`.
- **Shipping `sk-…` to the browser** → key leak; always ephemeral `ek_…`.
- **Using file-STT models in a Realtime voice-agent session** (or vice
  versa) → wrong product surface; pick from the table above.
- **Translation sessions** ignore the normal turn lifecycle — don’t block
  waiting for `response.create`.
- Reasoning on Realtime 2: start with low `reasoning.effort` for production
  latency; raise only when quality demands it.
- Voices differ from REST TTS (`tts.md`) — validate against Realtime voice
  lists, not the Speech API list.

## Error handling

Auth failures on client secret mint = fix server key/permissions. Mid-session
disconnects = reconnect with a **fresh** secret if expired. Tool-call errors
are application-level — don’t tear down the audio session unless necessary.
HTTP envelope details: `errors.md`.
