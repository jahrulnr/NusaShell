# Agent

The agent conversation workspace. Chat with the NusaShell agent, attach files, pick models, and manage conversation threads.

**How to open:** Click the Agent item in the left sidebar.

## Conversation list

Left panel listing local conversation threads. Click a thread to open it; use the × button to delete.

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

Main message area showing user and assistant messages, tool-call result tags, attachments, and retry buttons.

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
  - Action: Displays a user or assistant message bubble, plus metadata like model, trace id, and tool-call badges.

- **Message bubble** (`.agent-bubble`):
  - Section: Conversation thread
  - Type: div
  - Action: Contains the rendered message text. Assistant messages are rendered as Markdown.

- **Retry turn** (`.agent-retry-btn`):
  - Section: Conversation thread
  - Type: button
  - Action: Appears on a failed assistant turn. Click to re-run the same turn.

## Composer

Input area at the bottom of the thread. Write a prompt, attach images or PDFs, choose a model, and send the turn.

- **Composer form** (`#agent-form`):
  - Section: Composer
  - Type: form
  - Action: Wraps the message textarea, attachments, and send controls. Submit triggers the agent turn.

- **Message input** (`#agent-input`):
  - Section: Composer
  - Type: textarea
  - Action: Primary text area for typing a prompt to the agent.
  - Shortcut: Ctrl+Enter or Cmd+Enter submits the turn.

- **Attachment chips** (`#agent-attachments`):
  - Section: Composer
  - Type: list
  - Action: Shows files attached to the current turn. Each chip can be removed.

- **File attachment input** (`#agent-file-input`):
  - Section: Composer
  - Type: file input
  - Action: Hidden file picker triggered by the attach button. Accepts images and PDFs.

- **Attach files** (`#agent-attach-btn`):
  - Section: Composer
  - Type: icon button
  - Action: Opens the file picker to attach images or PDFs to the current turn. Up to 4 files, each under 4 MiB.
  - Related: File attachment input (`#agent-file-input`), Attachment chips (`#agent-attachments`)

- **Model and effort trigger** (`#agent-model-trigger`):
  - Section: Composer
  - Type: button
  - Action: Opens the model/effort picker dropdown. Label shows the selected model and effort, e.g. 'Model · auto'.

- **Model trigger label** (`#agent-model-trigger-label`):
  - Section: Composer
  - Type: text
  - Action: Text inside the model trigger showing the selected model id and effort value.

- **Model picker dropdown** (`#agent-model-menu`):
  - Section: Composer
  - Type: dialog
  - Action: Searchable list of imported models. Choose a model and, if supported, a reasoning effort level.

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
  - Action: Shows used / total context tokens for the selected model, or 'Choose a model' when none is selected.

- **Stop turn** (`#agent-stop-btn`):
  - Section: Composer
  - Type: icon button
  - Action: Cancels the currently running agent turn. Visible only while a turn is in progress.

- **Send turn** (`#agent-send-btn`):
  - Section: Composer
  - Type: submit button
  - Action: Submits the composer form and starts the agent turn.

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
