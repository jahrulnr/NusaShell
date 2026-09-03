# Agent

The default view: multi-conversation chat against any configured provider model. Each conversation keeps its own history on disk and can use skills, memory, docs, plugin-provided MCP tools, and spawned ACP subagents or internal delegates.

**How to open:** Open NusaShell, or click the Agent item in the left sidebar.

## Conversations pane

Lists conversations with a search filter and a New conversation button. The count reflects Agent rooms only; pipeline agent-step conversations are omitted. On narrow widths the pane hides behind a Rooms button and a backdrop; Escape or choosing a room closes it. Ctrl/Cmd+K focuses this search; Ctrl/Cmd+N creates a thread.

- **Conversations pane** (`#agent-conversations`):
  - Section: Agent
  - Type: container

- **Conversations label** (`#conversations-title`):
  - Section: Agent
  - Type: text

- **Thread count** (`#conversation-count`):
  - Section: Agent
  - Type: text
  - Notes: Counts Agent rooms only. Pipeline agent-step conversations are omitted.

- **New conversation** (`#new-conversation-btn`):
  - Section: Agent
  - Type: button
  - Action: Creates a new conversation and focuses the composer.
  - Shortcut: Ctrl+N / ⌘N on the Agent view

- **Conversation search** (`#conversation-search`):
  - Section: Agent
  - Type: search
  - Shortcut: Ctrl+K / ⌘K, or / when not typing
  - Notes: Filters conversation titles in real time.

- **Conversation list** (`#conversation-list`):
  - Section: Agent
  - Type: list
  - Notes: Interactive Agent rooms only. Pipeline agent-step conversations do not appear here.

- **Rooms** (`#agent-rooms-toggle`):
  - Section: Agent
  - Type: button
  - Action: On narrow widths, opens the conversations pane as a drawer. Escape or the backdrop closes it.

- **Rooms backdrop** (`#agent-rooms-backdrop`):
  - Section: Agent
  - Type: overlay
  - Action: Click to close the conversations drawer.

## Thread

Renders the active conversation. An empty thread offers starter prompt chips that fill the composer.

Long conversations open showing only the most recent history, scrolled to the bottom. The complete trailing assistant run remains visible even when it contains dozens of tool or reasoning messages, so a finished answer is not hidden behind the history control. A 'Load older messages' button at the top reveals older in-memory messages, then archived pre-compaction chunks, one batch at a time. Chunks are never auto-loaded after a turn finishes or mid-turn compaction. User messages and isolated ACP prompt events preserve Markdown as friendly HTML: inline backticks become code pills and fenced code becomes a bounded, horizontally scrollable code card. Thinking and compaction handover markdown are parsed only when their expandable disclosures are opened. While the backend compacts, the active room receives a room-scoped WebSocket status and shows an animated 'Context automatically compacting' row; the generic Thinking loading dots are suppressed so only one loading status is visible, and the row is removed when compaction completes or fails. When the provider is actively constructing a tool call before its arguments are valid, the room shows a transient animated activity row and cycles short status copy every five seconds; it contains no partial tool arguments and disappears when the tool card or answer is available. show(op=pdf) results render as a styled native PDF preview with a download link; DOCX/PPTX and other office previews are not enabled without a reliable renderer. Live streaming auto-follows only while the view is pinned to the bottom. Wheel, touch, keyboard, and scrollbar movement upward explicitly detaches follow; returning to the bottom re-enables it. Follow requests are frame-coalesced and retry across layout settling, while a refresh preserves a detached reader's position and does not force a scroll during live-to-complete transition.

Local Markdown file links open in a bounded text preview: GFM tables retain intrinsic width inside a horizontal scroller, and completed Mermaid fences render lazily when the bundled renderer is available.

Live completion refreshes authoritative state without replacing a connected live node. Delta rendering stays dirty across transient DOM detaches; SSE sequence gaps, duplicate frames, malformed frames, and premature EOFs are recovered from the last contiguous cursor, with a bounded snapshot fallback when replay is no longer available.

- **Agent shell** (`#agent-shell`):
  - Section: Agent
  - Type: container

- **Active conversation** (`#agent-conversation`):
  - Section: Agent
  - Type: container

- **Thread log** (`#agent-thread`):
  - Section: Agent
  - Type: log
  - Notes: Renders the active conversation messages. Long conversations are windowed: the last user bubble plus a short tail of a finished turn render on open (scrolled to bottom). A running turn keeps every persisted round of that turn mounted on reload. Older finished-turn rounds, earlier turns, and archived chunks load on demand via Load older or scroll-to-top. User and isolated ACP prompt events render Markdown as HTML; inline backticks use code pills and fenced code uses a bounded horizontal-scroll card. Thinking and compaction handover markdown are parsed when expanded; the expanded Thinking body shows up to 20 rendered lines and scrolls internally beyond that without clipping list numbers behind the scrollbar. Live Thinking deltas follow the thread only while the invisible end marker is visible at the bottom; scrolling up pauses auto-follow until the user returns to the bottom. Completion/error events resync the actual scroll geometry before sound and refresh so a reader stays on the message being read. Refreshes also preserve which Thinking and tool disclosures the user opened. Markdown table cells are capped at 400px and wrap long content; multi-column tables remain inside a local horizontal scroller so the scrollbar stays available. Local Markdown file links open a bounded text preview with styled tables and lazy Mermaid rendering when the bundled renderer is available. Tool cards load the versioned backend catalog from agent.tools.contracts, carry an isolated contract-owned class plus request/result hooks, and render predictable list, status, code, document, and media output formats; tool output attachments use the tool result renderer. During backend compaction, a room-scoped WebSocket event drives an animated inline status until the matching completed or failed event arrives; the generic Thinking dots are mutually exclusive with that status. Before a valid tool card exists, provider-side tool construction shows a transient animated activity row with five-second copy rotation and no partial arguments. An empty thread shows starter prompt chips.

- **Starter prompts** (`#agent-starter-prompts`):
  - Section: Agent
  - Type: button group
  - Action: Fills the composer with a ready-to-edit prompt. Available on an empty thread.

- **Load older messages** (`#agent-load-older`):
  - Section: Agent
  - Type: button
  - Action: Reveals the previous batch of older in-memory messages (first older rounds of a finished turn, then earlier turns), then archived pre-compaction chunks, keeping the scroll position anchored. Shown while older messages or unloaded chunks remain; hidden once the oldest history is reached. A running turn already mounts its full persisted round list, so this button is for earlier complete turns and archives.

## Tool call strip

Shows in-flight MCP or built-in tool calls for the current turn. Built-in tools render as compact execution events, open by default: the head row (status node, action title, elapsed) collapses the card to a single line, and the body leads with the primary argument as a path line followed by the result without any nested toggle; the raw request and output stay folded inside a show-request-and-raw-output details. Tool markers are independent rather than joined by a continuous rail, so a long tool round stays visually calm. Ocean marks inspection and connectivity, volcanic soil marks local authored work, sand marks scheduled work, and reef marks direct agent collaboration; mangrove remains reserved for success state. A card keeps the disclosure state chosen by the user when it settles, so completion does not collapse an expanded result or remove a reader's vertical context. Every tool card keeps its elapsed indicator in the card chrome; standalone Ask Question cards place it in their header. Before a valid card exists, provider-side tool construction is represented by a transient animated activity row that cycles status copy every five seconds without exposing partial arguments. exec streams its command output live into a terminal output panel inside the same timeline event paths (with a per-call Stop button while running), and MCP calls render the same event with a MCP badge and the tool ref as the path line. ACP delegation and internal delegate runs are represented by one clickable subagent card per run and share the same dock, drawer, popup, and transcript hydration; subagent_wait, delegate_result, and synthetic subagent_result bookkeeping stay out of the transcript so they do not duplicate that card. Ask Question cards render their question, option descriptions, and submitted answer as Markdown while keeping the interaction controls native and accessible. Completed show(op=pdf) calls become a native PDF preview card instead of a terminal row.

Streaming tools (exec) display their live output as it is produced and offer a per-call Stop button while running; the button cancels only that tool call via agent.tools.stop, persists interrupted by user, and lets the enclosing agent turn continue. The composer Stop remains the separate whole-turn cancellation control.

- **Composer stack** (`#agent-composer-stack`):
  - Section: Agent
  - Type: container
  - Notes: Holds ACP dock, tool strip, todo strip, steer queue, and composer.

- **Tool calls strip** (`#tool-job-strip`):
  - Section: Agent
  - Type: container
  - Notes: In-flight tool calls for the current turn.

- **Tool calls toggle** (`#tool-job-strip-toggle`):
  - Section: Agent
  - Type: button
  - Action: Collapse/expand the tool calls list.
  - Notes: Default collapsed.

- **Tool calls title** (`#tool-job-strip-title`):
  - Section: Agent
  - Type: text

- **Tool calls meta** (`#tool-job-strip-meta`):
  - Section: Agent
  - Type: text

## Background subagent dock

Appears above the composer when an ACP subagent or internal delegate is live (or recently finished in this room). Each chip opens the right-hand drawer; the peek control opens a popup for one run.

- **Background subagent dock** (`#acp-dock`):
  - Section: Agent
  - Type: container
  - Notes: Hidden until at least one live or recently finished ACP or internal delegate run exists.

- **Background dock toggle** (`#acp-dock-toggle`):
  - Section: Agent
  - Type: button
  - Action: Collapse/expand the subagent chip list.

- **Background dock title** (`#acp-dock-title`):
  - Section: Agent
  - Type: text

- **Background dock meta** (`#acp-dock-meta`):
  - Section: Agent
  - Type: text

- **Open background run drawer** (`#acp-dock-open-drawer`):
  - Section: Agent
  - Type: button
  - Action: Opens the right-hand multi-subagent drawer.

- **ACP dock chips** (`#acp-dock-list`):
  - Section: Agent
  - Type: list

## ACP live views

The drawer reuses the conversation-pane layout: subagent runs are listed newest first in a left sidebar and the selected transcript fills the right pane. The delegation prompt and each persisted steering prompt appear as isolated Markdown-rendered prompt events, not main-conversation user bubbles, and long prompts scroll inside a bounded panel. Assistant text, reasoning, and tools reuse the normal Agent conversation structure. Growing text and reasoning chunks update in place, tool updates stay on one row, and token usage appears as run metadata instead of transcript noise. Completed cards retain their run ID and fetch the persisted transcript when the backend cache is empty or has restarted. On phones, the run picker becomes a compact horizontal strip and the transcript owns the vertical scroll, so the selected run gets most of the drawer. The peek popup focuses one run.

- **Background run drawer overlay** (`#acp-drawer-overlay`):
  - Section: Agent
  - Type: overlay
  - Action: Click to close the subagent drawer.

- **Background subagent drawer** (`#acp-drawer`):
  - Section: Agent
  - Type: drawer
  - Notes: Uses the Agent conversation-pane pattern with a run sidebar and a live transcript pane.

- **Close background run drawer** (`#acp-drawer-close`):
  - Section: Agent
  - Type: button
  - Action: Closes the subagent drawer.

- **Background drawer title** (`#acp-drawer-title`):
  - Section: Agent
  - Type: text

- **Background drawer subtitle** (`#acp-drawer-subtitle`):
  - Section: Agent
  - Type: text

- **Background drawer body** (`#acp-drawer-body`):
  - Section: Agent
  - Type: container

- **Background run peek overlay** (`#acp-popup-overlay`):
  - Section: Agent
  - Type: overlay
  - Action: Click outside the dialog to close the peek popup.

- **Background run peek popup** (`#acp-popup`):
  - Section: Agent
  - Type: dialog

- **Close background run peek** (`#acp-popup-close`):
  - Section: Agent
  - Type: button
  - Action: Closes the peek popup.

- **Background peek title** (`#acp-popup-title`):
  - Section: Agent
  - Type: text

- **Background peek body** (`#acp-popup-body`):
  - Section: Agent
  - Type: container

## Task checklist

A compact strip showing the agent's todo list for the current conversation. Items come from the `todo` tool; the user can delete items from the UI.

- **Task checklist strip** (`#agent-todo-strip`):
  - Section: Agent
  - Type: container

- **Task checklist toggle** (`#agent-todo-strip-toggle`):
  - Section: Agent
  - Type: button
  - Action: Collapse/expand the task list.
  - Notes: Default collapsed.

- **Task count** (`#agent-todo-strip-count`):
  - Section: Agent
  - Type: text

- **Task progress meta** (`#agent-todo-strip-meta`):
  - Section: Agent
  - Type: text
  - Notes: Shows open count or 'All done'.

- **Task list** (`#agent-todo-strip-list`):
  - Section: Agent
  - Type: list
  - Notes: Items come from the `todo` tool; user-deleted items disappear.

## Steer queue

When a user sends a steer message mid-turn, it is queued here and applied at the next safe tool-round boundary. Once applied, the user bubble anchors the next assistant round: stale waiting dots from the completed round are removed and exactly one new waiting round appears after the user bubble.

- **Steer queue** (`#agent-steer-queue`):
  - Section: Agent
  - Type: container
  - Notes: Queued steer messages waiting for a safe boundary.

- **Steer queue toggle** (`#agent-steer-queue-toggle`):
  - Section: Agent
  - Type: button
  - Action: Collapse/expand the steer queue.
  - Notes: Default expanded.

- **Steer queue title** (`#agent-steer-queue-title`):
  - Section: Agent
  - Type: text

- **Steer queue state** (`#agent-steer-queue-state`):
  - Section: Agent
  - Type: text

- **Steer queue text** (`#agent-steer-queue-text`):
  - Section: Agent
  - Type: text

- **Cancel queued steer** (`#agent-steer-cancel`):
  - Section: Agent
  - Type: button
  - Action: Cancels the pending steer message.

## Composer

The message input with attachments, model picker, workspace selector, provider status, stop, and send buttons. Provider status shows server-authoritative used/window context tokens and remains visible as a compact status on narrow screens. Ctrl+Enter (⌘↩ on Mac) sends. While a turn is running, Send becomes Steer. The whole conversation area accepts drag & drop of files and folders — folders are attached as path-only references (desktop only).

- **Composer form** (`#agent-form`):
  - Section: Agent
  - Type: form

- **Message input** (`#composer-input`):
  - Section: Agent
  - Type: textarea
  - Shortcut: Ctrl+Enter / ⌘↩ to send; Escape closes dialogs

- **Attachments preview** (`#agent-attachments`):
  - Section: Agent
  - Type: container

- **File input** (`#agent-file-input`):
  - Section: Agent
  - Type: input
  - Notes: Hidden file picker for images, PDFs, and text files.

- **Attach files** (`#agent-attach-btn`):
  - Section: Agent
  - Type: button
  - Action: Opens the file picker.

- **Model picker trigger** (`#model-trigger`):
  - Section: Agent
  - Type: button
  - Action: Opens the model menu.

- **Model label** (`#model-trigger-label`):
  - Section: Agent
  - Type: text

- **Model menu** (`#model-menu`):
  - Section: Agent
  - Type: dialog
  - Notes: Lists models grouped by provider with effort chips.

- **Provider route trigger** (`#route-trigger`):
  - Section: Agent
  - Type: button
  - Action: Toggles the upstream provider route menu (router icon = multi-provider, home icon = single upstream).

- **Provider route menu** (`#route-menu`):
  - Section: Agent
  - Type: dialog
  - Notes: Lists searchable upstream providers for the selected model with quantization, latency, throughput, and per-provider input/output pricing badges; Auto restores gateway load balancing and has no single fixed price.

- **Workspace selector** (`#agent-workspace-btn`):
  - Section: Agent
  - Type: button
  - Action: Opens the host folder dialog.

- **Workspace label** (`#agent-workspace-label`):
  - Section: Agent
  - Type: text

- **Provider status / context usage** (`#agent-provider-status`):
  - Section: Agent
  - Type: text
  - Notes: Shows the backend context usage as used/window (e.g. 32k/1M), or 'Context automatically compacting' while the active room is compacting. The number is the server source of truth: the live server-side estimate while a turn streams and the provider-measured context fill after it completes. The frontend never sums message bubbles.

- **Stop** (`#stop-btn`):
  - Section: Agent
  - Type: button
  - Action: Cancels the running turn; partial output is kept and marked interrupted.

- **Send** (`#send-btn`):
  - Section: Agent
  - Type: submit
  - Shortcut: Ctrl+Enter / ⌘↩
