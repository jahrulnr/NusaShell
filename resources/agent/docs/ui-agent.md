# Agent

The chat workspace view: multi-conversation chat against any configured provider model. Each conversation keeps its own history on disk and can use skills, memory, docs, plugin-provided MCP tools, and spawned ACP subagents or internal delegates.

**How to open:** Click the Agent item in the left sidebar.

## Conversations pane

Lists conversations with a search filter and a New conversation button. The count reflects Agent rooms only; pipeline agent-step conversations are omitted. On narrow widths the pane hides behind a Rooms button and a backdrop; Escape or choosing a room closes it. Ctrl/Cmd+K focuses this search; Ctrl/Cmd+N creates a thread. In the experimental widget shell, a confirmed successful delete immediately removes its row even if the following list refresh fails; the deleted room cannot reappear. Failure to load the next room is reported separately from the successful deletion. Room actions live in one local menu there: each row's ellipsis button opens it anchored to the trigger, and right-click on the row opens the same menu at the pointer with Rename and Delete items. Arrow keys rove focus, Escape or an outside press closes it, and closing restores focus to the opener so Tab continues native traversal from the trigger.

- **Conversations label** (`#conversations-title`):
  - Type: text

- **Thread count** (`#conversation-count`):
  - Type: text
  - Notes: Counts Agent rooms only. Pipeline agent-step conversations are omitted.

- **New conversation** (`#new-conversation-btn`):
  - Type: button
  - Action: Starts a temporary local draft and focuses the composer; the durable conversation is created when the first user message is sent.
  - Shortcut: Ctrl+N / ⌘N on the Agent view

- **Conversation search** (`#conversation-search`):
  - Type: search
  - Shortcut: Ctrl+K / ⌘K, or / when not typing
  - Notes: Filters conversation titles in real time.

- **Conversation list** (`#conversation-list`):
  - Type: list
  - Notes: Interactive Agent rooms only. Pipeline agent-step conversations do not appear here.

- **Rooms** (`#agent-rooms-toggle`):
  - Type: button
  - Action: On narrow widths, opens the conversations pane as a drawer. Escape or the backdrop closes it.

- **Rooms backdrop** (`#agent-rooms-backdrop`):
  - Type: overlay
  - Action: Click to close the conversations drawer.

## Room info and mini window

A small floating control cluster at the top of the conversation area, hidden until a conversation is active. The Room info trigger opens a diagnostics popover titled with the conversation name: a Tools metric (tool calls in the loaded history, bookkeeping calls excluded), a Compactions metric (archived pre-compaction chunks), and the conversation ID with a copy-to-clipboard button. The popover closes on its X button, an outside click, or Escape. The mini window button opens a picture-in-picture mini window: on Chromium the live thread and composer move into an always-on-top picture-in-picture window; elsewhere a small popup window loads the app in mini mode. In the experimental widget shell the move also re-adopts every widget's constructed stylesheets: they are rebuilt with the PiP document's own CSSStyleSheet constructor from cached text (no refetch) on entry and again on return, so widgets stay styled across repeated open/close cycles.

- **Room info cluster** (`#agent-room-info`):
  - Type: container
  - Notes: Floating control cluster at the top of the conversation area; hidden until a conversation is active.

- **Room info** (`#agent-room-info-trigger`):
  - Type: button
  - Action: Toggles the conversation diagnostics popover.

- **Room info popover** (`#agent-room-info-popover`):
  - Type: dialog
  - Notes: Conversation diagnostics: tool count, compaction count, and conversation ID. Closes on its X button, an outside click, or Escape.

- **Room info title** (`#agent-room-info-title`):
  - Type: text
  - Notes: Shows the active conversation title.

- **Close room info** (`#agent-room-info-close`):
  - Type: button
  - Action: Closes the diagnostics popover and returns focus to the trigger.

- **Tool call count** (`#agent-room-tool-count`):
  - Type: text
  - Notes: Tool calls in the loaded conversation history; bookkeeping calls are excluded.

- **Compaction count** (`#agent-room-compaction-count`):
  - Type: text
  - Notes: Number of archived pre-compaction chunks for the conversation.

- **Copy conversation ID** (`#agent-room-id-copy`):
  - Type: button
  - Action: Copies the conversation ID to the clipboard.

- **Conversation ID** (`#agent-room-id`):
  - Type: text
  - Notes: Shown inside the copy button of the room info popover.

- **Mini window** (`#mini-window-btn`):
  - Type: button
  - Notes: Opens the picture-in-picture mini window (native PiP on Chromium, popup fallback elsewhere) showing the live agent thread. Appears beside Room info for an active browser conversation; hidden in Electron until its mini-window flow is supported.

## Thread

Renders the active conversation. An empty thread offers starter prompt chips that fill the composer.

Long conversations open showing only the most recent history, scrolled to the bottom. The complete trailing assistant run remains visible even when it contains dozens of tool or reasoning messages, so a finished answer is not hidden behind the history control. A 'Load older messages' button at the top reveals older in-memory messages, then archived pre-compaction chunks, one batch at a time. Chunks are never auto-loaded after a turn finishes or mid-turn compaction. User messages and isolated ACP prompt events preserve Markdown as friendly HTML: inline backticks become code pills and fenced code becomes a bounded, horizontally scrollable code card. Thinking and compaction handover markdown are parsed only when their expandable disclosures are opened. While the backend compacts, the active room shows an animated 'Context automatically compacting' row; the generic Thinking loading dots are suppressed so only one loading status is visible, and the row is removed when compaction completes or fails. When the provider is actively constructing a tool call before its arguments are valid, the room shows a transient animated activity row and cycles short status copy every five seconds; it contains no partial tool arguments and disappears when the tool card or answer is available. show(op=pdf) results render as a styled native PDF preview with a download link; DOCX/PPTX and other office previews are not enabled without a reliable renderer. Live streaming auto-follows only while the view is pinned to the bottom. Wheel, touch, keyboard, and scrollbar movement upward explicitly detaches follow; returning to the bottom re-enables it. Follow retries across layout settling, and a refresh preserves a detached reader's position without forcing a scroll during live-to-complete transition.

Local file links open a type-aware bounded preview. The preview kind and size badge are decided from the file header without downloading the body for media (image/audio/video/PDF); Markdown and text read the full body, and the filename extension is the final fallback. Markdown renders to HTML (including Mermaid diagrams with a readable inline baseline); text opens in a read-only syntax-highlighted code editor; image/audio/video/PDF use their native preview; and unrecognised binary data stays download-first. Closing the popup aborts the load. Tables retain intrinsic width inside a horizontal scroller; images and Mermaid diagrams offer a Zoom action that keeps vector diagrams sharp. Closing any zoom, artifact, audio, video, or text-preview overlay returns keyboard focus to the element that opened it, so keyboard navigation resumes from the trigger instead of the page body.

Live completion refreshes the thread's authoritative state without disturbing what is on screen. Interrupted live streams recover without losing or duplicating content; when a live stream cannot be replayed, the thread reloads instead of applying out-of-order updates.

- **Thread log** (`#agent-thread`):
  - Type: log
  - Notes: Renders the active conversation messages. Long conversations are windowed: the last user bubble plus a short tail of a finished turn render on open (scrolled to bottom). A running turn keeps every persisted round of that turn mounted on reload. Older finished-turn rounds, earlier turns, and archived chunks load on demand via Load older or scroll-to-top. User and isolated ACP prompt events render Markdown as HTML; inline backticks use code pills and fenced code uses a bounded horizontal-scroll card. Thinking and compaction handover markdown are parsed when expanded; the expanded Thinking body shows up to 20 rendered lines and scrolls internally beyond that without clipping list numbers behind the scrollbar. Live Thinking updates follow the thread only while the view is at the bottom; scrolling up pauses auto-follow until the user returns to the bottom. Completion/error resyncs the scroll position before sound and refresh so a reader stays on the message being read. Refreshes also preserve which Thinking and tool disclosures the user opened. Markdown table cells are capped at 400px and wrap long content; multi-column tables remain inside a local horizontal scroller so the scrollbar stays available. Local Markdown file links open a bounded text preview with styled tables and Mermaid diagram rendering. Tool cards render predictable list, status, code, document, and media output formats; tool output attachments use the same media rendering as the thread. During backend compaction, an animated inline status shows until the completed or failed state arrives; the generic Thinking dots are mutually exclusive with that status. Before a valid tool card exists, provider-side tool construction shows a transient animated activity row with five-second copy rotation and no partial arguments. An empty thread shows starter prompt chips.

- **Starter prompts** (`#agent-starter-prompts`):
  - Type: button group
  - Action: Fills the composer with a ready-to-edit prompt. Available on an empty thread.

- **Load older messages** (`#agent-load-older`):
  - Type: button
  - Action: Reveals the previous batch of older in-memory messages (first older rounds of a finished turn, then earlier turns), then archived pre-compaction chunks, keeping the scroll position anchored. Shown while older messages or unloaded chunks remain; hidden once the oldest history is reached. A running turn already mounts its full persisted round list, so this button is for earlier complete turns and archives.

## Tool call strip

Shows in-flight MCP or built-in tool calls for the current turn. Built-in tools render as compact execution events, open by default: the head row (status node, action title, elapsed) collapses the card to a single line, and the body leads with the primary argument as a path line followed by the result without any nested toggle; the raw request and output stay folded inside a show-request-and-raw-output details. Tool markers are independent rather than joined by a continuous rail, so a long tool round stays visually calm. Colour coding marks the kind of work: blue for inspection and connectivity, brown for local authored work, amber for scheduled work, and cyan for direct agent collaboration; green is reserved for success. A card keeps the disclosure state chosen by the user when it settles, so completion does not collapse an expanded result or remove a reader's vertical context. Every tool card keeps its elapsed indicator in the card chrome; standalone Ask Question cards place it in their header. Before a valid card exists, provider-side tool construction is represented by a transient animated activity row that cycles status copy every five seconds without exposing partial arguments. exec streams its command output live into a terminal output panel inside the same timeline event paths (with a per-call Stop button while running), and MCP calls render the same event with a MCP badge and the tool ref as the path line. ACP delegation and internal delegate runs are represented by one clickable subagent card per run and share the same dock, drawer, popup, and transcript; subagent steer/stop/wait and result bookkeeping calls stay out of the transcript so they do not duplicate that card. Ask Question cards render their question, option descriptions, and submitted answer as Markdown while keeping the interaction controls native and accessible. Completed show(op=pdf) calls become a native PDF preview card instead of a terminal row. Tool cards keep chronological order inside their owning assistant round: terminal cards group into stacks, and a standalone card (subagent, media, artifact, ask) splits the stack so later terminals follow it. Live rounds and reloaded transcripts use the same placement. A late completion for a tool that already appears in history updates that card where it sits, even after a later round or a steer, and never adds a copy at the live tail. A card that has already settled is not revived by a late start event or stream update, and its disclosure state is kept.

Streaming tools (exec) display their live output as it is produced and offer a per-call Stop button while running; the button cancels only that tool call, marks it interrupted by user, and lets the enclosing agent turn continue. The composer Stop remains the separate whole-turn cancellation control.

- **Composer stack** (`#agent-composer-stack`):
  - Type: container
  - Notes: Holds ACP dock, tool strip, todo strip, steer queue, and composer.

- **Tool calls strip** (`#tool-job-strip`):
  - Type: container
  - Notes: In-flight tool calls for the current turn.

- **Tool calls toggle** (`#tool-job-strip-toggle`):
  - Type: button
  - Action: Collapse/expand the tool calls list.
  - Notes: Default collapsed.

- **Tool calls title** (`#tool-job-strip-title`):
  - Type: text

- **Tool calls meta** (`#tool-job-strip-meta`):
  - Type: text

- **Tool terminal stop** (`.agent-tool-stop`):
  - Type: button
  - Action: Cancels only this tool call and lets the enclosing turn continue. Shown only on streaming tools (exec) while the tool is running; the button hides once the tool settles.
  - Notes: Appears inside the tool terminal card's summary row, next to the elapsed timer.

## Background subagent dock

Appears above the composer when an ACP subagent or internal delegate is live (or recently finished in this room). Each chip opens the right-hand drawer; the peek control opens a popup for one run. Live chips show the current phase and elapsed time so provider thinking or connection gaps are distinguishable from a settled run.

- **Background subagent dock** (`#acp-dock`):
  - Type: container
  - Notes: Hidden until at least one live or recently finished ACP or internal delegate run exists.

- **Background dock toggle** (`#acp-dock-toggle`):
  - Type: button
  - Action: Collapse/expand the subagent chip list.

- **Background dock title** (`#acp-dock-title`):
  - Type: text

- **Background dock meta** (`#acp-dock-meta`):
  - Type: text

- **Open background run drawer** (`#acp-dock-open-drawer`):
  - Type: button
  - Action: Opens the right-hand multi-subagent drawer.

- **ACP dock chips** (`#acp-dock-list`):
  - Type: list

## ACP live views

The drawer reuses the conversation-pane layout: subagent runs are listed newest first in a left sidebar and the selected transcript fills the right pane. The delegation prompt and each persisted steering prompt appear as isolated Markdown-rendered prompt events, not main-conversation user bubbles, and long prompts scroll inside a bounded panel. Assistant text, reasoning, and tools reuse the normal Agent conversation structure. ACP subagents and internal delegates stream into the same transcript, so thinking, text, tool starts, tool output, and terminal status update in place. ACP terminal rows stay compact and expose their full command through an accessible `Show full tool input` disclosure, with a scrollable monospace expanded state and a quieter raw-output fold below. Switching the parent Agent room while the drawer is open rebinds the sidebar to that room; its empty marker appears only when the new room has no runs. Growing text and reasoning chunks update in place, tool updates stay on one row, and token usage appears as run metadata instead of transcript noise. Completed cards retain their run ID and fetch the persisted transcript when the backend cache is empty or has restarted. On phones, the run picker becomes a compact horizontal strip and the transcript owns the vertical scroll, so the selected run gets most of the drawer. Live output sticks to the transcript tail only while that run's scroll pin is armed; scrolling up by wheel, touch, keyboard, or scrollbar releases it and returning to the bottom re-arms it. The peek popup focuses one run. Run state recovers from missed live events: a gap in the live stream reloads fresh state instead of applying out-of-order content. A list or get response that was requested before a reconnect, a room switch, or a newer live update for the same run is ignored, so a stale response cannot revert a terminal run to running or overwrite newer streamed content. A run that is waiting for permission counts as live in the dock and drawer. While the selected run's transcript is loading the drawer shows Loading transcript; if the request is superseded by a newer stream state it is reissued for the current connection without a click. A genuine failure shows the retry message, and the drawer does not retry it automatically; clicking the card again retries.

- **Background run drawer overlay** (`#acp-drawer-overlay`):
  - Type: overlay
  - Action: Click to close the subagent drawer.

- **Background subagent drawer** (`#acp-drawer`):
  - Type: drawer
  - Notes: Uses the Agent conversation-pane pattern with a run sidebar and a live transcript pane.

- **Close background run drawer** (`#acp-drawer-close`):
  - Type: button
  - Action: Closes the subagent drawer.

- **Background drawer title** (`#acp-drawer-title`):
  - Type: text

- **Background drawer subtitle** (`#acp-drawer-subtitle`):
  - Type: text

- **Background run peek overlay** (`#acp-popup-overlay`):
  - Type: overlay
  - Action: Click outside the dialog to close the peek popup.

- **Close background run peek** (`#acp-popup-close`):
  - Type: button
  - Action: Closes the peek popup.

- **Background peek title** (`#acp-popup-title`):
  - Type: text

## Task checklist

A compact strip showing the agent's todo list for the current conversation. Items come from the `todo` tool; the user can delete items from the UI.

- **Task checklist toggle** (`#agent-todo-strip-toggle`):
  - Type: button
  - Action: Collapse/expand the task list.
  - Notes: Default collapsed.

- **Task count** (`#agent-todo-strip-count`):
  - Type: text

- **Task progress meta** (`#agent-todo-strip-meta`):
  - Type: text
  - Notes: Shows open count or 'All done'.

- **Task list** (`#agent-todo-strip-list`):
  - Type: list
  - Notes: Items come from the `todo` tool; user-deleted items disappear.

## Steer queue

When a user sends a steer message mid-turn, it is queued here and applied at the next safe tool-round boundary. Finished background results and harness announcements are inserted before the steer, then the steer becomes the newest user instruction for the next provider request. Once applied, the user bubble anchors the next assistant round: stale waiting dots from the completed round are removed and exactly one new waiting round appears after the user bubble. An empty leftover round is removed only when it has no reasoning, text, or tool card; a round that holds a settled subagent or media card, or a terminal stack after an earlier empty stack, is kept in its position before the steer.

- **Steer queue** (`#agent-steer-queue`):
  - Type: container
  - Notes: Queued steer messages waiting for a safe boundary.

- **Steer queue toggle** (`#agent-steer-queue-toggle`):
  - Type: button
  - Action: Collapse/expand the steer queue.
  - Notes: Default expanded.

- **Steer queue title** (`#agent-steer-queue-title`):
  - Type: text

- **Steer queue state** (`#agent-steer-queue-state`):
  - Type: text

- **Steer queue text** (`#agent-steer-queue-text`):
  - Type: text

- **Cancel queued steer** (`#agent-steer-cancel`):
  - Type: button
  - Action: Cancels the pending steer message.

## Composer

The message input with attachments, model picker, workspace selector, provider status, stop, and send buttons. Provider status shows used/window context tokens and remains visible as a compact status on narrow screens. In the experimental widget shell the preflight estimate also carries a per-category breakdown — system, conversation, and tool-definition tokens — from the same canonical request estimator, as an estimate rather than provider-measured attribution. Ctrl+Enter (⌘↩ on Mac) sends. While a turn is running, Send becomes Steer. The whole conversation area accepts drag & drop of files and folders — folders are attached as path-only references (desktop only).

- **Composer form** (`#agent-form`):
  - Type: form

- **Message input** (`#composer-input`):
  - Type: textarea
  - Shortcut: Ctrl+Enter / ⌘↩ to send; Escape closes dialogs

- **File input** (`#agent-file-input`):
  - Type: input
  - Notes: Hidden file picker for images, PDFs, and text files.

- **Attach files** (`#agent-attach-btn`):
  - Type: button
  - Action: Opens the file picker.

- **Model picker trigger** (`#model-trigger`):
  - Type: button
  - Action: Opens the model menu.

- **Model label** (`#model-trigger-label`):
  - Type: text

- **Model menu** (`#model-menu`):
  - Type: dialog
  - Notes: Lists models grouped by provider with effort chips.

- **Provider route trigger** (`#route-trigger`):
  - Type: button
  - Action: Toggles the per-conversation routing menu. OpenRouter models offer Auto or an upstream provider; Codex models offer Auto account rotation or a strict account selection. The choice is restored from the active room instead of leaking across room switches. Other single-provider models show a non-interactive home icon.

- **Provider route menu** (`#route-menu`):
  - Type: dialog
  - Notes: Lists searchable upstream providers for the selected model with quantization, latency, throughput, and per-provider input/output pricing badges; Auto restores gateway load balancing and has no single fixed price. Listing failures show 'No provider in this model' instead of a raw upstream error.

- **Workspace selector** (`#agent-workspace-btn`):
  - Type: button
  - Action: Opens the in-app workspace folder browser (server-side directories).

- **Workspace label** (`#agent-workspace-label`):
  - Type: text

- **Provider status / context usage** (`#agent-provider-status`):
  - Type: text
  - Notes: Shows backend context usage as used/window (e.g. 32k/1M), or 'Context automatically compacting' while the active room is compacting. A provider-measured fill from the last completed round is authoritative; before the provider responds, a provisional preflight estimate is marked with '~'. The preflight is calculated from provider-visible content and never counts embedded media as text. The displayed number never sums message bubbles.

- **Stop** (`#stop-btn`):
  - Type: button
  - Action: Cancels the running turn; partial output is kept and marked interrupted.

- **Send** (`#send-btn`):
  - Type: submit
  - Shortcut: Ctrl+Enter / ⌘↩
