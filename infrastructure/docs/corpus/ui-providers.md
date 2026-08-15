# Providers

Configure LLM backends by wire format (Messages, Responses, Chat, Ollama, Codex). API keys live in the local SQLite credential store, never in the JSON files.

**How to open:** Click the Providers item in the left sidebar.

## Header

View title and an Add provider button.

- **Add provider** (`#add-provider-btn`):
  - Section: Providers
  - Type: button
  - Action: Opens the provider editor.

## Registry and detail

The registry lists providers as cards. Selecting one opens the detail pane for editing the base URL, API key, enabled state, importing models, and testing connectivity.

- **Provider registry** (`#provider-registry`):
  - Section: Providers
  - Type: list
  - Notes: Provider cards grouped by kind.

- **Provider detail** (`#provider-detail`):
  - Section: Providers
  - Type: container
  - Notes: Edit form for the selected provider.
