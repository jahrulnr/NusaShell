# Agent

The default view: multi-conversation chat against any configured provider model. Each conversation keeps its own history on disk and can use skills, memory, docs, and MCP servers as tools.

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
  - Notes: Renders the active conversation messages.

## Tool call strip

Shows in-flight tool calls for the current turn, with one entry per MCP or built-in tool invocation.

- **Composer stack** (`#agent-composer-stack`):
  - Section: Agent
  - Type: container
  - Notes: Holds tool strip, todo strip, steer queue, and composer.

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

The message input with attachments, model picker, workspace selector, provider status, stop, and send buttons. Ctrl+Enter (⌘↩ on Mac) sends.

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

- **Provider status** (`#agent-provider-status`):
  - Section: Agent
  - Type: text

- **Stop** (`#stop-btn`):
  - Section: Agent
  - Type: button
  - Action: Cancels the running turn; partial output is kept and marked interrupted.

- **Send** (`#send-btn`):
  - Section: Agent
  - Type: submit
  - Shortcut: Ctrl+Enter / ⌘↩
