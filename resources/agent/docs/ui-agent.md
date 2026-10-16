# Agent

The agent conversation view: a scrollable event stream of user and assistant rounds, thinking traces, tool calls, tasks, and artifacts; a composer with model, effort, route, workspace, and attachment controls; a left room drawer listing conversations; and a right context dock with Tasks, Subagents, and Context panels.

**How to open:** Click the Agent item in the left rail, or open #agent.

## Room drawer (conversations)

The left drawer lists every conversation room with search and a New chat action. On narrow widths it becomes an off-canvas panel toggled from the top bar; each row offers inline rename via the pencil affordance and a context meter dot. Deleted rooms disappear from the list and clear their per-room state.

- **Room drawer** (`#room-drawer`):
  - Type: container
  - Notes: Left drawer listing conversation rooms; slides in as an overlay on narrow widths.

- **Conversations drawer toggle** (`#room-drawer-toggle`):
  - Section: Top bar
  - Type: button
  - Action: Toggles the room drawer (conversation list) open or closed.
  - Related: Room drawer (`#room-drawer`), New chat (`#new-chat`)
  - Notes: Agent-only control: always present in the top-bar DOM but hidden on every non-Agent route (`.shell:not(.agent-active)` display:nones it); leaving Agent also closes any open overlay panel so the shared backdrop cannot strand over a panel-less page.

- **Conversation search** (`#room-search-input`):
  - Type: search
  - Shortcut: Ctrl+K / ⌘K, or / when not typing
  - Notes: Filters room titles in real time.

- **Conversation list** (`#room-list`):
  - Type: list
  - Notes: Interactive Agent rooms only; each row exposes inline rename and delete affordances.

- **New chat** (`#new-chat`):
  - Type: button
  - Action: Starts a temporary local draft and focuses the composer; the durable conversation is created when the first user message is sent.
  - Shortcut: Ctrl+N / ⌘N on the Agent view

## Thread

The event stream renders the active conversation: user/assistant bubbles, Thinking disclosures, tool cards, task updates, and artifacts. Filter buttons narrow the stream to messages or activity; the Latest jump returns to the bottom. Long conversations are windowed: older rounds and archived pre-compaction chunks load on demand via the Load-older sentinel, keeping the scroll anchored. A running turn keeps every persisted round mounted on reload; Live Thinking deltas follow the thread only while the end marker is visible.

Compared to the old shell, the empty-state starter prompt chips are not rendered. ACP run details live inside the context dock Subagents panel (opened from the stream's subagent tool card), and the single-run transcript reader popup (acp-popup-overlay) is retained as a separate surface for delegation cards rendered inside transcripts — e.g. the Learning job transcript — where the dock is not the destination.

Thinking code blocks and tool Markdown details show language labels and code-editor controls. Relative file links in Thinking open the type-aware preview against the active workspace. Image, audio, and video tool previews are lazy; if loading fails, Retry remounts the preview and Preview file opens the shared file viewer. Text-file preview failures show a status-specific message with Retry and Close.

Question choices appear as full-width list rows with separators so long answer labels remain readable. File-preview failures are centered directly on the existing blue preview surface with Retry; the nested error card and its duplicate Close button are removed.

- **Agent view surface** (`#conversation-surface`):
  - Type: container
  - Notes: Routed section carrying data-view="agent"; replaces the old agent-shell/agent-conversation containers.

- **Event stream** (`#event-stream`):
  - Type: log
  - Notes: Scrollable conversation log (aria-busy while a turn runs); windowed history plus archived chunks behind the load-older sentinel.

- **Thread log** (`#agent-thread`):
  - Type: log
  - Notes: Runtime wrapper around event-stream created for the mini-window (document PiP) path; display:contents so layout is unchanged. The stream renders user/assistant bubbles, Thinking disclosures, tool cards, tasks, and artifacts with preserved disclosure state across refreshes.

- **Stream activity** (`#stream-activity`):
  - Type: status
  - Notes: Animated status line under the stream (working, auto-continue, compaction) with aria-live announcements.

- **Jump to latest** (`#jump-latest`):
  - Type: button
  - Action: Scrolls the stream to the newest events.

- **Stream filters** (`[data-filter]`):
  - Type: button group
  - Action: Narrows the stream to all events, messages only, or activity only.

- **Load older messages** (`.load-older-events`):
  - Type: button
  - Action: Reveals older in-memory events first, then archived pre-compaction chunks (agent.conversations.chunk), keeping the scroll position anchored.

## Tool runs strip

A collapsible strip above the composer lists in-flight tool runs with per-call elapsed time and a stop button on stoppable tools (exec). It hides once every job settles; orphaned running jobs are marked interrupted on turn done, error, or room switch.

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

- **Tool job list** (`#tool-job-list`):
  - Type: list
  - Notes: Rows of in-flight tool calls with elapsed time.

- **Stop tool run** (`.agent-tool-job-stop`):
  - Type: button
  - Action: Stops a stoppable tool call (exec) via agent.tools.stop.
  - Related: tool-terminal-stop (`#tool-terminal-stop`)

## Steer queue

A queued steering message waits for a safe boundary before joining the active turn. One compact row stacked inside the composer frame shows the pending text, promotes it into the transcript once applied, restores it to the composer when the backend rejects it, and cancels on demand.

- **Steer queue** (`#agent-steer-queue`):
  - Type: container
  - Notes: One compact queued-steer row stacked inside the composer frame (icon, queued text, cancel), waiting for a safe boundary; hidden while the queue is empty.

- **agent-steer-queue-list** (`#agent-steer-queue-list`):

- **Steer queue text** (`#agent-steer-queue-text`):
  - Type: text

- **Cancel queued steer** (`#agent-steer-cancel`):
  - Type: button
  - Action: Cancels the pending steer message.

## Context dock

The right dock groups three panels behind tabs. Tasks shows the plan checklist with per-item delete; Subagents lists background ACP and delegate runs with an inline transcript; Context shows the live context-usage meter for the room. The dock collapses into an overlay on narrow widths.

Parity note: the old acp-dock strip and acp-drawer live view are folded into this panel — subagent runs and their transcripts open inline here. The acp-popup peek overlay is not folded in: it survives as the single-run transcript reader (see 'Subagent transcript reader' below), opened by delegation cards rendered inside transcripts such as the Learning job view.

- **Context dock** (`#context-dock`):
  - Type: container
  - Notes: Right dock grouping Tasks, Subagents, and Context panels behind tabs; overlay panel on narrow widths.

- **Dock toggle** (`#context-dock-toggle`):
  - Section: Top bar
  - Type: button
  - Action: Toggles the conversation context dock open or closed.
  - Related: Context dock (`#context-dock`)
  - Notes: Agent-only control: always present in the top-bar DOM but hidden on every non-Agent route (`.shell:not(.agent-active)` display:nones it); leaving Agent also closes any open overlay panel so the shared backdrop cannot strand over a panel-less page.

- **Tasks tab** (`#tab-tasks`):
  - Type: tab
  - Action: Opens the task plan checklist panel.

- **Tasks panel** (`#panel-tasks`):
  - Type: panel
  - Notes: Task plan checklist for the active conversation.

- **Delete task** (`dock-task-delete`):
  - Type: button
  - Action: Deletes a todo item via agent.todos.delete.

- **Subagents tab** (`#tab-subagents`):
  - Type: tab
  - Action: Opens the background subagent runs panel.

- **Subagents panel** (`#panel-subagents`):
  - Type: panel
  - Notes: Background ACP and delegate runs for the room; hidden until a run exists. Replaces the old acp-dock strip.

- **Subagent picker** (`.subagent-trigger`):
  - Type: button
  - Action: Opens the subagent run list inside the dock panel.

- **Subagent run list** (`.subagent-options`):
  - Type: listbox
  - Action: Lists parallel subagent runs with status dots.

- **Subagent detail** (`.subagent-detail`):
  - Type: container
  - Notes: Inline detail for the selected run: status, agent, elapsed time, objective, and transcript. Replaces the old acp-drawer live view; the single-run acp-popup reader remains a separate surface opened from delegation cards.

- **Context tab** (`#tab-context`):
  - Type: tab
  - Action: Opens the context usage panel.

- **Context panel** (`#panel-context`):
  - Type: panel
  - Notes: Context-usage detail for the active room, synced with the top-bar context meter.

## Subagent transcript reader

A modal dialog (role=dialog, aria-modal) that focuses a single subagent run: the header shows the agent name and the body renders that run's full transcript, hydrated on demand through acp.runs.get for historical runs the live cache no longer holds. It is the surviving single-run surface — the room-bound acp-drawer was retired when the dock panel took over.

Open: click a delegation/subagent card rendered inside a transcript (the Learning job transcript is the current surface; the same shared renderer drives any host that shows delegation cards). In the Agent view itself, prefer the dock Subagents panel — the stream's subagent tool card opens the dock, not this dialog.

Close: the × button, Escape, or clicking the dimmed overlay outside the dialog. Focus returns to the element that opened it.

- **Subagent reader overlay** (`#acp-popup-overlay`):
  - Type: overlay
  - Action: Click outside the dialog to close the transcript reader.

- **Subagent transcript reader** (`#acp-popup`):
  - Type: dialog
  - Notes: Modal single-run reader (role=dialog, aria-modal). Opened by delegation cards rendered inside transcripts (e.g. the Learning job transcript); Escape, the close button, or an overlay click dismisses it.

- **Close subagent reader** (`#acp-popup-close`):
  - Type: button
  - Action: Closes the transcript reader and returns focus to the opener.

- **Subagent reader title** (`#acp-popup-title`):
  - Type: text
  - Notes: Shows the selected run's agent name.

- **Subagent reader body** (`#acp-popup-body`):
  - Type: container
  - Notes: Hosts the single run transcript; historical runs are hydrated on demand through acp.runs.get.

## Composer

The composer wraps the textarea with an attachment tray, the model/effort/route configuration popover, a workspace folder browser, and send/stop actions. The model popover groups model, effort, provider-route, and workspace selection; the route picker persists per room via agent.conversations.set-provider.

Model options show consistent Lucide SVG icons for Text plus Vision input, Audio input, and Video input only when the catalog advertises those capabilities. Searchable model labels and route values stay unchanged. With no selected workspace, the folder picker reads Home and Host home; a selected folder always shows its actual basename.

Model options show the display name above the muted original model ID. SVG modality icons remain vertically centered beside the two lines.

The model picker uses the enabled provider catalog. When no chat models are available it shows an empty disabled picker. File drops attach file content; in Electron, folder drops attach a folder path through the desktop bridge. In the browser, choose the workspace when a folder path is unavailable.

Returning to Agent reloads the enabled model catalog so newly imported models appear without reloading the window; valid model and effort selections are retained.

- **Composer form** (`#message-form`):
  - Type: form

- **Message input** (`#message-input`):
  - Type: textarea
  - Shortcut: Ctrl+Enter / ⌘↩ to send; Escape closes dialogs

- **Attach files** (`#attachment-button`):
  - Type: button
  - Action: Opens the file picker.

- **File input** (`#attachment-input`):
  - Type: input
  - Notes: Hidden file picker for images, PDFs, and text files.

- **Model configuration** (`#model-config-trigger`):
  - Type: button
  - Action: Opens the composer configuration popover (model, effort, route, workspace).
  - Related: Model configuration popover (`#model-config-popover`), Model picker (`#model-select`)

- **Model summary** (`#model-config-summary`):
  - Type: text
  - Notes: Current model label inside the config trigger.

- **Effort summary** (`#effort-config-summary`):
  - Type: text
  - Notes: Current effort label inside the config trigger.

- **Model configuration popover** (`#model-config-popover`):
  - Type: dialog
  - Notes: Groups model, effort, provider-route, and workspace selection.

- **Model picker** (`#model-select`):
  - Type: select
  - Action: Chooses the active model for the next turn.
  - Notes: Slim Select instance populated from the provider catalog.

- **Effort picker** (`#effort-select`):
  - Type: select
  - Action: Chooses the reasoning effort for the next turn.

- **Provider route picker** (`#route-select`):
  - Type: button
  - Action: Selects the per-conversation provider route (Auto, upstream provider, or Codex account); persisted via agent.conversations.set-provider.

- **Workspace selector** (`#workspace-browser-trigger`):
  - Type: button
  - Action: Opens the in-app workspace folder browser (server-side directories via agent.workspace.list-dirs).

- **Workspace browser** (`#workspace-browser-view`):
  - Type: dialog
  - Notes: In-app folder browser listing server-side directories via agent.workspace.list-dirs.

- **Back to model config** (`#workspace-browser-back`):
  - Type: button
  - Action: Returns from the workspace browser to the model configuration view.

- **Parent folder** (`#workspace-up`):
  - Type: button
  - Action: Navigates the workspace browser to the parent directory.

- **Workspace breadcrumbs** (`#workspace-breadcrumbs`):
  - Type: navigation
  - Notes: Clickable path segments for the browsed folder.

- **Workspace search** (`#workspace-search`):
  - Type: search
  - Action: Filters folders in the current directory.

- **Workspace folder list** (`#workspace-folder-list`):
  - Type: list

- **Workspace empty state** (`#workspace-empty`):
  - Type: status

- **Current folder path** (`#workspace-current-path`):
  - Type: text

- **Use this folder** (`#workspace-use-folder`):
  - Type: button
  - Action: Confirms the browsed folder as the conversation workspace.

- **Ask popover** (`#ask-popover`):
  - Type: dialog
  - Notes: Inline confirmation popover for agent ask-style tool requests (aria-live=assertive).

- **Stop turn** (`#stop-turn-button`):
  - Type: button
  - Action: Cancels the running turn; partial output is kept and marked interrupted.

- **Send** (`#send-btn`):
  - Type: submit
  - Shortcut: Ctrl+Enter / ⌘↩

## Mini window

The mini-window button beside the stream opens the conversation in a picture-in-picture window (document PiP when supported, else a ?mini=1 popup). Room diagnostics — ID, tool calls, message count — are owned by the Context dock tab. The button is desktop-only chrome: it hides at widths through 720px where there is no floating window surface. Electron hides this browser-only control.

- **agent-room-info** (`#agent-room-info`):

- **Mini window** (`#mini-window-btn`):
  - Type: button
  - Notes: Opens the picture-in-picture mini window (Document PiP on Chromium, popup fallback elsewhere) showing the live agent thread. Appears in the stream toolbar for an active browser conversation; hidden in Electron until its mini-window flow is supported. Hidden at widths through 720px — there is no floating picture-in-picture surface on the phone layout.
