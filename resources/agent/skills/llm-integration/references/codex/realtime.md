# Codex — Realtime (calls + websocket / sideband)

Codex’s realtime client (`codex-api` `RealtimeCallClient` +
`RealtimeWebsocketClient`) drives WebRTC calls and control websockets.
**Not** the public OpenAI GA mint flow in [`../openai/realtime.md`](../openai/realtime.md).

## vs OpenAI GA (`../openai/realtime.md`) — path diffs only

| OpenAI GA | Codex client |
|---|---|
| Mint ephemeral via `POST /v1/realtime/client_secrets` (`ek_…`) | No `client_secrets` step — auth is provider/ChatGPT headers on the Codex base URL |
| Browser/WebRTC often starts after secret mint | Create call with SDP (+ optional session) → answer SDP + `Location` call id |
| Trusted WS: `wss://api.openai.com/v1/realtime?model=…` | Standalone WS from **provider** base; WebRTC **sideband** often still `wss://api.openai.com/v1/...` |

## Host split

1. **Call create / standalone WS** — provider `base_url` (e.g.
   `https://chatgpt.com/backend-api/codex` or `https://api.openai.com/v1`).
2. **WebRTC sideband WS** — defaults to `https://api.openai.com/v1`
   (`OPENAI_REALTIME_API_BASE_URL`), independent of the Codex backend host.
   Override only for local/dev via `with_webrtc_sideband_base_url`.

Call create on ChatGPT backend ≠ sideband host. Join the exact call with the
`call_id` from `Location`.

## Positive cases

### A — SDP-only call create

`POST …/realtime/calls` with `Content-Type: application/sdp` and raw SDP body.
Response body = answer SDP; `Location` path segment = `call_id` (`rtc_…` or
36-char UUID).

### B — Call create with session (API host)

Non-`/backend-api` base:

| Parser | Path | Body |
|---|---|---|
| V1 | `realtime/calls?intent=quicksilver&architecture=avas` | multipart: `sdp` + `session` JSON |
| FramelessBidi (“v3”) | `live` (no AVAS query) | same multipart |
| RealtimeV2 | rejected before send | — |

Session object from `session.update` shaping; **`id` stripped** before send
(WebRTC may start inference immediately).

### C — Call create with session (Codex `/backend-api`)

Always `POST …/realtime/calls` (even FramelessBidi). JSON body
`{ "sdp", "session" }` — not multipart. V1 **and** backend FramelessBidi get
`?intent=quicksilver&architecture=avas`.

### D — Standalone control websocket

From provider base → `wss`/`ws`, path `/v1/realtime` (V1/V2) or `/v1/live`
(FramelessBidi). V1 adds `intent=quicksilver` (+ optional `model=`). After
connect, send `session.update` for a new session; FramelessBidi waits for
`session.started`.

### E — WebRTC sideband join

After call create, connect sideband with `call_id`:

- V1 / RealtimeV2: `…/realtime?call_id=<id>` (on sideband base, usually OpenAI)
- FramelessBidi: `…/live/<call_id>`

Modes: legacy sideband may still `session.update` (not Frameless); **existing
call** attaches without overwriting session config.

## Contract

- **Auth:** same as other Codex endpoints (Bearer / ChatGPT token headers on
  provider requests). Sideband inherits merged provider + extra headers;
  optional `x-session-id`.
- **Call response:** UTF-8 SDP body + required `Location` containing call id
  (`rtc_*` or UUID); nested `/calls/calls/rtc_…` still parses last valid segment.
- **Parsers:** `V1` | `FramelessBidi` | `RealtimeV2` — path, query, wire events,
  and init differ. AVAS HTTP create allows V1 / Frameless only.
- **Session on create:** instructions, model, voice, modalities, initial items,
  `delegation` (Frameless), etc. — encoded like `session.update` without `id`.

## Edge cases

- Missing / non-id `Location` → hard error (no call id).
- RealtimeV2 + AVAS create → `InvalidRequest` (“require realtime v1 or v3”) —
  no HTTP.
- Frameless call ids `.` / `..` rejected on sideband URL build.
- Sideband connect retries per provider policy; **404 / 410** = session ended,
  do not retry.
- Backend multipart vs JSON: SIWC/`/backend-api` is JSON until routes align.
- Do not assume sideband host equals call-create host.
- Unexpected binary WS frames are errors; text JSON only.

## Related

- Public GA Realtime (client_secrets, session types): [`../openai/realtime.md`](../openai/realtime.md)
- Codex surface index: [`README.md`](README.md)
- Source: [realtime_call.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/realtime_call.rs),
  [realtime_websocket/](https://github.com/openai/codex/tree/main/codex-rs/codex-api/src/endpoint/realtime_websocket)
