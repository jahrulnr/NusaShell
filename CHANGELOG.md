# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A model or session option picked while a turn runs is kept as pending and applies to the **next** turn: the answer says 202 with `pending`, the room keeps both the running value and the waiting one through a reload, and the marker disappears on its own once the turn consumes it. A pick the agent no longer accepts is dropped with a notice instead of failing the turn.
- An option a room's agent does not offer yet (a reasoning effort that only exists once a model is chosen) is saved as deferred, shown as "not offered — saved for when it is", and applied when the agent starts offering it.
- Stored agent picks carry their order, and the model is applied first: an option that only exists once a model is chosen (a reasoning effort) is now reached in the same turn instead of being skipped, and the list is read again after each change until it settles (bounded).
- A compacted thread shows where its history was folded: a rule labelled `Compacted`, with the checkpoint kept behind it instead of a plain note in the transcript.

- Sending a message to a busy thread queues it instead of failing. The answer names one of `steer`, `turn`, or `queued` with its position, a full queue answers 429 with its depth, and a room shows how many messages wait — a Stop cancels the running turn but leaves them. `queued` is an acceptance, not a promise: a restart before the turn runs loses the message.
- `exec` results always end with a status line that carries the duration, and an interruption names who stopped it — the owner, the agent, a NusaShell restart, or a caller that went away — instead of a bare signal.
- A tool row says how it ended (`exit 1`, `timed out`, `stopped`, `restarted`, `canceled`) beside its clock. Colour is reserved for real failures, and the outcome is readable at 390px and by a screen reader, not only on hover.
- `exec` and `exec_session` refuse pattern kills (`pkill`, `killall`, `fuser -k`, `kill -9` on anything but a literal or looked-up pid) and teach the safe path. `allow_broad_kill: true` is the explicit escape hatch; `kill -9 -1` is never allowed.
- `read_file` and `read_media` each do the other's job instead of telling the model to try the other tool: a non-text payload opened with `read_file` is attached, described, or transcribed for a turn whose model cannot take it, and `read_media` on a text file returns the same pages `read_file` returns. Choosing the wrong one costs nothing. In the recorded history this replaces 95 wasted round-trips across 12 rooms ("use read_media", then "use read_file") with none.
- A model that cannot see an image or hear a sound still gets an answer: the file goes to the vision or transcription role as text, never as an attachment the model would reject. The `describe_media` tool is gone — one tool decides, because the model is no longer asked to guess what it can read.
- An omitted file says which reader works for it: images name `read_media`, audio names `transcribe`, and video names none because nothing reads it. The placeholder used to name one tool for every modality.
- Settings → Models gains the vision role and one switch, `media.reading_consent` (off by default), covering image descriptions and audio transcripts. Files leave the machine only when the turn's model asks for one and the switch is on; the tool refuses with a message naming the switch otherwise.
- A message sent to another thread can carry files. A target on this machine gets the sender's path pinned by sha256, so the receiver pays tokens and the sender pays a string; a target elsewhere gets an uploaded copy; `http(s)` references pass through. Directories, devices, and FIFOs are refused, as are more than four attachments, a file over its modality's cap, or more than 32 MiB in one message. A message that carries files always queues, because a running turn cannot take a new attachment.
- A tool row names the room it talks to instead of showing its id: `@dog "FE – …"` when the room has a label, the title alone when it has none, and a short id only when both are missing. A row still waiting for its result shows the short id until the answer arrives, and the label is never truncated — the title is, at 390px.
- An agent's session mode is one value in one place. The composer chip and the model picker read the same list and write through the same path, so a change shows in both at once instead of needing a second pick, and the control goes read-only while a turn runs rather than sending a change the request timeout drops.
- `nusashell config set` and `PATCH /api/settings` name the setting and the type it expects when a value has the wrong shape — `config key "media.reading_consent" expects a boolean value like false` — instead of answering only `invalid value`. The CLI prints that sentence once.
- A generated binary can no longer live in the tree: `make artifacts` fails the checks on any tracked or to-be-added ELF/Mach-O/PE file (it runs inside `make check`), and `AGENTS.md` now states the rule — build into `bin/` or outside the repo, never "solve" an artifact with a `.gitignore` line. The committed `fake-acp-agent` binary is gone; its source builds from `cmd/fake-acp-agent`.
- An `exec_session` can opt into reporting its exit: `wake: true` (off by default) leaves a completion record carrying the exit status and the tail of its output, capped at 2000 characters, and an idle room is woken to read it. The wake spends at most three machine-origin turns in a row before the record waits for a natural turn, a busy room is never interrupted, and a deleted room takes its pending records with it — a session that does not ask reports nothing.

- Skill improvement has a third mode, `suggest`, set with `skills.improvement`. `off` stays the default, and `observe` only logs, when an eligible finished turn shows a signal. In `suggest`, the loop may propose a new learned skill or a change to one after an eligible finished turn. Only an owner's accept writes `<storage.dir>/skills/<name>/SKILL.md`, and a dismissal needs a reason. Proposals are decided over HTTP; the Skills page shows how many wait and has no controls to decide them yet.
- `skills.catalog_max_bytes` (default `32768`, must be positive) is the skill catalog's byte budget, read at startup and used only in `suggest`. The bytes of every live skill's name and description count toward it, and a draft or an accept that would take the catalog over the budget is refused.

### Changed

- A prompt another thread hands over rides the room's running turn again: it is folded in at the next safe point, with the queue kept only as the fallback when no turn is live. A relayed prompt is one line — a `userType: relay` record carrying the sender as `relay{id,label,title}`, written when the room accepts it for either runtime, so a stop between accept and fold can delay, never lose it; the turn that folds it reads that record instead of writing a second one, and renders the sender's head from it. A full steering mailbox refuses at accept with 429 and nothing written.
- A prompt relayed from another thread stores only the words its sender wrote. The `[From thread …]` header is no longer kept inside the message: it is put back when a request is built, from the sender the message records, so the receiver's model still knows who asked while the message itself — and a copy or a search of it — carries the sender's text alone. Threads that already hold a header inside the message keep showing it as it was until the store is repaired at the next upgrade window — nothing is lost.

- A published release is the full binary: the workflow builds with the symbol-map tags read from the Makefile, so what users download carries the tree-sitter index instead of falling back to the regex-only build. `build-lean` stays a local choice.
- `exec` caps a call at 300 s (was 600 s) and `write_stdin` polls for at most 60 s, so a command that never exits cannot hold a turn for ten minutes. A capped poll now says "no new output" instead of staying silent.
- Only the installed binary may install itself. An instance running from anywhere else refuses the install with a message naming both paths, and a restart is only taken through the service manager that owns this process.

### Fixed

- A queued or cross-room send no longer fails falsely with `send completed without a persistence receipt`: the job-owned live journal now seals with the same persistence receipt the send job committed, while the guard itself remains in place.
- Agent slash-command catalogs now mirror the agent instead of inventing rows: an agent room has no native `compact` row unless its session reports one, an agent-reported command wins a name collision with a native thread command, and an agent that reports nothing leaves only `clear` and `fork`.
- An agent-reported slash-command list is kept as conversation state after the agent process idle-stops or NusaShell restarts: the room serves the last list that same agent reported, a newer live report replaces it instead of merging, and an agent that never reported commands still shows none.
- Chat-completions requests no longer send an unconditional prompt-cache TTL. Providers that support the extension accept different TTL vocabularies (and some reject the request outright), so the option stays absent until provider capability and TTL configuration are explicit.
- A pick the agent does not offer yet is kept as deferred and reported as such, instead of being counted as applied and silently ignored — the turn still runs.
- A file an agent hands over with no type (a voice note as `application/octet-stream`) is named by its bytes and file name, so it reaches the tool that can transcribe it instead of being stored as an opaque blob.

- An agent option no longer reverts to an older value: every session update and response carries its place in the wire order, so a late notification cannot overwrite the response that came after it — the same guard covers the session mode.
- An option an agent stops offering disappears instead of lingering: a session update replaces the offered list, so the picker stops showing a choice the agent would refuse with `-32602` and fail the turn.

- A restart no longer reports itself as an owner stop: shutdown names its cause, a cut tool keeps the output it already captured, and a cancelled stream keeps its partial reply instead of losing it.
- The tool status line now reaches the live row, so a timed-out or stopped command no longer looks successful until the page is reloaded.
- `project_context` is written once per change instead of once per step — eight edits now leave one record instead of eight, and one live tail drops from 11.8k to 1.5k tokens.
- The failed-turn card no longer appears under an answer that already arrived, and Retry only shows while the last prompt is still unanswered.
- Retry is not offered for a `Compacted` divider. A checkpoint is history, not a prompt waiting for an answer, so the failed-turn card retries the prompt that is actually unanswered — the one before the fold — instead of a divider an agent room could only refuse with "nothing to retry".
- The update flow waits for the server to answer with a new version and reloads itself; "What's new" renders as Markdown instead of raw text.
- Whether a prompt came from another thread is the server's word (`userType: "relay"`), not something the transcript reads back out of the text: a message the owner starts with `[From thread …]` keeps its Edit, and a relayed prompt without that head loses it. Relayed prompts written before this release carry no `userType`, so they render as ordinary owner text and stay editable; the store is repaired at the next upgrade window.
- A native command leaves the composer when it is sent and becomes a receipt above the input — `Compacting history…` with the elapsed time after two seconds, the command name no longer repeated — so the box is empty and focused while it runs. A command the server refuses goes back where it can be acted on: into the box if it is empty, otherwise the receipt stays with the reason, the command line it would resend, and a Retry (only when resending cannot run the action twice); an unknown outcome puts the text back and says so, with no Retry. The header's status chip is gone — the receipt is the one place a running command shows. Enter while a command runs queues the message instead of sending it into a refusal.
- A prompt relayed from another thread shows what it carried inside its own block, under the thread's head, instead of above the bubble in the owner's lane where the uploads would read as the owner's. A relayed prompt offers no edit either — it is not the owner's message to rewrite. The owner's own prompts are unchanged.
- A relayed attachment keeps its promise. An agent target that opens the file with its own tools has the pinned digest checked first, and a file that changed or vanished is left out with a note instead of being handed over as a different file.
- A send that is refused no longer leaves its uploaded copy behind. A thread whose queue is full answers 429 with the depth and with nothing written; the copies this call made are taken back, and a copy the room already owned is never touched.
- Audio named `.m4a` is read as audio instead of as an unreadable generic file, so a voice note reaches the tool that can transcribe it. The content sniffer that the media tools already had is now the only one, in the domain layer, used by both doors — the two copies disagreed, and the one on the `read_file` path was the wrong one.
- `exec` still ends with its status line when a run is long enough to suggest `exec_session`: the hint sits above that line, so a 25-second command that failed reads as `exit 3`, not as success without an exit code.
- A clipped tool result says where the rest of its output went, and every tool says it the same way: `full output: <path>`, followed by how to read it (`read_file`, or `grep` through `exec`), and a spill file that hit its own cap says so in that same line (`spilled output (truncated at 32 MiB)`). `exec` used to end with a bare path that read like an argument, while `web_fetch` used different words for the same thing.
- A message that waits behind another thread's turn is visible in the room it waits in: `3 waiting to run` while the queue holds work, and the answer appears when the queue drains — the tab refetches once as the count reaches zero, which happens after the queued turn finished. The count is part of the room's state (room view, thread list, and the MCP thread list), so a deep link or a first load shows it at once; `conversation.queued` remains the nudge that reports a change after the tab is open.
- An empty envelope is no longer written beside every tool result: `tool_output` and delivery records carry no `data` field at all, and a message line without one is refused with a named error instead of a panic. Records written before this still load.
- A media role can be set to Off again. The picker wrote an `effort` leaf on roles that have none, so clearing Speech to text, Image, Video, or Embedding answered "not a writable settings leaf" and the role stayed set; one function now decides which leaves a role owns.
- Vision can be assigned from the Models page. The role listed only models of a `vision` type that does not exist, so its picker was always empty and the card told the owner to set a type under Providers — which could not be done. A vision model is a text model whose input modalities include an image, and a model assigned outside the page now shows its name instead of `(not found)`.
- Settings → Agents opens again on a machine where agents have saved session state: the list read every document in the agents' folder, and a stored session catalog sits in that same folder, so one saved catalog answered "Could not load agents", made detection answer "Detection failed", and left the agent picker empty. The list now reads only the documents that describe an agent.
- A room whose agent has not offered anything yet no longer claims "this agent offers no session options": that sentence is now reserved for a definitive answer, while a live session that reports nothing right now gets its own transient line with a refresh beside it. An empty report can no longer overwrite the stored catalog either, so the options do not vanish from the next idle room. The composer's options chip follows the same rule through `agentChipVisible`: it stays visible on a transient `empty` report — the refresh is still something to act on — and hides only when the agent's definitive answer is an empty list.
- A session-option marker no longer claims "next turn" during the turn that already carries the pick: the pick applies when the turn starts, so the marker is gone while it is in play instead of asserting it still waits — and if the turn fails to land, the pick becomes visible again rather than being lost. Edit and resume consume the same parked pick; a fold does not. A pick parked while that turn ran is a newer generation and survives — `ClearPendingSelection` compares the consumed generation under the store's lock and leaves the newer file.
- A pick parked while a room was busy can no longer overwrite a newer pick made while the room is idle: applying the idle pick supersedes the parked generation field by field — the runtime, the effort, the upstream, or the session options it just set — while anything it did not cover stays parked and keeps its notice at the next turn.
- The strip above the composer is a single line again: `roomLine` joins the room's blind-work note and its queue depth into at most two clauses instead of stacking a `WorkingLine` and a `QueuedLine`, and `workingBlind` no longer fires for a turn this tab is already watching, so a watched room does not claim it cannot be watched. The live/idle orb in the sidebar, the recent list, and the transcript now all read the same `watching` predicate.
- A model picked for an agent-bound room now survives the turns that use it. Hydration restored the provider/model fields and then bound the agent, so `BindAgent`'s "agent changed" cleanup ran over values that were just restored and erased `modelId` — providerId, effort, and upstream with it — on every load, and the next save wrote the pick away. The agent is now bound first, before those fields are restored; picks already lost stay lost.
- Picking a model no longer reads as the panel closing and reopening: the upstream select stays mounted once a model's list ever came back non-empty (`upstreamControl`), so a pick, a refetch, or the frame between keeps the same node instead of unmounting the control. The popup's invariants are locked by tests that run — same dialog node, `scrollTop` kept, focus still inside, and a `MutationObserver` that fails on any structural removal.
- The agent session-options panel keeps its frame while the list scrolls: the search field and the `Providers / Agents` footer stay put and only the body between them moves, so a wheel over the panel no longer drags its chrome along — checked at 1440×900 and at 390×844.

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
- A room says how many messages wait behind its turn: `conversation.queued` puts a quiet line under the transcript ("3 waiting", gone when the queue drains), so a Stop is never silent. The number is the room's current one — last notice wins, no ordering — and it is drawn by a line that subscribes to it alone, so a count moving never rebuilds the transcript, and a stop tool's answer feeds the same number instead of a second one.
- A tool row says how its call ended, not only in the card: `exit 1`, `killed`, `timed out`, `stopped`, `stopped by agent`, `restarted`, `canceled`, `no result` sit before the clock on the row itself, in a colour family (red for a failure, amber for a timeout, neutral grey ring for a run this side stopped, none for a call with no result), and a folded group counts `N failed` or `N timed out` from the same value. The row's word and its footer come from one function, so they cannot disagree; Escape closes an open card and returns focus to its row, and the dot is decoration — the word carries the meaning.
- A failure card only appears for a prompt that is still waiting for an answer, and offers Retry only then. A turn the server never ran — its user line was never written — returns the prompt to the draft and says why in a toast instead of leaving a card that offers to send it again, and a server shutting down (503) is not reported as this turn's failure either; answers already in the room keep the transcript as it is.
- A tool row reads the status line the runner closes its output with, in every shape it writes — `(exit 1, 43.2s)`, `(signal: killed, 3.2s)`, `(timed out after 120s — …)`, `(interrupted by user|agent|nusashell restarted|…)`, `(canceled after …)`. A nonzero exit counts as a failure again (the line used to be matched only without its duration), while a run this side stopped reads as one — "Interrupted — user", "Timed out after 120s", "Canceled" — instead of a red failure, and the "N failed" count in a folded tool group follows the same rule.
- A running tool row says how long it has been running — `exec · 33s`, one second at a time — so a call that prints nothing (everything redirected to a file) no longer looks the same as one that hung. Only a running row owns a timer: its label keeps its own one-second interval and is unmounted the moment the call finishes or the turn ends, and a finished row shows the duration the runner measured — its own status line, fixed — so a call the tab replayed or resynced still reads the 43s it took instead of the 0s the tab's clock knows about.
- Installing an update no longer leaves the page staring at nothing. The install state dies with the process that restarts, so the page followed a `succeeded` that a restarted server never reports; it now reloads on the one thing that survives — the version the server answers with — after waiting a bounded time for it. A failed install stays on screen with its error, and an install that never lands offers a Reload instead of waiting forever. "What's new" is rendered as Markdown in a panel that scrolls, instead of showing the release notes' raw text.
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
