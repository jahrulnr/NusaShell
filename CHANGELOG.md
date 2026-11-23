# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
