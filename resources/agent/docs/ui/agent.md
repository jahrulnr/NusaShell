# Agent

The agent conversation workspace. Chat with the NusaShell agent, attach files, pick models, and manage conversation threads.

**How to open:** Click the Agent item in the left sidebar.

## Conversation list

A fixed workbench rail listing local conversation threads. Click a thread to open it; use the × button to delete. The rail remains visible beside a wider conversation surface at desktop widths.

- **Conversations heading** (`#agent-conversations-title`):
  - Section: Conversation list
  - Type: heading
  - Action: Labels the left conversation panel.

- **Conversation count** (`#agent-conversation-count`):
  - Section: Conversation list
  - Type: status text
  - Action: Displays the number of conversation threads.

- **New conversation** (`#agent-new-conversation`):
  - Section: Conversation list
  - Type: icon button
  - Action: Creates a new empty conversation thread and focuses the composer.
  - Related: Message input (`#agent-input`)

- **Search conversations** (`#agent-conversation-search`):
  - Section: Conversation list
  - Type: search input
  - Action: Filters the conversation list by thread title as you type.

- **Conversation list** (`#agent-conversation-list`):
  - Section: Conversation list
  - Type: list
  - Action: Container for conversation rows. Shows the active thread highlighted.

- **Conversation row** (`.agent-conversation-item`):
  - Section: Conversation list
  - Type: listitem
  - Action: Click the row to open the conversation; click the × button to delete it.

## Conversation thread

The full-height message runway separates compact user cards from editorial assistant responses. User attachments render as image previews or file cards. Provider reasoning is persisted and appears before the answer in a collapsed Thinking disclosure with sanitized Markdown; models that return no reasoning show no placeholder. Completed tool calls render as expandable terminal cards with the tool name in the header and full args/output in the body. Interactive ask_question calls render as Ask Question cards with selectable options and an optional free-text answer; after the user replies, the sealed card shows the chosen answer. Message footers expose timestamps, model/trace metadata, copy, and retry where applicable. Only this area scrolls.

- **ACP status bar (legacy)** (`#acp-status-bar`):
  - Section: Conversation thread
  - Type: status bar
  - Action: Shows the ACP provider name and running/idle chip for legacy peer-chat ACP conversations. Only visible on conversations with kind='acp'.
  - Notes: Legacy. New ACP interactions use the subagent side pane, not peer-chat threads.

- **ACP status provider** (`#acp-status-provider`):
  - Section: Conversation thread
  - Type: label
  - Action: Shows the provider id for the current ACP peer-chat conversation.

- **ACP status chip** (`#acp-status-chip`):
  - Section: Conversation thread
  - Type: status chip
  - Action: Shows running/idle state for the current ACP peer-chat conversation.

- **Conversation thread** (`#agent-thread`):
  - Section: Conversation thread
  - Type: log region
  - Action: Main scrollable message area for the current conversation. Auto-scrolls as new content arrives.

- **Empty thread state** (`#agent-empty`):
  - Section: Conversation thread
  - Type: placeholder
  - Action: Shown when no conversation is selected or when a new conversation has no messages yet. Prompts to choose a model.

- **Message row** (`.agent-message`):
  - Section: Conversation thread
  - Type: article
  - Action: Displays a right-aligned user card or a full-width editorial assistant response with a compact metadata and action footer.

- **Message bubble** (`.agent-bubble`):
  - Section: Conversation thread
  - Type: div
  - Action: Displays plain text inside a compact user card; completed assistant messages render sanitized GitHub-Flavored Markdown directly on the conversation runway.

- **Sent attachment preview** (`.agent-message-attachments`):
  - Section: Conversation thread
  - Type: attachment gallery
  - Action: Shows persisted image attachments as bounded thumbnails and PDF or text attachments as compact file cards above the user message.

- **Thinking** (`.agent-reasoning`):
  - Section: Conversation thread
  - Type: disclosure
  - Action: Appears when the provider returns reasoning or a thinking summary. Expand it to inspect sanitized Markdown; it is collapsed by default and remains available after reopening the conversation.

- **Tool terminal** (`.agent-tool-terminal`):
  - Section: Conversation thread
  - Type: disclosure
  - Action: Timeline-style expandable tool call. Header keeps the ›_ rail badge plus tool name and status. Expand to inspect nested tool and Output panels with args and truncated result text.

- **Ask Question card** (`.agent-ask-card`):
  - Section: Conversation thread
  - Type: interactive card
  - Action: Rendered when the agent calls ask_question. Shows the question, selectable option rows, optional free-text answer, and Send answer. After the user replies or on reload, the sealed card shows the chosen answer.

- **Ask option** (`.agent-ask-option`):
  - Section: Conversation thread
  - Type: button
  - Action: Selects one option (or toggles multi-select options) on an Ask Question card before sending the answer.

- **Send ask answer** (`.agent-ask-send`):
  - Section: Conversation thread
  - Type: button
  - Action: Submits the selected option(s) or typed free-text answer for the pending ask_question tool call.

- **Copy message** (`.agent-message-action`):
  - Section: Conversation thread
  - Type: icon button
  - Action: Copies the message text through the Electron clipboard bridge and briefly confirms success.

- **Retry turn** (`.agent-retry-btn`):
  - Section: Conversation thread
  - Type: button
  - Action: Appears on a failed assistant turn. Click to re-run the same turn.

## Subagent activity

When the parent agent delegates work through the subagent tool, an in-chat Subagent run card appears in the message thread. While the subagent run is active, the card shows a compact mini activity stream (roughly ten monospace rows) that mirrors the full Subagent side pane: live reasoning, text snippets, tool calls with success/failure marks, and plan progress. The mini stream auto-scrolls to the bottom while the user is pinned to the tail; scrolling up pauses stickiness until the user returns to the bottom. Clicking the card head opens the full Subagent side pane, which preserves the complete live log with markdown rendering, expandable tool terminals, plan steps, and permission/ask cards. Clicks inside the mini stream do not toggle the drawer. When the run ends, the mini stream freezes in place until the parent turn seals the tool card; the side pane keeps the full frozen history for review.

- **Subagent side pane** (`#agent-subpane`):
  - Section: Agent
  - Type: drawer
  - Action: Full-height drawer that renders the complete live subagent stream with markdown, tool terminals, plans, and permission/ask cards.

- **Subagent pane overlay** (`#agent-subpane-overlay`):
  - Section: Agent
  - Type: overlay
  - Action: Dimmed backdrop behind the Subagent side pane.

- **Subagent pane header** (`.agent-subpane-head`):
  - Section: Agent
  - Type: header
  - Action: Shows the provider badge, run title, live status chip, and close button.

- **Subagent pane badge** (`#agent-subpane-badge`):
  - Section: Agent
  - Type: badge
  - Action: Short uppercase provider id for the active subagent run.

- **Subagent pane title** (`#agent-subpane-title`):
  - Section: Agent
  - Type: text
  - Action: Title of the active subagent run.

- **Subagent pane status** (`#agent-subpane-status`):
  - Section: Agent
  - Type: status
  - Action: Live status chip (RUNNING/OK/FAIL) for the active subagent run.

- **Subagent pane close** (`#agent-subpane-close`):
  - Section: Agent
  - Type: button
  - Action: Closes the Subagent side pane drawer.

- **Subagent pane body** (`#agent-subpane-body`):
  - Section: Agent
  - Type: scroll container
  - Action: Scrollable log that renders the full subagent stream: reasoning disclosures, text bubbles, tool terminals, plan steps, and error blocks.

- **Subagent run card** (`.agent-subagent-card`):
  - Section: Agent
  - Type: card
  - Action: In-chat card representing a subagent run. The head opens the full Subagent side pane; while running, a mini activity stream is shown below the head.

- **Subagent card head** (`.agent-subagent-card-head`):
  - Section: Agent
  - Type: button
  - Action: Clickable header that opens the full Subagent side pane for the run.

- **Subagent card badge** (`.agent-subagent-card-badge`):
  - Section: Agent
  - Type: badge
  - Action: Short uppercase provider id on the in-chat subagent card.

- **Subagent card title** (`.agent-subagent-card-title`):
  - Section: Agent
  - Type: text
  - Action: Title of the subagent run shown on the in-chat card.

- **Subagent card status** (`.agent-subagent-card-status`):
  - Section: Agent
  - Type: status
  - Action: Live status chip on the in-chat subagent card (RUNNING/OK/FAIL).

- **Subagent mini activity stream** (`.agent-subagent-card-stream`):
  - Section: Agent
  - Type: scroll container
  - Action: Compact scrollable mini log inside the running subagent card. Mirrors the full side pane as one-line rows (Thinking, text, tool calls, plan). Auto-scrolls while pinned to the bottom; scrolling up pauses stickiness.

- **Subagent mini stream row** (`.agent-subagent-card-stream-row`):
  - Section: Agent
  - Type: row
  - Action: Single compact activity row in the mini stream (reasoning, text, tool, or plan).

- **Subagent card summary** (`.agent-subagent-card-summary`):
  - Section: Agent
  - Type: text
  - Action: Markdown summary shown on the sealed subagent card after the run ends.

- **Subagent card error** (`.agent-subagent-card-error`):
  - Section: Agent
  - Type: text
  - Action: Error message shown on the subagent card when the run fails.

## Composer

A compact command dock at the bottom of the thread. Its message input starts at one row, grows with wrapped or explicit lines, and caps at ten rows before scrolling internally. Attachment, model, and workspace context stay grouped separately from context usage and turn actions; long labels truncate, and actions wrap below only at very narrow widths.

- **Composer form** (`#agent-form`):
  - Section: Composer
  - Type: form
  - Action: Wraps the message textarea, attachments, and send controls. Submit triggers the agent turn.

- **Message input** (`#agent-input`):
  - Section: Composer
  - Type: textarea
  - Action: Primary text area for typing a prompt. Starts at one row, grows automatically with content, and scrolls internally after reaching ten rows.
  - Shortcut: Ctrl+Enter or Cmd+Enter submits the turn.

- **Attachment chips** (`#agent-attachments`):
  - Section: Composer
  - Type: list
  - Action: Shows files attached to the current turn. Each chip can be removed.

- **File attachment input** (`#agent-file-input`):
  - Section: Composer
  - Type: file input
  - Action: Hidden file picker triggered by the attach button. It inspects file bytes, accepting supported images and PDFs plus valid UTF-8 text such as source code, markup, and configuration; filenames and claimed MIME types are not trusted.

- **Attach files** (`#agent-attach-btn`):
  - Section: Composer
  - Type: icon button
  - Action: Opens the file picker to attach images, PDFs, or UTF-8 text files to the current turn. Up to 4 files, each under 4 MiB. Text attachments are included as text context for every chat model. Unless image input is disabled in Agent runtime settings, images are sent optimistically; a provider 4xx response retries the same turn once without image parts.
  - Related: File attachment input (`#agent-file-input`), Attachment chips (`#agent-attachments`)

- **Model and effort trigger** (`#agent-model-trigger`):
  - Section: Composer
  - Type: button
  - Action: Opens the model/effort picker dropdown. Label shows the selected model and effort, e.g. 'Model · auto'.

- **Model trigger label** (`#agent-model-trigger-label`):
  - Section: Composer
  - Type: text
  - Action: Text inside the model trigger showing the selected model id and effort value.

- **`#agent-acp-pill`** (missing map entry)

- **`#agent-acp-pill-label`** (missing map entry)

- **Model picker dropdown** (`#agent-model-menu`):
  - Section: Composer
  - Type: dialog
  - Action: Searchable list of imported models. Capability badges distinguish vision, no vision, and vision unknown using explicit provider metadata only; they do not block image attachments. Choose a model and, if supported, a reasoning effort level.

- **Search models** (`#agent-model-search`):
  - Section: Composer
  - Type: search input
  - Action: Filters the model picker list by model id, label, provider, or capability.

- **Model list** (`#agent-model-list`):
  - Section: Composer
  - Type: listbox
  - Action: Container for model rows in the picker.

- **Context usage status** (`#agent-provider-status`):
  - Section: Composer
  - Type: status text
  - Action: Shows approximate used / total context window fill for the selected model (from agent.context estimates and local chars/4 estimates — not cumulative billed input tokens), or 'Choose a model' when none is selected.

- **Stop turn** (`#agent-stop-btn`):
  - Section: Composer
  - Type: icon button
  - Action: Cancels the currently running agent turn. Visible only while a turn is in progress.

- **Send turn** (`#agent-send-btn`):
  - Section: Composer
  - Type: submit button
  - Action: Submits the composer form and starts the agent turn. Shortcut is shown as a tooltip (Ctrl+Enter / ⌘↩).
  - Shortcut: Ctrl+Enter or Cmd+Enter submits the turn.

## Task strip

A collapsible checklist above the composer that mirrors the agent-owned todo list. The agent writes it via the `todo` meta-tool; the user can delete individual items (which removes them from the runtime port so they do not reappear in the next prompt injection) and collapse the strip. It hides automatically when the list is empty.

- **Task strip** (`.agent-todo-strip`):
  - Section: Task strip
  - Type: container
  - Action: Collapsible checklist above the composer mirroring the agent-owned todo list. Hides when the list is empty.

- **Collapse tasks** (`.agent-todo-strip-toggle`):
  - Section: Task strip
  - Type: button
  - Action: Collapses or expands the task list.

- **Task count** (`.agent-todo-strip-count`):
  - Section: Task strip
  - Type: text
  - Action: Shows incomplete/total task counts.

- **Task list** (`.agent-todo-strip-list`):
  - Section: Task strip
  - Type: list
  - Action: Renders each task with a status glyph and a delete button. Clicking the delete button removes the item via agent.todos_delete.

## Background jobs strip

A strip above the composer (below the task strip) that shows running and recently finished async tool jobs started via `async_run`. Each card shows the tool name, status badge, live tail output, and a Stop button that calls `agent.tool_job_kill`. The strip rehydrates from `agent.tool_job_list` on conversation open and hides when no jobs are active.

- **Background jobs strip** (`.agent-tool-job-strip`):
  - Section: Background jobs strip
  - Type: container
  - Action: Shows running and recently finished async tool jobs. Hides when no jobs are active.

- **Job cards** (`.agent-tool-job-list`):
  - Section: Background jobs strip
  - Type: list
  - Action: Renders each job as a card with tool name, status badge, live tail, and a Stop button. Clicking Stop calls agent.tool_job_kill.

## Delete conversation dialog

Confirmation dialog shown before permanently removing a conversation thread.

- **Delete overlay** (`#agent-delete-overlay`):
  - Section: Delete conversation dialog
  - Type: overlay
  - Action: Clicking outside the dialog closes it without deleting.

- **Delete conversation confirmation** (`#agent-delete-dialog`):
  - Section: Delete conversation dialog
  - Type: dialog
  - Action: Confirms permanent removal of the selected conversation thread.

- **Dialog title** (`#agent-delete-title`):
  - Section: Delete conversation dialog
  - Type: heading
  - Action: Shows 'Delete conversation?'.

- **Dialog description** (`#agent-delete-copy`):
  - Section: Delete conversation dialog
  - Type: text
  - Action: Explains that the local conversation will be permanently removed.

- **Close dialog** (`#agent-delete-close`):
  - Section: Delete conversation dialog
  - Type: icon button
  - Action: Closes the delete conversation dialog.

- **Cancel** (`#agent-delete-cancel`):
  - Section: Delete conversation dialog
  - Type: button
  - Action: Closes the dialog without deleting.

- **Delete** (`#agent-delete-confirm`):
  - Section: Delete conversation dialog
  - Type: button
  - Action: Permanently deletes the selected conversation thread.

## Agent Canvas

A shell-owned canvas drawer over the agent workbench. Completed assistant messages auto-render html, svg, and mermaid fences inline (mermaid is lazy-loaded, strict mode; html uses a sandboxed iframe via renderArtifact, sandbox=allow-scripts, no allow-same-origin, CSP with an empty external allowlist in v1); the raw fence is hidden after a successful render. Show source hides the inline preview and reveals a scrollable source block capped at about 10 rows; Hide source restores the preview. Sidebar opens the overlay drawer. html fences also show a compact artifact card with a type badge, title, size hint, Sidebar, and Show source. The drawer slides in from the right with a dimmed overlay (plugin-drawer style); it shows one active artifact at a time with a type badge, Refresh, Download source, and Close (overlay click or Escape also closes). Artifacts persist per conversation and restore on reopen; a Settings toggle disables the canvas entirely.

- **Agent shell grid** (`#agent-shell`):
  - Section: Agent Canvas
  - Type: region
  - Action: Two-column agent workbench (conversations · thread). Canvas opens as a fixed overlay drawer and does not add a third grid column.

- **Canvas drawer overlay** (`#agent-canvas-overlay`):
  - Section: Agent Canvas
  - Type: overlay
  - Action: Dimmed backdrop behind the canvas drawer; click closes the drawer.

- **Canvas overlay drawer** (`#agent-canvas`):
  - Section: Agent Canvas
  - Type: dialog
  - Action: Right-edge overlay drawer for the active canvas artifact. Slides over the agent workbench without resizing the chat column; closed via Close, overlay click, or Escape.

- **Resize canvas drawer** (`#agent-canvas-resize`):
  - Section: Agent Canvas
  - Type: separator
  - Action: Drag the left edge to resize the drawer. ArrowLeft/ArrowRight adjust it by keyboard; Home and End go to the widest and narrowest supported sizes.

- **Canvas title** (`#agent-canvas-title`):
  - Section: Agent Canvas
  - Type: status text
  - Action: Shows the active artifact title.

- **Canvas type badge** (`#agent-canvas-badge`):
  - Section: Agent Canvas
  - Type: badge
  - Action: Labels the artifact kind (HTML, SVG, or MERMAID).

- **Canvas body** (`#agent-canvas-body`):
  - Section: Agent Canvas
  - Type: region
  - Action: Hosts the rendered artifact: a sandboxed iframe for HTML, or a static SVG container for SVG/Mermaid.

- **Canvas hint** (`#agent-canvas-hint`):
  - Section: Agent Canvas
  - Type: status text
  - Action: Shows a fallback note when an artifact fails to render.

- **Refresh canvas** (`#agent-canvas-refresh`):
  - Section: Agent Canvas
  - Type: icon button
  - Action: Re-runs the render pipeline for the active artifact.

- **Download source** (`#agent-canvas-download`):
  - Section: Agent Canvas
  - Type: icon button
  - Action: Downloads the active artifact source as a file.

- **Close canvas** (`#agent-canvas-close`):
  - Section: Agent Canvas
  - Type: icon button
  - Action: Hides the pane and clears the active artifact for the conversation.

- **Canvas fence actions** (`.agent-canvas-fence-actions`):
  - Section: Agent Canvas
  - Type: group
  - Action: Action row appended to a canvas-tagged code fence.

- **Canvas fence action button** (`.agent-canvas-fence-btn`):
  - Section: Agent Canvas
  - Type: button
  - Action: Sidebar or Show source action attached to a canvas fence. Show source replaces the inline preview with a scrollable ~10-row source block.

- **Artifact card** (`.agent-canvas-card`):
  - Section: Agent Canvas
  - Type: region
  - Action: Compact card under an auto-rendered HTML preview; shows badge, title, size hint, Sidebar, and Show source.

- **Artifact card header** (`.agent-canvas-card-head`):
  - Section: Agent Canvas
  - Type: region
  - Action: Header row of an artifact card with badge, title, and meta.

- **Artifact card badge** (`.agent-canvas-card-badge`):
  - Section: Agent Canvas
  - Type: label
  - Action: Type badge (HTML) on an artifact card.

- **Artifact card title** (`.agent-canvas-card-title`):
  - Section: Agent Canvas
  - Type: label
  - Action: Title label on an artifact card (e.g. 'html 1').

- **Artifact card meta** (`.agent-canvas-card-meta`):
  - Section: Agent Canvas
  - Type: label
  - Action: Size hint (lines · bytes) on an artifact card.

- **Artifact card actions** (`.agent-canvas-card-actions`):
  - Section: Agent Canvas
  - Type: region
  - Action: Action row on an artifact card containing Sidebar and Show source buttons.

- **Inline canvas render** (`.agent-canvas-inline`):
  - Section: Agent Canvas
  - Type: region
  - Action: Static SVG container rendered inline above an svg/mermaid fence on completed assistant messages.

- **Inline HTML preview** (`.agent-canvas-inline-preview`):
  - Section: Agent Canvas
  - Type: region
  - Action: In-chat sandboxed iframe auto-rendered for HTML canvas fences on completed assistant messages.

- **Canvas SVG host** (`.agent-canvas-svg`):
  - Section: Agent Canvas
  - Type: region
  - Action: Centered SVG host inside the canvas body for svg/mermaid artifacts.
