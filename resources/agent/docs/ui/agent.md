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

The full-height message runway separates compact user cards from editorial assistant responses. User attachments render as image previews or file cards; completed tool calls form a collapsible status timeline based on real turn results. Message footers expose timestamps, model/trace metadata, copy, and retry where applicable. Only this area scrolls.

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

- **Tool activity** (`.agent-activity`):
  - Section: Conversation thread
  - Type: disclosure
  - Action: Expands or collapses the completed tool-call timeline. Each row reports the persisted tool name and success or failure state without implying live progress.

- **Copy message** (`.agent-message-action`):
  - Section: Conversation thread
  - Type: icon button
  - Action: Copies the message text through the Electron clipboard bridge and briefly confirms success.

- **Retry turn** (`.agent-retry-btn`):
  - Section: Conversation thread
  - Type: button
  - Action: Appears on a failed assistant turn. Click to re-run the same turn.

## Composer

A raised command dock at the bottom of the thread. Its message input starts at one row, grows with wrapped or explicit lines, and caps at ten rows before scrolling internally. Attach images, PDFs, or text files, choose a model, and send the turn without losing the conversation rail.

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
