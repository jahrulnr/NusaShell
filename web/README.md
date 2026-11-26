# NusaShell WebUI

Browser client for `nusashell serve`, embedded into the binary (`embed.go`)
and served from the same loopback origin as the HTTP API and the `/api/ws`
turn socket. No npm, no build step, no CDN — every dependency is vendored
under `vendor/` (see `vendor/README.md`).

## Stack

- **Preact + htm** (`vendor/preact/htm-preact.mjs`) — components as tagged
  templates, no JSX compile.
- **Tailwind v4 browser build** — utilities compile in the page; theme
  tokens live in `index.html` (`@theme`) and map to the CSS variables in
  `styles/app.css`, which also owns rendered Markdown, syntax colors, and
  third-party widget styling.
- **marked + DOMPurify + highlight.js** — Markdown rendering.
- **Pele** — Mermaid-syntax diagrams, rendered to SVG locally.
- **slim-select** — styled `<select>` replacement (`components/ui.js` `Select`).

## Caching

Every asset answers an `ETag` (a content hash of the embedded file), so a
reload costs a `304` instead of a re-download. Scripts and stylesheets stay
`no-cache`: a rebuilt binary must show up at once, and a stylesheet served
from cache next to a fresh script pairs new markup with stale rules. The
icon is the one asset with no cross-file contract, so it alone is
`public, max-age=4h` — and revalidates by its `ETag` after that.

## Popovers

`Popover` (`components/ui.js`) opens from its wrapper on wide screens and as a
bottom sheet on narrow ones. Beside its wrapper it opens on the side
`placement` asks for unless its content does not fit there and the other side
is clearly better (`lib/placement.js`), and it is capped at the room on the
side it took, measured after every render while open, so the cap follows the
content and the anchor, and a tall panel never runs off the screen. The panel
is the one scroll owner: its parts do not scroll themselves (no `overflow-y-auto`
or `max-h` on a part), and what must stay reachable is `sticky` (the model
picker's search row and footer). A popover whose anchor can hide while open
takes its open flag from `usePopoverOpen(show)`, which closes it when the anchor
hides. Put a group of labelled controls in a two-column grid with the label above
a `dense` `Select`. `FolderBrowser` names its scroll owner: `scroll="panel"` in a
popover, `scroll="list"` in a dialog body; there is no default.

## No native widgets

Nothing the browser draws as chrome appears in the app, so it looks the same
everywhere and obeys the theme: no `alert`/`confirm`/`prompt`, no native
`<select>` (use `Select`; `dense` for toolbars), number, date, time, or
checkbox input (use `NumberField`, `DateTimeField`, `TimeField` from
`components/fields.js` and `Checkbox` from `components/ui.js`), no `title`
tooltip bubble (`lib/tooltip.js` takes any `title` over and draws its own, so
keep writing `title=` — a few words naming the control, never an explanation:
a long one lands as a panel over the content the reader was reading, and the
bubble leaves on its own after a few seconds; put explanations in the
interface), and no `controls` on media (`components/player.js`).
Ask a yes-or-no question with `askConfirm()` from `lib/store.js`, which
`ConfirmHost` renders; forms carry `noValidate` so the browser never shows its
own validation bubbles. The exceptions are the file picker behind Attach, the
PDF viewer in a PDF preview, and the links a terminal opens.

## Layout

```
index.html            shell: tokens, vendored scripts, #app
app.js                theme + layout + boot
lib/
  store.js            one mutable state tree, batched emit(), useStore()
  api.js              fetch wrapper → ApiError(status, code, message); timeouts
  socket.js           reconnecting turn socket
  chat.js             controller: routing, rooms, every socket command and frame
  views.js            a thread's draft and side pane, kept on the server
  turn.js             frames/history → transcript items; tool summaries
  markdown.js         block-incremental Markdown → sanitized DOM
  diagram.js          Pele seam: preflight gate, SVG scrubber, locked options
  providers.js        provider API styles, named once for every form
  settings.js         the settings document a page edits, and dotted reads
  tooltip.js          the app's own tooltips, taking over `title`
components/
  sidebar.js          thread list (paged), new thread, settings
  chat.js             thread header/actions, ChatView, HomeView
  transcript.js       messages, thoughts, tool groups, status, failure card
  selectionbar.js     card over selected message text: reply as a quote, dictate, side chat
  toolcard.js         tool call row + terminal-style input/output card
  composer.js         draft, send/steer/queue/stop, pending tray, voice input
  pickers.js          model picker, workspace browser, context meter
  settings.js         providers, general settings (account, password), tools, about/update
  agents.js           agents: edit, process status, sign-in (in a terminal here)
  terminal.js         xterm terminals: a thread's shell, an agent's sign-in
  browser.js          thread's browser: live view, address bar with history, console, agent pointer, marks, recording
  fields.js           NumberField, DateTimeField, TimeField
  player.js           video/audio player for the preview overlay
  ui.js, icons.js     primitives
```

Routes are hash-based: `#/` (new thread), `#/c/<id>`, `#/settings/<section>[/<arg>]`.
The server answers a browser navigation to any other path with the shell
and `404`, so the app routes it: a path-style thread link (`/c/<id>`) is
rewritten to `/#/c/<id>` and opened, and any other path shows the
not-found view (`MissingView`).

## Behavior

- **Send, steer, queue, stop.** Idle: Enter sends. While a send turn runs,
  Enter steers that exact turn (`turn.steer`) and Ctrl/⌘+Enter queues for
  after it; while anything else keeps the room busy, Enter queues. Stop
  cancels the room's turn. Send, Queue, and Steer carry the same text,
  selected references, and attachments. The queue is per room and in memory only; it
  drains oldest-first after a clean finish and is held after a failure or
  stop until the user sends or presses *Send next*. A head the server
  refused before opening goes back to the front, ahead of later messages;
  when dispatch isn't possible the queue stays on hold for *Send next*.
- **Steering receipts.** Pending steers can be recalled; a fold removes them
  from the tray. Recall and a terminal snapshot that confirms a steer was not
  consumed return its complete input to the draft, not to the queue. An
  unknown outcome is never resent automatically.
- **Native commands.** `/compact`, `/clear`, and `/fork` are NusaShell's own
  actions, not turns: a draft whose first line is one of those names runs it
  (`POST /api/conversations/:id/commands`) and never reaches the model or the
  agent — a refusal keeps the draft, and only a 200 spends it. The "/" menu
  lists them above the agent's commands and the file/skill/MCP matches, each
  row naming its source (NusaShell, or the room's agent) and, when the room
  cannot use it, the server's reason (`/api/commands?conversationId=`), which
  the arrows step over. `/clear` asks with a danger dialog first; a fork opens
  the new thread. The header's *Compact history* calls the same endpoint.
- **Drafts follow the thread.** A room's unsent draft — text, references,
  quotes, files that finished uploading, and an attached browser
  recording (the RecordingBar's *Attach* picker in the live view; it
  sends as `recordingId` and can never steer) — its open side pane, and the
  browser's last page are saved to the server 0.7 s after a change and on
  leaving the page (`lib/views.js`), and restored when the room first opens
  in a page: after a reload, or on another device. A draft typed before the
  kept one arrived is never replaced, and a pane is not reopened on a
  narrow screen, where it would cover the transcript.
- **Failures.** `turn.failed` (except a user stop) shows a card with
  *Retry* (`turn.resume`, keeps history) and *Dismiss*. A send whose user
  line never persisted returns its text to the draft. When that send was
  a thread's first, the server drops the thread, so the view goes back to
  the home composer with the text, the model choice, and the project, and
  the error as a toast.
- **Reconnects.** Live turns re-watch from their last `seq`; a send still
  waiting for `turn.opened` has unknown delivery, so its text returns to the
  draft with a warning; such commands are never replayed automatically. A
  Stop without an acknowledgement can be pressed again after reconnecting.
  An unconfirmed retry keeps its earlier error unless a replayed terminal
  frame confirms the retry ran. Every terminal frame re-reads history over
  HTTP.
- **Rendering.** Assistant prose is GFM Markdown plus GitHub-style footnotes
  (`[^id]`), sanitized by DOMPurify against a tag allowlist: inline HTML
  such as `<kbd>`, `<del>`, `<dl>`, `<details>` renders, while images are
  never fetched, scripts/styles/forms are dropped, and only the renderer's
  own class names survive. Links are limited to http(s)/mailto/same-origin
  paths. Closed
  ```` ```mermaid ```` fences render through Pele after the preflight gate
  in `lib/diagram.js`; anything it rejects shows as source. User text gets
  the owner-typed profile (`lib/usermark.js`): same sanitizer and allowlist,
  but single newlines break, a typed `[^id]:` or ```` ```mermaid ```` stays
  literal, and reference selections ride the parse as private-use sentinels
  that become `.useref` spans after sanitize (or strip out where the label
  landed inside markup). Raw text stays the source of truth — copy, edit,
  and send never read the rendered DOM.
- **Media.** What a tool showed the model (a `read_media` file, a browser
  screenshot) shows in its card and as thumbnails under a folded run of
  tools; message attachments use the same component (`components/media.js`).
  Anything clicked opens the preview overlay, never a new tab.
- **Tool I/O** renders as escaped text, collapsed into "Used N tools"
  groups between prose. Each call expands to a terminal-style card
  (`components/toolcard.js`): shell commands show as highlighted bash
  "Command in <workspace>", other tools as pretty JSON; long input shows its
  head and long output its tail behind a fade, each expandable in place.
  The result's trailing status line — `(exit N, 4.4s)`, `(no output, exit 0, …)`,
  `(signal: killed, …)`, `(timed out …)`, `(interrupted by user|agent …)`,
  `(nusashell restarted …)`, `(canceled …)` — becomes the card footer and the
  row's own word (`exit 1`, `killed`, `timed out`, `stopped`), with the wall
  time on the row.
- **History paging.** A thread opens on its newest 60 messages; older pages
  load automatically as the reader nears the top (skeleton placeholder,
  inline retry on failure) while the line being read stays in place.
- **Browser panel.** The address bar suggests pages the session's profile
  visited (recent on focus, matching while typing; arrows and Enter pick,
  Esc closes), and so does the start form. **Console** opens a drawer
  under the live view with the page's log, polled every 2 s and starting
  over with each page, and a prompt that runs JavaScript in the page
  (Enter runs, Shift+Enter adds a line, ↑/↓ recall). When the agent
  clicks, hovers, or scrolls, a labeled cursor glides to the spot and
  fades after 3.5 s of stillness.
- **Side panels.** On desktop the threads sidebar and the thread's side
  pane (Browser/Files/Terminal) drag from a divider on the panel's edge
  next to the chat; the handle also takes arrow keys (Shift for bigger
  steps, Home/End for the bounds), and the width is remembered. The chat
  keeps at least the space its CSS reserves. The browser panel's device
  picker tracks the session's preset from the live-state socket: Responsive
  sizes the tab to the pane and follows it as the pane resizes, while the
  fixed presets and Custom keep their viewport. On narrow screens the
  sidebar is a drawer (under `md`) and the side pane an overlay (under
  `lg`), and no divider shows.
- **Preferences** (theme, last model) are the only thing in localStorage.
- **Delete all.** Settings → General → Data holds a "Delete all threads"
  action guarded by typing `DELETE`: `DELETE /api/conversations` removes
  every room (busy turns are preempted, not skipped) and purges uploads,
  page marks, recordings, and saved views; only after the server confirms
  does the client drop its rooms, drafts, and lists and return home — a
  failed purge keeps them all (`lib/chat.js:deleteAllRooms`).
- **Cross-thread activity.** Settings → Tools lists what one thread asked
  of another (`GET /api/conversation-control/activity`, last ~50): time,
  caller, tool, target — each id links to its room — outcome, and
  duration. Message text and tool arguments are never logged, and the
  ring empties with a delete-all.

## Development

```sh
make build && NUSASHELL_WEB_DIR=web ./bin/nusashell serve --port 18080
```

`NUSASHELL_WEB_DIR` serves the UI from disk instead of the embedded copy, so
edits show on reload. `make test-web` syntax-checks the sources.
`make lint-web` (Biome unused imports/vars + Knip module graph) uses
devDependencies under `web/node_modules` only — still no runtime npm or
build step; `vendor/` remains what the browser loads.

## Tests

`make test-web` (part of `make check-web` / `make check`) syntax-checks
every source and runs `tests/*.test.mjs` with Node's built-in runner.
`tests/register.mjs` lets the modules load under Node (their `/vendor/...`
imports are paths under `web/`), and `tests/env.mjs` stands in for the
browser: `location` with `hashchange`, `localStorage`, a scriptable
`fetch` (`mockApi`), and a `WebSocket` the test opens and feeds frames by
hand. `chat.test.mjs` drives the real controller that way;
`turn.test.mjs`, `references.test.mjs`, and `api.test.mjs` cover the pure
modules. What needs a DOM — components, `follow.js`, Markdown rendering —
is checked in the browser.
