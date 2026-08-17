# Agent

The default view: multi-conversation chat against any configured provider model. Each conversation keeps its own history on disk and can use skills, memory, docs, plugin-provided MCP tools, and spawned ACP subagents.

**How to open:** Open NusaShell, or click the Agent item in the left sidebar.

## Conversations pane

Lists conversations with a search filter and a New conversation button. The count reflects persisted threads.

- **Conversations pane** (`#agent-conversations`):
  - Section: Agent
  - Type: container

- **Conversations label** (`#conversations-title`):
  - Section: Agent
  - Type: text

- **Thread count** (`#conversation-count`):
  - Section: Agent
  - Type: text

- **New conversation** (`#new-conversation-btn`):
  - Section: Agent
  - Type: button
  - Action: Creates a new conversation and focuses the composer.

- **Conversation search** (`#conversation-search`):
  - Section: Agent
  - Type: search
  - Notes: Filters conversation titles in real time.

- **Conversation list** (`#conversation-list`):
  - Section: Agent
  - Type: list

## Thread

Renders the active conversation. An offline mascot appears when the backend is unreachable.

Long conversations open showing only the most recent messages, scrolled to the bottom; a 'Load older messages' button at the top of the thread reveals earlier messages in batches (keeping the scroll position anchored). Live streaming auto-follows only while the view is pinned to the bottom.

- **Agent shell** (`#agent-shell`):
  - Section: Agent
  - Type: container

- **Active conversation** (`#agent-conversation`):
  - Section: Agent
  - Type: container

- **Offline mascot** (`#agent-offline-state`):
  - Section: Agent
  - Type: status
  - Notes: Shown when the backend is unreachable.

- **Thread log** (`#agent-thread`):
  - Section: Agent
  - Type: log
  - Notes: Renders the active conversation messages. Long conversations are windowed: only the most recent messages render on open (scrolled to bottom), older ones load on demand.

- **Load older messages** (`#agent-load-older`):
  - Section: Agent
  - Type: button
  - Action: Reveals the previous batch of older messages at the top of the thread, keeping the scroll position anchored. Shown only while older messages remain windowed out; hidden once the oldest message is reached.

## Tool call strip

Shows in-flight tool calls for the current turn, with one entry per MCP or built-in tool invocation.

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

## ACP subagent dock

Appears above the composer when ACP subagents are live (or recently finished in this room). Each chip opens the right-hand drawer; the peek control opens a popup for one run. Drawer and popup support steer, stop, mode changes, and risk promotion. Permission prompts use a global overlay that stays visible across views.

- **ACP subagent dock** (`#acp-dock`):
  - Section: Agent
  - Type: container
  - Notes: Hidden until at least one live or recently finished ACP run exists.

- **ACP dock toggle** (`#acp-dock-toggle`):
  - Section: Agent
  - Type: button
  - Action: Collapse/expand the subagent chip list.

- **ACP dock title** (`#acp-dock-title`):
  - Section: Agent
  - Type: text

- **ACP dock meta** (`#acp-dock-meta`):
  - Section: Agent
  - Type: text

- **Open ACP drawer** (`#acp-dock-open-drawer`):
  - Section: Agent
  - Type: button
  - Action: Opens the right-hand multi-subagent drawer.

- **ACP dock chips** (`#acp-dock-list`):
  - Section: Agent
  - Type: list

## ACP live views

The drawer lists every live spawn so you can switch between parallel subagents. The peek popup focuses one run. The permission overlay is fail-closed: timeout denies. Allow once, allow for this session, or deny.

- **ACP drawer overlay** (`#acp-drawer-overlay`):
  - Section: Agent
  - Type: overlay
  - Action: Click to close the subagent drawer.

- **ACP subagent drawer** (`#acp-drawer`):
  - Section: Agent
  - Type: drawer
  - Notes: Wider than the plugin drawer so a live transcript, steer box, and mode chips fit.

- **Close ACP drawer** (`#acp-drawer-close`):
  - Section: Agent
  - Type: button
  - Action: Closes the subagent drawer.

- **ACP drawer title** (`#acp-drawer-title`):
  - Section: Agent
  - Type: text

- **ACP drawer subtitle** (`#acp-drawer-subtitle`):
  - Section: Agent
  - Type: text

- **ACP drawer body** (`#acp-drawer-body`):
  - Section: Agent
  - Type: container

- **ACP peek overlay** (`#acp-popup-overlay`):
  - Section: Agent
  - Type: overlay
  - Action: Click outside the dialog to close the peek popup.

- **ACP peek popup** (`#acp-popup`):
  - Section: Agent
  - Type: dialog

- **Close ACP peek** (`#acp-popup-close`):
  - Section: Agent
  - Type: button
  - Action: Closes the peek popup.

- **ACP peek title** (`#acp-popup-title`):
  - Section: Agent
  - Type: text

- **ACP peek body** (`#acp-popup-body`):
  - Section: Agent
  - Type: container

- **ACP permission overlay** (`#acp-permission-overlay`):
  - Section: Agent
  - Type: overlay
  - Notes: Global. Timeout denies. Stays visible if you leave the Agent view.

- **ACP permission dialog** (`#acp-permission-dialog`):
  - Section: Agent
  - Type: dialog

- **ACP permission title** (`#acp-permission-title`):
  - Section: Agent
  - Type: text

- **ACP permission details** (`#acp-permission-body`):
  - Section: Agent
  - Type: container

- **Deny ACP permission** (`#acp-permission-deny`):
  - Section: Agent
  - Type: button
  - Action: Denies the pending tool. Fail-closed.

- **Allow ACP permission for session** (`#acp-permission-allow-session`):
  - Section: Agent
  - Type: button
  - Action: Allows this tool and auto-allows later permission prompts on the same run.

- **Allow ACP permission once** (`#acp-permission-allow`):
  - Section: Agent
  - Type: button
  - Action: Allows this tool call once.

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

When a user sends a steer message mid-turn, it is queued here and applied at the next safe tool-round boundary.

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

The message input with attachments, model picker, workspace selector, provider status, stop, and send buttons. Ctrl+Enter (⌘↩ on Mac) sends. The whole conversation area accepts drag & drop of files and folders — folders are attached as path-only references (desktop only).

- **Composer form** (`#agent-form`):
  - Section: Agent
  - Type: form

- **Message input** (`#composer-input`):
  - Section: Agent
  - Type: textarea
  - Shortcut: Ctrl+Enter / ⌘↩ to send

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
  - Notes: Shows the backend context usage as used/window (e.g. 32k/1M). The number is the server source of truth: the live server-side estimate while a turn streams and the provider-measured context fill after it completes. The frontend never sums message bubbles.

- **Stop** (`#stop-btn`):
  - Section: Agent
  - Type: button
  - Action: Cancels the running turn; partial output is kept and marked interrupted.

- **Send** (`#send-btn`):
  - Section: Agent
  - Type: submit
  - Shortcut: Ctrl+Enter / ⌘↩
