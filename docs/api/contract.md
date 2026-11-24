# WebUI API contract

**Status: phases 1–4 implemented; the Swagger UI (phase 5) remains
proposed.** The HTTP surface — all `GET` routes, the mutations
(`PATCH`/`PUT`/`DELETE`/`POST`), the `Host`/`Origin` security envelope,
and problem+json error mapping — is wired in
`infrastructure/delivery/http`, and the WebSocket surface — upgrade
validation, `turn.send`/`turn.edit`/`turn.resume` turns, `turn.cancel`/`turn.watch`
turn control, bounded journals with replay, and the full stream/terminal
lifecycle — is wired in `infrastructure/delivery/websocket`; both are
served by `nusashell serve` (loopback `127.0.0.1` only). This document,
the OpenAPI specification in [`openapi.yaml`](./openapi.yaml), and the
WebSocket spec describe the interface the delivery layer must satisfy;
where the current engine cannot yet supply a behavior, the
[implementation
work](#implementation-work-required) section says so explicitly.

## What this is

A single-seat, single-machine web UI for the conversation engine — one
intended user on the loopback host. (Anything on the host can reach the
socket; see [trust model](#deployment-and-trust-model).) The
engine already exposes everything a UI needs through the application
layer — `application/conversation` for turns and room lifecycle,
`application/provider` for provider management, `application/setting` for
the JSON config — so the WebUI is a transport, not a product change. The
contract has two halves:

- **HTTP** (Gin, OpenAPI-described): resource reads and management
  writes — conversations, messages, providers, settings, tools, health.
- **One multiplexed WebSocket**: the live channel — streaming turn
  events and the commands that drive turns (send, edit, cancel,
  reattach). There is deliberately **no client-facing SSE**: provider
  streaming stays inside `infrastructure/service` adapters, and turn
  progress reaches the browser only as WS frames. One socket carries
  every conversation's events; events are attributed by
  `conversationId`/`turnId`.

## Scope and non-goals

**In scope** — one intended user on the loopback host:

- Conversation list/detail/history reads, rename, delete, on-demand
  compaction.
- Sending and editing messages, canceling turns, reattaching to a turn
  after a reconnect — all over the one WebSocket.
- Provider management: list, inspect, upsert, delete —
  with write-only credential handling.
- Settings read and leaf-level write through `application/setting`.
- Tool catalog read.
- A locally-served Swagger UI rendering `openapi.yaml`.

**Out of scope, explicitly:**

- **Multi-user, remote access, or any listen address other than
  `127.0.0.1`.** There is no remotely-configurable listen address and no
  authentication or token layer in this contract — see
  [trust model](#deployment-and-trust-model). If remote access is ever
  wanted, that is a new contract, not a flag on this one.
- **Client-facing SSE.** Not offered, ever, on this transport.
- **Browser-supplied paths as attachments.** No `file://` and no `data:`
  URIs cross the API as attachments — a browser that could hand the engine
  a local file path would turn every local process into a file
  exfiltration path. Files are uploaded first (`POST /api/uploads`) and
  `turn.send` names the server-held upload ids.
- **Tool approval prompts, settings-change events, and room-wide
  broadcast events.** Not in the initial frame set.
- **API versioning.** Per the root breaking-changes policy there is one
  contract at a time — no `/v1` path segments, no shims, no deprecated
  fields. Breaking changes land as contract revisions of these
  documents.

**Server-owned hidden field — provider `optionalContent`.** The API
never serializes `optionalContent` in either direction — the transport
does not expose or accept it, full stop. But `PUT /api/providers/{id}`
is a full replace of the *readable* document, and a plain replace would
destroy each stored model's hidden provider params (e.g. a
`gemini.thinking_budget` written by the CLI). The contract therefore
requires the application layer to **preserve** it: on upsert, each model
in the replacement catalog that shares an `id` with a stored model keeps
that stored model's `optionalContent`; a model dropped from the catalog
loses it with the record. `application/provider.Upsert` implements this:
it reads the stored snapshot under its mutex and carries matching
models' hidden field forward — application-layer work, not a transport
merge (the wire never carries the field), serialized against the stored
record because provider writes do not pass through a room worker.
Exposing the field on the API remains future work.

## Deployment and trust model

- The server binds `server.bind` (default `127.0.0.1`) on `server.port`
  (default `10994`, changeable in Settings; applies after restart).
  `--port` remains a one-process CLI override.
- `server.hosts` is the list of hostname/IP aliases accepted in `Host`
  and `Origin` headers; defaults are `127.0.0.1` and `localhost`.
  Entries omit the port, which is taken from `server.port`. A host that is
  not a loopback name is a reverse proxy's public name; for those the bare
  host (no port) and an `https://` origin are also accepted.
- **Login.** `server.auth` decides whether requests need a session:
  `auto` (default) when the server is reachable beyond this machine — a
  non-loopback `server.bind`, or a non-loopback name in `server.hosts` —
  `on` always, `off` never (refused at startup on a reachable server).
  With a login required, every `/api` route except `/api/health` and
  `/api/auth/*` needs the `nusashell_session` cookie (HttpOnly,
  `SameSite=Strict`; `401 unauthenticated` without it), and every unsafe
  method also needs the session's token in `X-CSRF-Token`
  (`403 csrf`). The page itself and its assets are always served so the
  login screen can load. On loopback without a login the trust boundary
  is the machine: any local process can reach the socket, and the checks
  below are what keep *other websites* out:

  - **`Host` validation.** Every request must carry a `Host` header
    matching one configured `server.hosts` entry with the configured
    port (for example, `localhost:10994`). Anything else is rejected
    `403`.
  - **`Origin` on unsafe HTTP.** `POST`, `PUT`, `PATCH`, `DELETE`
    requests must carry an `Origin` header matching the same configured
    host and port, such as `http://localhost:10994`. Missing or
    mismatched origins are rejected `403`. Non-browser callers (curl,
    scripts) must supply `Origin` explicitly; there is no "CLI
    exemption" path.
  - **`Origin` on the WebSocket handshake.** The upgrade request must
    carry an `Origin` matching one configured host and port; a missing
    or foreign origin is rejected before the socket opens. This is the
    standard WebSocket CSRF defense — browsers send `Origin` on every
    WS handshake.
  - **Safe methods** (`GET`, `HEAD`) do not require `Origin` — they are
    reads, and the responses are ordinary JSON that a cross-origin page
    could not read anyway under CORS rules (the server sends no CORS
    allow-headers).

  These checks are implementation work — Gin middleware — and are not
  optional plumbing; they are the security model.

- **Not found.** A `GET`/`HEAD` that did not match a route or asset is
  answered with the app shell and `404` when it asked for HTML (`Accept:
  text/html`) — a browser opening an address — so the app renders its own
  not-found view instead of a problem+json body a browser shows as a dead
  page. A path-style thread link (`/c/<id>`) is taken over on boot and
  rewritten to `/#/c/<id>`, so a shared address still opens its thread.
  `/api` paths, missing assets (`/components/…`, `/vendor/…`), and
  requests that did not ask for HTML keep the `404` problem+json
  (`not_found`).

- Swagger UI is served by the same server on loopback only, alongside
  the JSON/YAML form of the spec. It documents the HTTP surface; it does
  **not** define WebSocket frames — [`websocket.md`](./websocket.md) is
  normative for those.

## Projection rules — what the server never serializes

Every response body is a sanitized projection. The guarantee is scoped
honestly: it covers **server-held fields**, not message text. The
following never leave the server on any endpoint or frame:

- **API key material and raw key references.** Provider responses expose
  `keySet` and a `keyState` enum (`unset` | `env` | `vaulted` |
  `broken`), never the stored `apiKey`, never the raw `"env:NAME"` /
  `"secret:<key>"` reference string. `apiKey` is write-only.
- **Internal error causes.** `apperror.Cause` (upstream bodies, stack
  details) stays server-side; the wire sees `code` + `detail` only.
- **Provider `optionalContent`.** Adapter-namespaced replay artifacts
  (`gemini.thought_signature`, `deepseek.reasoning_content`, …) are
  internal state — omitted from message and model read projections and
  not accepted on write. On provider `PUT` the application preserves it
  per surviving model `id` — see the
  [hidden-field rule](#scope-and-non-goals).
- **Normalized message parts.** Assistant history exposes ordered
  `parts`: answer text, raw or summarized reasoning text, and tool-call
  positions. Live reasoning crosses in order as `turn.reasoning` chunks.
  Provider replay artifacts in `optionalContent` remain internal.
- **Attachment references, not bytes.** Message and tool-output
  attachments project `mediaType`, `modality`, `uri`, and optional
  `name` — the stored reference (including `file://` local paths)
  serializes to the loopback owner by design, and a stored name can
  carry path-shaped data, so clients render both as escaped text.
  Attachment bytes are never served over the API, and the `sha256`
  content pin stays internal. An assistant message that went out to a
  linked chat also carries `sentFiles` (the files sent with it, same
  shape) and `receipt` (`"delivered"` or `"read"` once the chat confirms;
  WhatsApp only). Changes arrive as a `conversation.updated` socket
  notice.
- **What intentionally crosses: tool I/O.** This is a single-user
  loopback UI for its owner — tool `input` (exact argument string) and
  `output` (live chunks over WS; plain result text over HTTP) are part of
  the contract. Each `toolCalls[]` entry carries `type`: `origin` for a
  model-issued call, `injection` for an application-inserted context
  record (e.g. the workspace line). The provider still receives the
  stored untrusted-evidence envelope; its delimiter and latency tag are
  not sent to the UI.
  They are untrusted data — can contain paths, commands, secrets — and
  must render as escaped text, never markup. Any loopback process can
  reach the API; SOP/Host checks guard browsers, they are not OS-user
  auth.
- **Server internals.** No config file location, no `storage.dir`, no
  app metadata (see [settings](#settings-semantics)).

What the contract **cannot** guarantee — and does not promise: text
fields are untrusted data. `Message.content`, `turn.delta` text, tool
catalog `description`/`parameters`, and similar fields are produced by
the model, the user, or tool execution and can contain arbitrary
strings — credentials, paths, anything. The guarantee above is about
fields the server *holds*; it is not a content filter. Clients must
render every text field as escaped, sanitized content, never as markup.

## HTTP surface

Full schemas, typed errors, and examples live in
[`openapi.yaml`](./openapi.yaml). Summary:

| Method & path | Purpose |
|---|---|
| `GET /api/health` | Liveness — `200 {"status":"ok"}`. |
| `GET /api/auth/status` | `{ required, authenticated, csrfToken?, hasPassword? }` — whether a login is needed, the CSRF token of the session the cookie names, and (for a signed-in caller) whether an owner password is set. |
| `POST /api/auth/login` | Body `{password}`; sets the session cookie and answers `{ required, authenticated, csrfToken }`. Wrong password `401 unauthenticated`; five failures per client per 15 minutes `429 too_many_attempts`. |
| `POST /api/auth/logout` | Ends the session and clears the cookie; `204`. |
| `POST /api/owner/password` | Body `{current, new}`: sets the owner password. Behind the session and CSRF check like any other route; `current` must match the password set now (`403 wrong_password`, counted with failed logins, `429 too_many_attempts` after five) and is ignored while none is set; a new one outside 4 characters to 72 bytes is `400`. Every session ends; when logins are required the caller gets a new session cookie and answers `{ required, authenticated, csrfToken, hasPassword }`. |
| `GET /api/agents` · `GET /api/agents/{id}` | Agent projections: `id`, `name`, `command`, `args`, `env` (`[{name, ref}]`, never values), `policy`. |
| `PUT /api/agents/{id}` · `DELETE /api/agents/{id}` | Full-replace upsert (body `{name, command, args, env, policy}`; an env value that is a stored ref is kept, anything else is vaulted) and removal. |
| `GET /api/agents/detect` · `POST /api/agents/import` | Known agent CLIs found on `PATH`, and registering the ones not configured yet. |
| `GET /api/agents/{id}/models` | Models the agent offers, read from a throwaway session; starts the agent on first use. |
| `GET /api/agents/{id}/status` | The agent's process without starting it: `state` (`stopped`, `running`, `error`), `pid`, `uptimeMs`, `lastError`, `authNeeded`, `canLogout`, `logs` (its latest 400 stderr lines, kept across restarts), and `authMethods` (`[{id, name, description, type, command}]`; a `terminal` method carries its command line, for a terminal of the owner's own). |
| `POST /api/agents/{id}/authenticate` · `POST /api/agents/{id}/logout` · `POST /api/agents/{id}/restart` | Sign the agent in with body `{methodId}` (starts it when needed; a `terminal` method is `400` — it runs through `sign-in`), sign it out and stop it (`400` when it does not support that), or stop its process so the next turn starts it again. Each answers `204`. A turn on an agent that needs a sign-in fails with `502 upstream_error`. |
| `GET /api/conversations/{id}/approvals` · `POST /api/conversations/{id}/approvals/{requestId}` | Open permission requests of the room's agent, and answering one with body `{optionId}` (`204`; `404` when nothing waits under that id). Answering with an `allow_always` option stores an approval rule. |
| `GET /api/conversations/{id}/approval-rules` · `DELETE /api/conversations/{id}/approval-rules/{ruleId}` | The room's "always allow" rules — `{ "data": [{ id, action, createdAt }] }`; the agent is not asked again for that kind of action — and forgetting one (`204`). |
| `GET /api/conversations/{id}/elicitations` · `POST /api/conversations/{id}/elicitations/{elicitationId}` | Open requests for input from the room's agent (`[{id, mode, message, schema, url}]`, `mode` is `form` or `url`), and answering one with body `{action, content}`: `action` is `accept`, `decline`, or `cancel`; `content` (accept only) must match the form's schema — a missing required field, an unknown field, or a wrong type, length, range, pattern, or choice is `400` and the request stays open. `204`; `404` when nothing waits under that id. |
| `GET /api/conversations/{id}/agent-options` · `PUT /api/conversations/{id}/agent-options` | The agent's session options for the room — `{ "data": [{ id, name, description, category, type, current, choices: [{value, name, description}] }] }`, its live session's when one runs, else the agent's defaults — and setting one with body `{id, value}` (answers the updated list). The room keeps its picks and applies them before each turn. |
| `GET /api/commands?conversationId=` | The composer's slash-command catalog — `{ "data": [{ name, description, hint, source, enabled, reason }] }`: the native commands (compact, clear, fork) first, then the room's agent commands, one row per name with `source` saying which owns it (native wins a collision). `enabled`/`reason` are the server's verdict for this room; an agent command is always on. Without `conversationId` it answers the native set with every row off and `reason: "Send a message first"`. |
| `GET /api/conversations?offset&limit` | List projections, `updatedAt` desc (best-effort). Items include `activeTurnId` / `activeTurnKind` while a transport turn is in progress. `limit` default 30, max 100. |
| `GET /api/conversations/{id}` | One projection. Detail adds `canCompact`, and `activeTurnId` / `activeTurnKind` while a transport turn is in progress. |
| `GET /api/conversations/{id}/messages?start&limit` | Ranged history; `start >= 0` absolute, negative counts back from the end. `limit` max 100. |
| `PATCH /api/conversations/{id}` | Any of `title`, `projectId` (`""` takes the room out of its project; an unknown id is `400`), `pinned`, `label` (the room's `@handle`: 1–24 of `a-z0-9` with inner hyphens, a leading `@` dropped; `""` clears; invalid `400`, taken by another room `409`), and `routing` (`{replyToOrigin, mirrorFinal, notifyApprovals}`; `null` returns to the defaults); absent fields keep their value. Queued on the room's worker, so it lands after a running turn. Answers the updated projection, which carries `label` and `routing` when set. |
| `GET /api/conversations/{id}/export` | The whole thread as one JSON download (`Content-Disposition: attachment; filename=nusashell-<id>.json`): `{ exportedAt, conversation, messages }`, in the shapes the projection and messages routes serve. |
| `POST /api/uploads` | Multipart `file`: keeps one file for the next message — `201 { "upload": { id, name, mediaType, size } }`. At most 25 MB (`413`); empty `400`. A missing or generic media type is sniffed from the name, then the bytes. `turn.send` names it in `payload.uploads`. |
| `GET /api/references?q=&conversationId=&projectId=&workspace=` | Composer `@`/`/` choices for the place the draft belongs to — `{ "data": [{ kind, id, name, detail }] }`: threads, projects, skills, MCP servers, and the files and folders of the place's folders (the thread's or project's, else the workspace; `.gitignore` respected, `.git` skipped). At most 25 per kind and 100 in all, names starting with `q` first. |
| `GET /api/roots?conversationId=&projectId=&workspace=` | The place's folders, primary first — `{ "data": ["/abs/dir", …] }`. |
| `GET /api/files?path=&conversationId=&projectId=&download=` · `GET /api/conversations/{id}/files?path=` | Serves one file for preview: an absolute path, `~/…`, a `file:` URL, or a path relative to the place's primary folder (a trailing `:line` is tried off when the file is missing). Media and PDF open inline unless `download=1`; everything else downloads. `nosniff` and a sandboxing CSP (not on PDF); range requests work. Missing `404`. |
| `GET /api/conversations/{id}/terminal` · `GET /api/terminal?workspace=` | The owner's terminal: a WebSocket running a login shell in a pty (local environment only; `cwd` must be inside one of the place's folders, empty is the primary one). Query `cwd`, `cols`, `rows`, plus `projectId`/`workspace` on `/api/terminal`. The client sends JSON `{type:"input", data}` and `{type:"resize", cols, rows}`; the server sends binary output, `{type:"ready", cwd}` once, and `{type:"exit", ok, message}` when the shell ends. Same Origin rule as `/api/ws`. |
| `GET /api/agents/{id}/sign-in?methodId=&cols=&rows=` | An agent's `terminal` sign-in method as a WebSocket terminal: the agent's own command line with the method's arguments and variables, in a pty in the owner's home folder. Same messages as the owner's terminal (`ready` has no `cwd`). When it exits the agent's process stops, so its next start reads the new login. An unknown or non-terminal method is `400`. Same Origin rule as `/api/ws`. |
| `GET /api/conversations/{id}/view` · `PUT /api/conversations/{id}/view` · `GET /api/view` · `PUT /api/view` | What the owner left in a thread's view — the WebUI's own JSON object (the unsent draft, its references, quotes and ready attachments, the side pane, the browser's last page) — kept so a reload or another device opens the thread as it was; `/api/view` is the composer of a thread not started yet. `GET` answers `{ "view": { document, updatedAt } }` (`{}` and `0` when nothing is kept); `PUT` takes `{document}`, replaces it (`{}` forgets it), and answers the same. Not an object `400`; over 256 KB `413`; a thread that does not exist `404`. Deleting the thread deletes its view. |
| `GET /api/conversations/{id}/links` · `DELETE /api/conversations/{id}/links?chat={key}` | Chats linked to the room — `{ "data": [{ "key", "channel", "chatId", "thread", "display", "group", "since" }] }` — and unlinking one by its `key` (`204`). |
| `GET /api/channels` | Channel statuses — `{ "data": [{ name, label, enabled, connected, state, detail, account, hasQr, blocked: [{senderId, chatId, name, group, at}], updatedAt }] }`. |
| `GET /api/channels/{name}/settings` · `PUT /api/channels/{name}/settings` | The channel's settings document (allowlists, group policy; WhatsApp also `retentionDays`, `sendPacing`, `queueCap`; secrets show only as `tokenSet`). `PUT` changes the fields given and answers the stored document; `addNumbers`/`removeNumbers`/`addGroups`/`removeGroups` (WhatsApp) and `addUserIds`/`removeUserIds`/`addGroupIds`/`removeGroupIds` (Telegram) change a list in place, and an entry that is not a valid id is `400`; Telegram's `token` is tri-state write-only (absent keeps, `""` deletes, a value replaces). Unknown channel `404`. |
| `POST /api/channels/{name}/connect` · `…/disconnect` · `…/logout` | Start pairing or reconnect, drop the connection, or forget the account (WhatsApp unpairs the device; Telegram deletes the token). Answers the channel status; a refused action is `400`. |
| `GET /api/channels/{name}/qr` | The current WhatsApp pairing QR as `image/png` (`Cache-Control: no-store`); `404` when none is pending. |
| `GET /api/channels/{name}/groups` | Groups the WhatsApp account is in, for the allowlist picker; `404` for channels without groups. |
| `POST /api/channels/{name}/send` | One message the owner sends apart from any room (the CLI's `channel … send`): WhatsApp's send request (`target`, `type`, and the type's fields; files as base64 `data`, up to the channel file limit). Answers `{ "messageId" }`. A malformed request or a channel that is off or disconnected is `400`; a failure past that is `502`; channels without explicit sends `404`. |
| `GET /api/channels/{name}/user?id=` | A WhatsApp user's profile through the connection: `{ jid, found, user: { status, pictureId, devices, lid?, verifiedName? }, contact: { jid, firstName, fullName, pushName, businessName } }`. |
| `GET /api/channels/{name}/contacts?q=&page=&limit=` | Contacts the linked device synced whose JID or names contain `q`: `{ contacts: [contact…], page, hasMore }`. `limit` 1-500 (50). |
| `GET /api/channels/{name}/messages?q=&chat=&sender=&cursor=&limit=&raw=` | The channel's kept messages, newest first: `{ "data": [{ chat, chatName, id, sender, senderName, at, fromMe, group, text, receipt, explicit, raw? }], "nextCursor" }`. `q` matches as one phrase (case-insensitive); `limit` 1-500 (50); `raw=1` adds the channel's own payload. Only WhatsApp keeps messages. |
| `GET /api/chats` | Every chat linked to any room, in the links shape — the chats an automation can send its result to. |
| `GET /api/automations` | `{ "data": [automation…], "timezone" }` — `timezone` is the server's, the default for new schedules. An automation: `id`, `name`, `prompt`, `agentId` or `providerId` + `modelId`, `schedule` (`once` with `runAt`, or `cron` with `cron`), `timezone` (IANA), `thread` (`new` or `same`), `conversationId` (the kept thread of a `same` automation), `projectId`, `notify` (`{channel, chatId, thread}`), `expiresAt`, `enabled`, `expired`, `nextRunAt`, `lastRun`, `createdAt`, `updatedAt`. |
| `GET /api/automations/{id}` | One automation (without `lastRun`). |
| `POST /api/automations` | Body `{name, prompt, agentId, providerId, modelId, schedule, runAt, cron, timezone, thread, projectId, notify, expiresAt, enabled}`; times are RFC 3339, an empty `timezone` takes the server's, `thread` defaults to `new`, `enabled` to `true`. An invalid cron or zone, a one-time moment more than a minute past, a past expiry, or an unknown project is `400`. `201` with the automation. |
| `PUT /api/automations/{id}` | Full edit with the same body plus `updatedAt` from the automation the edit started from (required, `400` without it); an automation the owner saved since answers `409` (a firing does not change `updatedAt`). An absent `enabled` keeps the automation paused or active; an unchanged schedule keeps its next run. |
| `PATCH /api/automations/{id}` | Pause or resume — body `{enabled}`. Resuming a repeating schedule plans from now; firings missed while paused do not run. |
| `DELETE /api/automations/{id}` | Removes the automation and its run history; the threads its runs wrote stay. `204`. |
| `GET /api/automations/{id}/runs` | The 50 most recent runs (100 are kept), newest first — `{ "data": [{ id, scheduledAt, startedAt, endedAt, conversationId, status, note }] }`; `status` is `running`, `completed`, `failed`, `cancelled`, or `interrupted` (the server stopped during the run). |
| `POST /api/automations/{id}/runs` | Run now: starts one run without moving the schedule. `202` with the run (`running`); `409` while a run of the automation is still going, `503` while the server shuts down. |
| `POST /api/automations/{id}/test` | Sends a test message to the automation's result chat. `204`; `400` when it has none, `503` when channels are unavailable. |
| `GET /api/notifications` | Desktop notifications on the server's machine — `{ enabled, available, backend, reason? }`; `reason` says why they cannot show (for example a headless host). |
| `POST /api/notifications/test` | Shows a test notification whatever `notifications.desktop` says. `204`; `409` when unavailable, `502` when the backend failed. |
| `GET /api/conversations/{id}/browser` | The thread's browser — `{ open: false }`, or `{ open: true, page: { url, title, width, height }, device: { preset, width, height, dpr, mobile, touch, userAgent? }, lease: { controller?, agentLock }, profile }`. |
| `GET /api/conversations/{id}/browser/ws` | Live view WebSocket (same Origin rule as `/api/ws`; `404` before the upgrade when no session runs — watching never starts one). The server sends `{type:"hello", viewer}`, `{type:"state", page, device, lease, profile, agentPointer?}` when the state changes (`agentPointer {x, y, action, seq}` is where the agent last acted — `click`, `hover`, or `scroll` — sent just before it acts; `seq` grows with each action) (slow viewers may skip intermediate snapshots; `device.preset` is `responsive`, `desktop`, `mobile`, `tablet`, or `custom`), binary JPEG frames (a screenshot first), `{type:"refused", message}`, `{type:"keyboard", editable}` after a touch, and `{type:"closed"}`. The viewer sends `takeover` / `release`, then while it holds the lease (and the agent has not locked input): `move {x,y,buttons}`, `down`/`up`/`click {x,y,button,clickCount}`, `wheel {x,y,deltaX,deltaY}`, `touch {phase,x,y}`, `key {key,modifiers}`, `text {text}`, `navigate {url}`, `back`, `forward`, `emulate {preset}` (`responsive`, `desktop`, `mobile`, or `tablet`). Separately, `fit {width,height}` sizes a responsive tab to the sender's pane: it needs no lease — a watching pane reports while no other viewer drives, and the driver, if any, owns the size. Coordinates are CSS pixels of the emulated viewport. A viewer that disconnects releases the lease. |
| `POST /api/conversations/{id}/browser/annotate` | Marks the live page for the next message: body `{kind, x, y, width, height, marker}` — `element` (default) at `x,y`, `region` with a positive size, or `page`; coordinates are CSS pixels of the emulated viewport and `marker` is the badge number (≥1). Paints the badge, keeps a full-viewport PNG with it as an upload, removes the badge, and answers `201 { pickId, marker, descriptor, upload }`. The descriptor holds the selector, tag, role, accessible name, text, opening tag, rect, frame path, page URL and title, viewport, scroll, and resolved style. No element at the point `422`; another mark in flight `409`; no running session `409`. `turn.send` names the pick in `payload.annotations`. |
| `POST /api/conversations/{id}/browser/annotate/preview` · `…/annotate/clear` | The element under `{x, y}` for a hover outline — `{ missed, skipped?, rect?, tagName?, name?, selector? }` (`skipped` while a mark is in flight) — and removing a badge left on the page (`204`). |
| `GET /api/browser/profiles` | The browser profiles — `{ "data": ["default", …] }`, the default one first. |
| `GET /api/browser/history?q=&profile=&limit=` | Address-bar suggestions: pages the profile's tabs finished loading (http and https only) — `{ "data": [{ url, title, visits, visitedAt }] }`. An empty `q` is the most recent; otherwise pages whose url or title contains it, those starting with it (after the scheme and `www.`) first, then the most visited. No `profile` is the default one; `limit` defaults to 8, at most 50. |
| `GET /api/conversations/{id}/browser/console` | What the open page logged and threw since it loaded, oldest first, at most the last 200 — `{ "data": [{ level, text, ts }] }` (`level` is `log`, `info`, `warn`, `error`, or `debug`; `ts` Unix ms). `404` when the thread's browser is not open; it never opens one. |
| `POST /api/conversations/{id}/browser/eval` | Runs the owner's JavaScript in the open page, awaiting a promise: body `{expression}`, answer `{ value }` (the value as JSON). Empty `400`; over 64 KB, or a value over 256 KB, `413`; a thrown error `422` with its message; no open browser `404`. |
| `GET /api/browser/install` | The browser executable: `{missing, execPath, searched, platform, proot, methods: [{id, label, detail, needsRoot}]}`. |
| `POST /api/browser/install` | Body `{method}` from the offered ids; runs up to 15 minutes. `200` with `{execPath}` or `{error}`, plus `log` (the output's last 32 KB). |
| `GET /api/conversations/{id}/browser/logins` · `GET /api/browser/logins?profile=` | Saved logins without passwords — for the page the thread's tab is on, or for a profile (all when empty): `{ "data": [{ id, profile, origin, username, updatedAt }] }`. The live view answers a save offer with `login {id, save, username?, password?}` and fills one with `autofill {id}` (needs the lease). |
| `GET /api/conversations/{id}/recordings` | The thread's browser recordings, newest first — `{ "data": [{ id, status, profile, createdAt, startedAt, endedAt?, counts: {events, annotations, errors}, … }] }`. The live view starts and steers one with `record`, `pause_recording`, `resume_recording`, `stop_recording`. `turn.send` attaches one in `payload.recordingId`; a user message carries it as `recording: {id, status}`. |
| `GET /api/conversations/{id}/recordings/{rec}/summary` · `…/events?after=&limit=&types=` | A recording's summary (plain text, written when it stops) and its timeline page `{ events, next, done }`. |
| `DELETE /api/browser/logins/{id}` | Removes a saved login and its password. `204`. |
| `DELETE /api/conversations/{id}/browser` | Ends the thread's browser session. `204`, also when none ran. |
| `GET /api/mcp-servers` · `GET /api/mcp-servers/{id}` | The owner's MCP servers — `{ "data": [server…] }`: `id`, `name`, `transport` (`stdio`, `http`, `sse`), `command`, `args`, `env` (names only), `url`, `headers` (names only), `enabled`, `status` (`{connected, tools, error?}`, the model runtime's connection, enabled servers only), `createdAt`, `updatedAt`. Values of variables and headers never cross. |
| `POST /api/mcp-servers` | Body `{name, transport, command, args, env, url, headers, enabled}`; `env` and `headers` are `[{name, value}]`. A name is 1-32 of `a-z0-9_-` and not `conversation` or `browser`; stdio needs a command, http and sse an http(s) URL (`400`); a taken name is `409`. `201`. |
| `PUT /api/mcp-servers/{id}` | Full edit with the same body plus `updatedAt` (required); a server saved since is `409`. A variable or header sent with an empty value keeps its stored value; one left out is removed. |
| `PATCH /api/mcp-servers/{id}` · `DELETE /api/mcp-servers/{id}` | Turn on or off with `{enabled}`; remove (`204`). The model runtime reconnects at once; agents get the change in their next session. |
| `GET /api/mcp/{server}` · `POST /api/mcp/{server}` | For agents only: the relay an agent runs as `nusashell mcp run <server>` lists a built-in server's tools (`{ name, tools }`) and calls one (body `{tool, arguments}`, answer an MCP tool result; a failed tool is a `200` with `isError`). Authorized by `Authorization: Bearer <thread token>` instead of a login — the token is minted per thread, held in memory, and handed to the agent in its session's MCP server environment. `401` for an unknown token, `404` for an unknown server. |
| `GET /api/projects` · `GET /api/projects/{id}` | Projects: `id`, `name`, `folders` (absolute paths; the first is primary), `pinned`, `threads` (count; the list only), `createdAt`, `updatedAt`. Pinned first. `GET /api/conversations?projectId=` lists one project's threads. |
| `POST /api/projects` | Body `{name, folders, pinned}`; every folder must be an existing directory (`400`). An empty name takes the primary folder's name. `201` with the project. |
| `PUT /api/projects/{id}` | Full edit, body `{name, folders, pinned, updatedAt}` — `updatedAt` from the project the edit started from; a project saved since answers `409`. |
| `PATCH /api/projects/{id}` | Pin or unpin — body `{pinned}`. |
| `DELETE /api/projects/{id}` | Removes the project and takes its rooms out of it; the rooms and their history stay. Answers `{released: [conversationId…]}`. |
| `POST /api/projects/{id}/restore` | Undoes a delete: body `{name, folders, pinned, released}`. Recreates the project under its id; each released room rejoins while it is still outside any project. `201` with `{project, rejoined: [conversationId…]}`; `409` when the project exists. |
| `DELETE /api/conversations/{id}` | Delete the room and its storage; queued jobs on it die `410`. |
| `DELETE /api/conversations` | Delete **every** room, then purge the artifacts only rooms own — uploads, page-mark picks, browser recordings, and saved views (the fresh-room view included). Body must be `{"confirm":"DELETE"}` — anything else is `400 confirm_required`. Each room's delete preempts a running turn rather than skipping it; a room that refuses to delete aborts the artifact purge so the survivors' files stay, and the error names the failed ids. Answers `{"deleted": N}` and clears the activity log below. |
| `GET /api/conversation-control/activity?caller=&target=&limit=` | The cross-thread activity log — which thread called which `conversation` tool, newest first: `{ "entries": [{ at, caller, tool, target, outcome, durationMs }] }`. Metadata only — no message text or tool arguments. `caller`/`target` filter on thread ids (a target that never resolved to a thread is empty); `limit` default 50, max 500. |
| `POST /api/conversations/{id}/commands` | Run one native command — an action, not a turn: body `{name, args, confirm}`, `200 {command, conversation}` with the room detail projection (the new room for `fork`). `compact` folds the live tail like the old compactions route, in the provider rooms that can; `clear` empties history, folds, and tool output while keeping the room's identity and drops its agent session (`409 confirmation_required` without `confirm: true`, or the word `confirm` as the only argument); `fork` copies the whole logical history and its tool-output files into a new room titled `<source> (fork)` (a still-default title stays default) and answers that room. Refusals: `400 invalid_argument` for an unknown name or stray arguments, `400 nothing_to_compact` / `nothing_to_clear` / `nothing_to_fork`, `409 busy` while a transport turn runs, `409 unavailable` for a command that does not apply to the room kind (compaction in an agent room). |
| `GET /api/providers?offset&limit` · `GET /api/providers/{id}` | Sanitized provider projections (paged list, sorted by id), including broken records. |
| `GET /api/providers/{id}/upstreams?model={modelId}` | Upstream backends an aggregator provider can serve the model through — `{ "data": [{ "name", "slug" }] }`, empty for direct providers. `model` is required (`400` without it). The picker echoes `slug` back as `payload.upstream` on `turn.send`/`turn.edit`/`turn.resume`. |
| `PUT /api/providers/{id}` | Full-replace upsert — the path id is authoritative (a body `id` is `400`); `apiKey` is tri-state write-only. After the replace, the provider catalog merges into the record: advertised ids the document lacked are appended, and entries already present backfill catalog-declared metadata (`contextWindow`, `outputLimit`, `modelType`, modalities, `supportedEfforts`, `name`, `canonical`, `aliasOf`, `pricing`) only on fields still unknown — an explicit value wins over the provider's declaration; clearing a field to zero re-derives it at the next import. A model's `style` pin fills the same way from the catalog's per-model API recommendation (models.dev `recommended_api`: chat-completions, responses, messages), and only when it differs from the provider's style; an explicit pin always wins, and clearing it re-derives at the next import. The response carries `added` (always present) plus `catalogError` when the merge failed — a catalog failure never fails the write. |
| `POST /api/providers/{id}/import-models` | Re-runs that same merge against the stored record — endpoint, style, and credential come from the record, not the request — and answers the same `added`/`catalogError` shape. `404` when the provider is absent. |
| `DELETE /api/providers/{id}` | Remove provider + vaulted key; `404` when absent. |
| `GET /api/settings` | Effective settings document (projected — see below). |
| `PATCH /api/settings` | One write of one or more leaves — body `{values: {key: value}}`. |
| `GET /api/tools?conversationId=<id>` | Registered tool catalog for that room: `name`, `description`, `parameters`, `enabled`, `permission`. Without `conversationId`, uses the local starting catalog. Returns `404` when no tool catalog is wired. |
| `GET /api/fs/browse?path=` | Directory-only listing for the workspace picker — `{ "path", "parent", "roots": [{"name","path"}], "entries": [{"name","path","hidden"}], "truncated"?: true }`. Empty `path` defaults to the owner's home; `parent` ascends toward the filesystem root and `roots` jumps to `/` (or present drive letters on Windows). `path` must be absolute (`400`); missing directories are `404`, unreadable `403`. Listings cap at 1000 directories (`truncated: true` when more remain). |
| `GET /api/fs/list?path=` | One directory's folders and files for the Files pane — `{ path, parent, dirs: [{name, path}], files: [{name, path, size}], truncated? }`, sorted by name without case. Same path rules as `/api/fs/browse`. |
| `GET /api/context?conversationId&providerId&modelId` | Composer-footer stats — `{ "estimate", "total" }`: estimated live input tokens and the bound model's input allowance. `conversationId` is optional — a fresh room still resolves `total` when `providerId`+`modelId` name a known model. |
| `GET /api/update` | Cached newest-release picture: `{ current, enabled, installable, latest, available, publishedAt, releaseUrl, notes, installCommand, checkedAt, error }`. The release lookup re-runs only when the server's one-hour cache is stale; a failed lookup is reported in `error`, not as an HTTP failure. `enabled` is `false` for dev builds, which never check. |
| `POST /api/update/check` | Forces a fresh release lookup and answers the same picture. |
| `POST /api/update/install` | Starts the in-app install of the latest release as one background job: download, verify against `SHA256SUMS`, stage beside the binary, replace it, restart. Answers `202` with the first progress state. Only one install runs at a time; refusals are `409` (see the error model). |
| `GET /api/update/install` | Progress of the current or most recent install: `{ state, phase, version, bytesDone, bytesTotal, restart, error }`. `state` is `idle`, `running`, `succeeded`, or `failed`; `succeeded` means the restart is queued, and the new version answers once it is up. |
| `GET /api/ws` | WebSocket upgrade — `101`; frames defined in [`websocket.md`](./websocket.md). |

There is intentionally **no `POST /api/conversations`** and **no
`PATCH /api/providers/{id}`**: sending a message *is* conversation
creation (`turn.send` upserts the room — a null or absent
`conversationId` gets a server-minted `c_<base32>` id), and provider
mutation *is* full replacement via `PUT`. Inventing soft-partial
endpoints would only fork the semantics.

### Native slash commands

A native command is an **action, not a turn**: it writes no message and emits
no `turn.*` frame. The catalog (`GET /api/commands`) is the one definition —
the three commands NusaShell owns (`compact`, `clear`, `fork`) plus the room's
agent commands, one row per name. A recognized name on a draft's first line
always dispatches as a command, never as text for the model or the agent; a
name outside the catalog is ordinary prose.

`POST /api/conversations/{id}/commands` runs one, and the application — not
the transport — enforces every precondition:

- **Idle room.** A live `turn.send`/`turn.edit` refuses every command with
  `409 busy`; compaction, clearing, and copying history under a running turn
  are all unsound. The turn hub is the authority on "live", read through a
  late-bound hook (`UseLiveTurns`) wired at composition.
- **Room kind.** `compact` keeps its old meaning: provider rooms only, a
  foldable tail required. An agent room answers `409 unavailable` — the agent
  manages its own context.
- **Nothing to act on.** An empty room answers `400 nothing_to_compact`,
  `400 nothing_to_clear`, or `400 nothing_to_fork`; a non-empty room with no
  new foldable history answers `400 nothing_to_compact`.
- **Confirmation.** `clear` is destructive: without `confirm: true` (or the
  word `confirm` as its only argument, the shape a chat ingress sends) it
  answers `409 confirmation_required`.
- **Arguments.** `compact` and `fork` take none and `clear` accepts only
  `confirm`; anything else is `400 invalid_argument`, as is an unknown name. A
  refused command keeps the client's draft; only a `200` spends it.

`clear` keeps the room's identity — id, title, provider/model/effort/upstream
or agent binding and its options, workspace, environment, project, `@label`,
reply routing, pin, memory setting, and accumulated usage — and removes every
message, sealed segment, and conversation-owned tool-output file. The agent's
session handle and context report go with the history, so the next turn starts
a fresh one; the same release runs against the agent's own session.

`fork` creates a new room that is a full copy of the source's logical history
(sealed segments plus live tail, with message attachments and the tool-output
files they reference) and of its bindings, workspace, environment, project,
memory setting, and agent session options. It copies no `@label`, reply
routing, pin, approval rules, agent session handle, saved view, or usage
(starts at zero). The title is `<source title> (fork)`, except a source still
on its default title keeps it and auto-titles on its first send. The source is
never modified, and the copy becomes visible — to `Info`, `List`, and this
contract — only once its history is complete.

### Pagination, honestly

`GET /api/conversations` is backed by the repository's `List` — it reads
**all** conversation infos and the API slices the page afterward. The
order is `updatedAt` descending, but the list is **not a snapshot**: a
write between two page requests shifts offsets, so a client paging
through a live list can see duplicates or gaps. That is accepted for a
single-seat UI; cursor pagination is a contract change if it ever
matters.

`GET .../messages` is backed by `LoadAt(start, end)` over the global
logical history (sealed segments first, then the live tail). The
response carries `effectiveStart`, `nextStart`, `total`, and `hasMore`
so the client can page in either direction without caching semantics —
but the same caveat applies: indexes move when a turn appends or an edit
rewrites history. After any `turn.completed`/`turn.failed` frame a
client should re-fetch rather than merge.

`GET /api/providers` uses the same offset/limit envelope — the transport
slices the application's `InspectList` result (sorted by id). Provider
writes are rare, so offset drift is mostly theoretical, but the same
best-effort rule applies. `GET /api/tools` resolves the selected room's
registered catalog when `conversationId` is present and stays unpaginated.

### Settings semantics

`GET /api/settings` returns the **effective persisted** configuration —
the JSON file as stored on disk, merged over `default` struct tags;
what a restart would load, not a snapshot of the running process. It is
projected to leaves that are safe to show a browser: `server.*`
(`server.{port,bind,hosts,public_url}` — listen port, listen address, accepted host
aliases, and the public URL a reverse proxy answers at; the URL opens no
listener, and its host is folded into the accepted aliases), `conversation.*`, `service.*`, `log.*`, `notifications.desktop`, and
`models.*` (`models.{threads,chat}.{agent,provider,model}` — `threads` is what a new WebUI thread starts on;
`models.{utility,embedding,transcription,image,video,speech}.{provider,model}`). `server.hosts` is a JSON
array of host aliases without ports. It excludes server internals (`App`
metadata is never marshaled) and local paths (`storage.dir` — a
filesystem location, not a UI concern).

`PATCH /api/settings` takes `{"values": {key: value, …}}` where each
`key` is a dotted path to a **leaf inside the projected document** (e.g.
`conversation.max_turn_steps`, `server.hosts`, or
`models.utility.model`) and each `value` is a JSON literal — `null` is
rejected, and an empty `values` is `400`. Writes go through
`application/setting.SetMany`, which applies every key, validates the
whole resulting document once, and writes it atomically — all or
nothing, so a role's provider and model change together. Keys outside
the projected surface (`storage.dir`, section paths, unknown keys) are
`400`.

Timing nuance, documented because it is real behavior and not a promise
to fix: config writes take effect **on next process start** for most
sections, but `application/conversation` re-reads the conversation
section on every job post (`reloadConfig`), so `conversation.*` changes
apply to the *next submitted job*, `notifications.desktop` applies to
the next notification, `models.*` to the next chat thread or title it
picks a model for, while `server.*`, `service.*`,
`log.*`, and `storage.dir` wait for restart. The response to `PATCH` does
not imply the running process has already applied the value.

### Error model

All 4xx/5xx error responses are `application/problem+json` (RFC 9457
shape) with a stable machine `code`, the HTTP `status`, a
human-readable `detail` safe for display, and nothing else — no
`Cause`, no stack, no internal identifiers. (The `101` WS upgrade is a
successful non-2xx and has no body.)

| `status` | `code` | Meaning |
|---|---|---|
| 400 | `invalid_argument` | Malformed id, unknown enum, negative token count, non-user edit target, unknown command name or stray command arguments, unknown setting key, `null` value, … |
| 400 | `nothing_to_compact` | The room has no new foldable history (or none at all). |
| 400 | `nothing_to_clear` | The room is already empty. |
| 400 | `nothing_to_fork` | The room has nothing to copy. |
| 403 | `forbidden_origin` | `Host`/`Origin` check failed. |
| 404 | `not_found` | Conversation, provider, model-in-catalog, message id, or running turn does not exist. |
| 408 | `interrupted` | The request or turn was canceled (client cancel, shutdown race). |
| 409 | `busy` | A transport turn is live in the room; a native command waits for it to finish. |
| 409 | `confirmation_required` | A destructive command (`clear`) was called without `confirm`. |
| 409 | `unavailable` | The command does not apply to this room kind (compaction in an agent room). |
| 409 | `update_busy` | An update install is already running. |
| 409 | `update_disabled` | This build cannot install a release: a dev build, or a platform without a release binary. |
| 409 | `update_not_available` | The latest known release is not newer than this build. |
| 409 | `update_unknown` | No release has been looked up yet; call `POST /api/update/check` first. |
| 410 | `gone` | The job was queued on a room that was deleted underneath it. |
| 413 | `input_too_large` | Request payload beyond the server's input bound. |
| 429 | `queue_full` | The room's mailbox is full — retry later. |
| 500 | `internal` | Engine or deployment fault (adapter missing, tool loop limit, persistence failure). |
| 500 | `update_failed` | An update install could not start for an unexpected reason. |
| 502 | `upstream_error` | The provider call failed at the wire. |
| 503 | `unavailable` | The application is shutting down. |
| 504 | `upstream_timeout` | The provider did not answer inside its deadline. |

Upstream provider statuses pass through `apperror` and surface mapped to
this table — a provider `413` stays `413`, a provider `429` keeps status
`429` but reports `code: "upstream_error"` (a rate-limited provider, not
a full room queue), provider `401/403` collapses to `502 upstream_error`
with a dead-credential detail (the credential itself is never echoed),
and everything else upstream becomes `502`/`504`. The mapping exists so
a UI can route on a closed set, never on provider error text.

Success codes: `200` for reads and updates (`PATCH` rename, `PATCH`
settings, `PUT` provider — upsert is idempotent so there is no `201`),
`202` for an update install that started, `204` for deletes, `101` for the WS upgrade. Conversation detail includes
`canCompact`; it is true when the live tail is longer than the configured
keep-tail, including after a checkpoint when that tail still exceeds it.
The same response — and each item of the list projection — includes
`activeTurnId` and `activeTurnKind` (`send` or `edit`) when this
process's turn hub has a non-terminal turn for the room, so a client
can attach with `turn.watch`. Both fields are omitted when the room is
idle. They are not persisted conversation state.

## WebSocket surface

`GET /api/ws` upgrades to a single multiplexed socket carrying all turn
traffic. [`websocket.md`](./websocket.md) is the normative spec; the
shape in brief:

- Client commands: `turn.send`, `turn.edit`, `turn.resume`,
  `turn.cancel`, `turn.watch` — each carrying a client-chosen
  `requestId` that correlates the command with its replies. A malformed
  command (bad JSON, unknown `type`, invalid fields) is rejected with an
  `error` frame before any turn exists — it never produces `turn.opened`
  or `turn.failed`.
- Server stream: `turn.opened` means the command was accepted and an
  asynchronous submission scheduled — journaled and written before any
  other turn frame; it is **not** a worker-acceptance, persistence, or
  provider-start signal. Then `turn.delta` / `turn.phase` /
  `turn.reasoning` / `turn.tool_call` / `turn.tool_input` /
  `turn.tool_output` (ordered live argument/output chunks — progress,
  not completion; raw and summarized reasoning chunks in arrival order)
  / `turn.step_finished`, and exactly one terminal
  `turn.completed` or `turn.failed` per turn. Every turn frame carries
  `conversationId`, `turnId`, and a per-turn strictly increasing `seq`.
- Every terminal frame carries `resyncRequired: true` — stream frames
  never contain persisted message ids or tool results, so clients
  re-read via HTTP after every terminal frame.
- `turn.cancel` targets one exact `turnId` — including while it sits in
  the room's queue — and never interrupts an unrelated job. The reply is
  `turn.cancel_ack` (accepted), and the terminal `turn.failed` with
  `status: 408` still follows.
- `turn.watch` reattaches to a live turn from `afterSeq` against a
  bounded server journal; gaps, unknown turns, and expired journals
  answer `turn.resync_required`, after which the client re-reads via
  HTTP. Missing deltas are never fabricated.
- Disconnecting the socket never aborts a turn — sends run under a
  server-scoped context, and `userMessagePersisted` tells the truth
  about whether the user line committed.

## Implementation work required

Phases 1–4 are implemented in `infrastructure/delivery/http` and
`infrastructure/delivery/websocket`. Remaining gaps:

- ~~**The HTTP delivery itself.**~~ Done in phase 1: Gin wiring in
  `infrastructure/delivery/http`, `nusashell serve` as the `cmd` entry,
  the loopback bind, and the `Host`/`Origin` middleware described above.
- ~~**The WS hub.**~~ Done in phase 3: connection registry in
  `infrastructure/delivery/websocket/conversation`, fan-out by
  `conversationId`/`turnId`, goroutine-per-turn around blocking
  `Send`/`Edit`, `turn.opened` journaled before stream frames.
- ~~**Per-turn cancellation lives in the hub.**~~ Done in phase 4:
  server-scoped cancel per `turnId`; `turn.cancel` never maps to
  room-wide `ConversationApp.Cancel`.
- ~~**A bounded per-turn event journal** for `turn.watch` replay.~~ Done
  in phase 4: `turn.opened` at `seq` 0, bounded retention, expiry →
  `turn.resync_required`.
- ~~**Server-side turn ids.**~~ Done: `t_<base32>` minted at accept
  time via the domain ID rule.
- ~~**Projections.**~~ Done for reads in phase 1: message→DTO
  projection (owner-visible tool call records, attachment
  uri/name references, `optionalContent` dropped), the provider `keyState`
  derivation, and the settings leaf-projection.
- ~~**`optionalContent` preservation.**~~ Done in phase 2:
  `application/provider.Upsert` inspects the stored record and carries
  each surviving model's hidden `optionalContent` forward under a mutex
  that serializes same-process inspect→save. **Caveat:** the mutex is
  process-local — a concurrent `nusashell provider set` CLI process writing
  the same store can still interleave; close cross-process atomicity
  only if multi-writer operation becomes real. The wire is untouched by
  this rule.
- ~~**Non-blocking fan-out.**~~ Done: bounded per-subscriber buffers
  with drop-and-disconnect on overflow so a slow client cannot stall the
  provider read loop.
- **Swagger UI serving** of `openapi.yaml`, loopback-only.

## Build phases and acceptance matrix

Suggested order — each phase is shippable and testable on its own:

1. **Phase 1 — HTTP skeleton + reads. ✅ Done.** Gin wiring, `Host`
   check, `Origin` check on unsafe methods, `GET` endpoints (health,
   conversations, messages, providers, settings, tools), problem+json
   error mapping.
2. **Phase 2 — HTTP writes. ✅ Done.** `PATCH` rename, `DELETE`
   conversation, native commands (`POST /api/conversations/{id}/commands`),
   `PUT`/`DELETE` provider, `PATCH`
   settings — strict one-document JSON bodies, `Content-Type`
   `application/json` required, 1 MiB body cap, path ids validated
   against the domain identity rule.
3. **Phase 3 — WebSocket. ✅ Done.** Upgrade + `Origin` check,
   `turn.send`, `turn.opened` (seq 0), stream frames,
   `turn.completed`/`turn.failed` with the persistence receipt, hub
   fan-out with bounded admission (`error`/`queue_full`), ping/pong,
   frame limits.
4. **Phase 4 — Turn control. ✅ Done.** `turn.edit` (regenerate from a
   user message; terminal never claims `userMessagePersisted`),
   `turn.resume` (continue current history without rewrite; terminal
   never claims `userMessagePersisted`), `turn.cancel` (exact per-turn
   ctx cancel — never the room-wide interrupt — with `turn.cancel_ack`
   before cancellation), `turn.watch` journal replay under the hub lock
   with `turn.resync_required` on gaps/unknown/expired, live attach on
   truncated-but-active turns.
5. **Phase 5 — Swagger UI + polish.**

Acceptance checks (each maps to a contract clause):

| # | Check | Passes when |
|---|---|---|
| A1 | `Host` validation | `Host: evil.example` → `403 forbidden_origin` on any path; each configured `server.hosts` alias with the configured port passes. |
| A2 | `Origin` on unsafe HTTP | `POST`/`PUT`/`PATCH`/`DELETE` with absent or unconfigured `Origin` → `403`; configured host origins pass. |
| A3 | `Origin` on WS | Handshake without/with foreign `Origin` → rejected `403` before upgrade. |
| A4 | Bind and restart | Loopback (`127.0.0.1`) is the default bind; `server.port`, `server.bind`, and `server.hosts` apply after restart. |
| A5 | Conversation list paging | `offset`/`limit` honored, `limit` > 100 → `400`; page metadata `total`/`hasMore` correct. |
| A6 | Message ranges | `start=-50` returns the last ≤50; `effectiveStart`/`nextStart`/`hasMore` consistent with `total`. |
| A7 | Rename/delete/compaction | `PATCH` rename `200`; `DELETE` `204` then `404`; compaction `200` with updated projection on success, `400` nothing-to-compact (including repeat), `429` full mailbox. |
| A8 | Provider tri-state | `PUT` with absent `apiKey` keeps stored ref; `""` clears; literal vaults; `keyState`/`keySet` correct; no key material or raw ref in any response. |
| A9 | Settings | `GET` exposes `server`, `conversation`, `service`, and `log` leaves without `storage`/`App`/paths; `PATCH` leaf `200`, invalid values → `400`; writes atomic and schema-ordered. |
| A10 | Tools | Catalog exposes name/description/parameters/enabled/permission; nothing internal leaks. |
| A11 | WS lifecycle | An accepted `turn.send` → exactly one `turn.opened` (`seq` 0, journaled and written before any other turn frame) → ordered stream → exactly one terminal frame; `seq` strictly increasing per turn. |
| A12 | `opened` honesty | `turn.opened` = accepted + scheduled only: a full mailbox still ends the turn `turn.failed` `429` *after* `opened`, and a malformed command (bad JSON, unknown `type`, invalid fields) gets an `error` frame with no `turnId`/`seq` — never a `turn.failed` for a turn that never existed. |
| A13 | Cancel | `turn.cancel` on a running turn → `cancel_ack` then `turn.failed` `408`; on a queued turn same; on an unknown `turnId` → `error` frame `not_found`. |
| A14 | Disconnect | Closing the socket mid-turn does not abort it; `turn.watch` after reconnect replays or `resync_required`, and persisted state via HTTP is authoritative. |
| A15 | Edit preemption | `turn.edit` during a running turn: running turn ends `turn.failed` `408`, edit completes; both terminal frames carry `resyncRequired: true` and the client re-reads via HTTP. |
| A15b | Resume keeps history | After a failed turn that left tool results, `turn.resume` completes without dropping those messages; a retry may carry a new `providerId`/`modelId`. |
| A16 | Delete race | `DELETE` a conversation with queued turns → queued turns end `turn.failed` `410`. |
| A17 | Failed-send receipt | Provider failure after the user append → `turn.failed` with `userMessagePersisted: true`. |
| A18 | Projection redaction | Inject a sentinel string into each server-held field — stored `apiKey` material, a raw `env:`/`secret:` ref, an error cause, a model's `optionalContent`, and an attachment `sha256` pin — and assert it appears in no projected *metadata field* (`keyState`, `code`/`detail`, tool call refs, `parameters`, …). Provider reasoning is owner-visible content in ordered `Message.parts` and `turn.reasoning` frames; the check targets projected fields only, not all traffic. |
| A19 | Problem+json | Every error body parses as problem+json with a `code` from the catalog table. |
| A20 | Slow subscriber | A stalled WS client does not delay the provider read loop or other subscribers; overflow ends that subscriber's stream, not the turn. |
| A21 | Provider list paging | `GET /api/providers` honors `offset`/`limit` with the same `data`/`page` envelope as conversations; `limit` > 100 → `400`; sorted by id including `broken` records. |
| A22 | `optionalContent` preservation | Seed a stored model's `optionalContent` out-of-band, `PUT` a replacement catalog keeping that model `id` → the stored record still carries it; drop the model `id` → it is gone. No `optionalContent` field exists anywhere on the API surface. |
