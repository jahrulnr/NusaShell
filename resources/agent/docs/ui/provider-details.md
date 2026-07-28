# Provider Details

Deep view for one provider: connection info, imported models, import catalog, add model, edit, and delete.

**How to open:** Click Details or Configure on an AI Providers card.

## Provider summary

Back link, provider name, API type, base URL, masked API key state, default model, and enabled status.

- **Back to AI Providers** (`#provider-details-back`):
  - Section: Provider summary
  - Type: button
  - Action: Returns to the AI Providers registry list.

- **Provider name** (`#provider-detail-title`):
  - Section: Provider summary
  - Type: heading
  - Action: Displays the provider name.

- **Provider id and API type** (`#provider-detail-subtitle`):
  - Section: Provider summary
  - Type: text
  - Action: Displays the provider id and API type (chat/responses/messages).

- **Base URL** (`#provider-detail-base-url`):
  - Section: Provider summary
  - Type: text
  - Action: Displays the configured API base URL or 'Local'.

- **API key state** (`#provider-detail-key`):
  - Section: Provider summary
  - Type: text
  - Action: Shows whether an API key is saved, optional, or missing.

- **Default model** (`#provider-detail-default-model`):
  - Section: Provider summary
  - Type: text
  - Action: Displays the provider's default model or 'Not set — choose per turn'.

- **Enabled status** (`#provider-detail-status`):
  - Section: Provider summary
  - Type: text
  - Action: Shows whether the provider is Enabled or Disabled.

## Provider actions

Edit the connection, import the provider's model catalog, or delete the provider.

- **Edit** (`#provider-detail-edit`):
  - Section: Provider actions
  - Type: button
  - Action: Opens the provider editor for this provider.

- **Import models** (`#provider-import-models`):
  - Section: Provider actions
  - Type: button
  - Action: Fetches the provider's model catalog and imports it. Errors appear in the provider import error box.

- **Delete** (`#provider-detail-delete`):
  - Section: Provider actions
  - Type: button
  - Action: Opens the delete provider confirmation dialog.

- **Import error message** (`#provider-import-error`):
  - Section: Provider actions
  - Type: alert
  - Action: Displays an error when model import fails.

## Available models

Searchable list of imported models. Each row shows model id, label, context window, input modes, and capability badges.

- **Model count** (`#provider-model-count`):
  - Section: Available models
  - Type: text
  - Action: Shows the number of imported models for this provider.

- **Search imported models** (`#provider-model-search`):
  - Section: Available models
  - Type: search input
  - Action: Filters the imported model list by id, label, or description.

- **Add model** (`#provider-add-model`):
  - Section: Available models
  - Type: button
  - Action: Prompts for a model id and optional display label, then adds it manually to the provider.

- **Model list** (`#provider-model-list`):
  - Section: Available models
  - Type: list
  - Action: Container for imported model rows.

- **Model row** (`.provider-model-item`):
  - Section: Available models
  - Type: listitem
  - Action: Displays model id, label, context window, input modes, and capability badges.
