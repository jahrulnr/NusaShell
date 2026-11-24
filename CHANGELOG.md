# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1] - 2026-11-25

### Changed

- The app's mark is the icon the tab shows: the sidebar, the home view, sign-in, and first-run setup render `favicon.svg` itself, so the tab, the README, and the interface share one file. The old hand-drawn tile is gone.
- WebUI assets answer an `ETag` and the icon is cached for four hours (`public, max-age=14400`); scripts and stylesheets still revalidate on every load, so a rebuilt binary shows up at once.

### Fixed

- The file tools expand a path before resolving it. `~/…` now reaches the owner's home instead of creating a literal `./~` folder in the workspace, a `file:` URL becomes the local path it names, and on Windows `%VAR%` becomes its value (an unset variable is an error naming it — never a literal `%APPDATA%` folder). `$HOME`, `${HOME}`, `~user` and a `~` inside a path stay literal. One helper (`infrastructure/system/patharg`) does it for the file tools, `read_media`, the media tools' input paths and the HTTP preview.
- Tooltips are labels again: a bubble is a few words naming the control, leaves on its own after a few seconds, and never appears while the pointer is selecting text or dragging. The composer no longer explains slash commands in its `title` — a full sentence became a panel over the transcript.
- A steer is drawn once. The store writes a steered input as an ordinary user message carrying the `inputId` of the `turn.steer` command, and the journal replays the same steer to a tab that reloads mid-turn; the transcript now keeps the history row (annotation and uploads intact) and drops the live copy for that `inputId`, falling back to the live bubble while the row is not loaded. `turn.steer_recall` removes the live bubble the same way, and a steer this tab sent shows the annotation picks it carried — the journal frame carries the screenshot alone, so the marks come from what the tab submitted. A record with no `inputId` is never matched — an agent room folds several steers into one follow-up line, which stands for none of them by id, so its live bubbles stay until the turn ends.
- The thread title and the @handle keep their own draft while being edited: a refresh that lands mid-edit no longer rewrites the field under the cursor, and the new value is sent only when the owner commits it (Enter or leaving the field; Escape cancels).
- Thread refreshes compare before they write: an answer that says what the room and its rows already say changes nothing (no new row object, no header swap, no render), while a different answer is applied. A read that started before a local change is read again instead of put back over it, so a rename, pin, or model pick is no longer undone by a refresh — and a rename updates the open room and every list row at once.
- Neighbouring text is drawn as one block. The server seals the text of a finished step into its own part, and each part was parsed as its own Markdown document: a sentence split at a step boundary came out as two paragraphs, and a fenced block split there came out as two broken blocks — with a stray blank line added by "Copy response". Parts of one answer are now joined before grouping and copying, while a tool, steer, plan, media, approval, or elicitation between two texts still keeps them apart.
- A steered input keeps its place in the live transcript. The record row is drawn where the live steer bubble sat, with its annotations and uploads, instead of above the text that streamed before it, and it is not drawn a second time in its history place; once the turn ends the row is drawn from history again, in message order.
- A turn a tab started watching late is drawn once. A reload (or a second tab) replays the journal from its first frame while history already holds the steps that finished before it, so the same work appeared twice — the prose repeated and one run of tool calls split into two "Used N tools" rows that only merged once the turn ended. The live tail now begins after the last tool call the record holds; anything the record cannot vouch for is left alone, and a marker saying output is missing stays.
- A message can no longer fake the renderer's chrome. Copy buttons, diagram boxes, footnote references and media placeholders are marked with a nonce drawn for each render, and anything else wearing those attributes or class names — HTML a model or the owner wrote — loses them: text that mentions `<div data-diagram>` is drawn as the text it is, and a "copy" button it writes does nothing. A diagram box without the renderer's own shape is skipped instead of taking the whole transcript down halfway through a reconcile.
- A retried provider call starts its answer over. The store persists only the attempt that succeeds, so when the turn announces `retry_wait` the live transcript drops what the abandoned attempt streamed — its text, its reasoning, and tool rows still waiting for a result — up to the last segment the store already holds. The next attempt fills the transcript from there, and the two never splice into one answer.
- A failed turn leads with what the provider said. The status and machine code sit underneath as facts, and a failure frame that carries no message says so instead of passing "the turn failed" off as an explanation.
- "Jump to latest" appears only when the reader actually left the bottom, and once they leave nothing takes the position back from them. A layout nudge (the browser's scroll anchoring, a tool card settling, a pin whose scroll event arrives late) is not read as scrolling away, and — the other way round — a position this page wrote, or one that moved because the window or the keyboard changed size, no longer counts as the reader returning: only their own scroll back down to the bottom follows again. A slow wheel step, each one landing within a few pixels of the bottom, is no longer undone by the pin the next delta applies.
- A live turn this tab did not start keeps the answer above it, and its prompt shows while it runs. The transcript used to trim history at the newest user message it had read, which — before the server wrote a relayed prompt — was the previous turn's, so the reply in front of the running turn disappeared until a reload.
- Steered input renders as the owner's message: Markdown, a "From thread" card when another thread relayed it, attachments and copy, instead of raw text while the turn runs.

## [0.1.0] - 2026-11-24

### Added

- **Delete all threads** in Settings → General → Data. Confirm with `DELETE`; NusaShell stops active turns before removing thread-owned uploads, picks, recordings, and saved views. If a thread cannot be deleted, its files are kept and the failure is reported.
- **Cross-thread activity** in Settings → Tools shows which thread called which `conversation` tool, the target, result, and duration.
- **Public URL support** with `server.public_url`; `nusashell serve --open` opens the WebUI after startup or reuses the server already listening on the port.
- **Server-backed CLI management** for automations, thread models, conversations, and WhatsApp or Telegram channels. These commands use the running server rather than writing to the database.
- **Windows installation and updates**, including amd64 and arm64 release assets, a per-user background service, and an in-app updater. Linux installation adds a per-user launcher and icon.
- **Owner password management** in Settings → General → Account, with session logout on other browsers.
- **First-run setup** for connecting an API model or ACP agent.
- **Agent sign-in and session controls** in Settings → Agents, including sign-in terminals, session options, auto-start, and idle shutdown.
- **Suggested thread titles** use the configured utility model after a thread's first reply, unless the owner has renamed it.
- **New-thread runtime defaults** in Settings → Models. The choice applies across devices; a model picked in a thread stays local to that thread.
- **Saved thread drafts and views** across reloads and devices, including references, quotes, uploaded files, open panes, and the browser's last page.
- **Quote replies and side chats** from selected message text, plus voice input in the composer and copy-as-Markdown for completed turns.
- **Cross-thread prompts** appear as cards that identify and link to the sending thread.
- **Memory tools and suggestions** let threads save and find memories, review proposed updates, and keep memory disabled for selected threads.
- **Web search settings** configure providers, keys, limits, and a live test for `web_search` and `web_fetch`.
- **Browser history suggestions and a console** in the browser panel.
- **Live agent pointer** shows clicks, typing, and other browser actions in the shared view.
- **Browser control** can be started from a thread header and supports live takeover, device emulation, profiles, saved logins, network and WebSocket routes, init scripts, and browser recordings. Chrome can be installed from Settings → Tools or with `nusashell browser install`.
- **Browser recordings** capture page activity and can be attached to a message for model context.
- **Browser annotations** add marked elements, regions, or screenshots to the next message.
- **Built-in MCP tools** give threads access to their conversations and browser. Owners can also connect external MCP servers over stdio, streamable HTTP, or SSE; ACP agents and model runtimes can use supported servers.
- **Projects and folders** organize related threads and give agents access to the project's workspace. Deleted projects can be restored from the undo notice.
- **Composer references and attachments** support threads, projects, skills, MCP servers, files, folders, and images or documents.
- A **search tool for models** finds text by regular expression and files by glob in the thread's project folders.
- **Browser, Files, and Terminal panes** provide access to a thread's workspace. Threads can also be exported as JSON.
- **ACP agents** run alongside API models and appear beside API models in the picker, with approvals, session recovery, steering, and agent-specific capabilities. Agent titles and notices appear in the thread.
- **Agent attachments** are passed according to each agent's supported image, audio, and text inputs; agent-generated media stays with its reply.
- **WhatsApp and Telegram channels** support pairing, allowlists, thread handles and routing, model selection, approvals, quoted replies, and live turns.
- **Chat media and history** include incoming and outgoing files, WhatsApp message search, delivery status, queued sends, and native approval polls.
- **Telegram inline mode** can find threads and projects by handle.
- **Automations** run scheduled model or agent turns, keep run history, and can deliver results to linked chats.
- **Desktop notifications** report completed or failed turns, agent approvals, automation results, and new releases.
- **Model roles** can be assigned for chat, utility, embeddings, speech-to-text, and image, video, and speech generation.
- **Media tools** generate images and video, speak text, and transcribe audio or video using the configured model roles.
- **Application storage** uses SQLite with embedded schema migrations.
- **Owner login** protects the WebUI and API when enabled.
- The OpenAPI document now covers the HTTP API routes.

### Changed

- `PATCH /api/settings` accepts a validated map of dotted keys and applies them in one write.
- `read_file` supports line-based paging; successful `exec` calls with no output report their exit status. File search respects `.gitignore` and skips dependencies, caches, binaries, and oversized files.
- Model catalogs classify models by supported output and role; non-chat models stay out of the chat picker.
- Context compaction reserves room for replies and accounts for image attachments. Large images are resized before provider requests, and the default first-token timeout is longer.
- Provider terminology is consistent across settings, CLI, storage, and API routes; aggregator backends are called upstreams.
- Message attachments and tool-provided media appear in the thread and open in the preview overlay.
- User messages render Markdown while preserving literal footnote definitions, Mermaid fences, and reference selections.
- Settings navigation, model pickers, dialogs, and form controls use the WebUI's own components. The model picker distinguishes API and ACP runtimes; panes resize by dragging, and wide tables scroll within their container.
- Browser device controls follow the active session; responsive mode fits the browser pane.
- Channel allowlists reject invalid IDs instead of silently dropping them.
- Secret values now use AES-256-GCM; values stored before this change must be entered again.

### Fixed

- WebSocket turn replay now recovers from large or gapped histories with bounded snapshots, and reconnect retries use backoff.
- Thread-list and room refreshes no longer fan out on reconnects or bursts of events; concurrent reads are deduplicated.
- Browser startup handles cold launches, cancellation, and stale profile locks more reliably. Profiles also work when the data folder is on a filesystem without symlink support.
- Browser annotations resolve to the saved pick and its screenshot.
- Relayed thread titles stay on one line and no longer include embedded prompt text.
- Long turns can compact more than once, preserve reply capacity, and fit checkpoint history to the model's context window.
- Context usage follows the active request, including agent-reported usage.
- File and tool I/O now handles Windows line endings, invalid UTF-8, file growth, special files, and large command output without hanging or exhausting memory.
- MCP tools with invalid schemas are skipped, and name collisions receive stable distinct names. Web-search errors no longer echo provider credentials.
- Send, queue, and steer preserve the same draft and attachments; an unconsumed steer returns to the draft.
- Thread deletion now updates project lists and waits for running agent sessions to close. Agent sessions are also cleaned up when threads are edited, moved, or deleted.
- Editing and retrying turns in ACP threads starts the expected agent session and preserves the thread's workspace.
- Agents receive project folders when supported; duplicate session IDs cannot be shared across threads.
- Model choices and per-thread effort or upstream settings persist across reloads and apply to turns started from chats or automations.
- Changing a model during a running turn applies at the next safe point instead of timing out.
- Agent permission forms stay in order with the agent's messages; scrolling during a streamed reply no longer jumps to the bottom.
- A failed first message returns to the composer with its text and selections intact.
- WhatsApp pairing waits for login before showing connected; channel allowlists and group settings remain visible after saving.
- Saving settings without a config file reports the required restart setup instead of an internal error.
- Onboarding uses the correct default Anthropic Messages endpoint.
- Windows updates replace a running executable safely, retain required environment variables, and stop child processes when an agent stops.
- Image attachments are converted to provider-supported formats before sending.
- Browser input can submit an empty value when Enter is pressed on a focused page element.

### Removed

- `models.media` is replaced by `models.image`, `models.video`, and `models.speech`; old `models.media` configuration is rejected.
- The `channels` runtime settings and the default chat runtime setting are replaced by `models.chat`.
- Settings → Projects has been removed; projects are managed from the sidebar.
- `product.mode` and its Settings page have been removed; old `product` configuration is rejected.
- The terminal REPL and slash commands have been removed. Running `nusashell` without arguments starts the WebUI server.
