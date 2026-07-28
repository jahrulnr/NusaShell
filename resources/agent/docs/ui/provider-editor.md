# Provider Editor

Form to configure or edit an AI provider connection and its runtime defaults.

**How to open:** Click Configure on a provider card, or Edit on the Provider Details view.

## Identity

For custom providers: name and id/prefix. Built-in providers hide the custom fields and use their preset id.

- **Provider editor form** (`#ai-settings-form`):
  - Section: Identity
  - Type: form
  - Action: Contains all provider fields. Submit saves the provider configuration.

- **Editor title** (`#ai-settings-title`):
  - Section: Identity
  - Type: heading
  - Action: Shows 'Configure provider' or 'Edit {Provider}'.

- **Editor subtitle** (`#ai-settings-subtitle`):
  - Section: Identity
  - Type: text
  - Action: Short description of the provider kind.

- **Close editor** (`#ai-settings-close`):
  - Section: Identity
  - Type: icon button
  - Action: Closes the provider editor without saving.

- **Preset id (hidden)** (`#settings-ai-preset-id`):
  - Section: Identity
  - Type: hidden input
  - Action: Stores the selected preset id used to pre-fill built-in provider fields.

- **Provider type (hidden)** (`#settings-ai-provider-type`):
  - Section: Identity
  - Type: hidden input
  - Action: Stores the internal provider type, e.g. 'openai-compatible'.

- **Custom provider fields** (`#provider-custom-fields`):
  - Section: Identity
  - Type: fieldset
  - Action: Shown only for custom providers. Contains name, id, and API type.

- **Provider name** (`#settings-ai-name`):
  - Section: Identity
  - Type: text input
  - Action: Display name for a custom provider.

- **Provider id / prefix** (`#settings-ai-id`):
  - Section: Identity
  - Type: text input
  - Action: Machine-friendly id for a custom provider. Read-only when editing.

- **API type** (`#settings-ai-api`):
  - Section: Identity
  - Type: select
  - Action: Protocol used by the provider: Chat completions, Responses, or Messages.

## Connection

Base URL, API key, and default model for the provider.

- **Base URL** (`#settings-ai-base-url`):
  - Section: Connection
  - Type: url input
  - Action: The provider's OpenAI-compatible or native base URL.

- **API key** (`#settings-ai-api-key`):
  - Section: Connection
  - Type: password input
  - Action: API key for the provider. Leave blank to keep a saved key. Placeholder indicates whether a key exists.

- **Default model** (`#settings-ai-model`):
  - Section: Connection
  - Type: text input
  - Action: Optional default model id. Can be left blank and chosen per turn.

## Runtime

Timeout in seconds, retry attempts, routing weight, and enabled toggle.

- **Timeout (seconds)** (`#settings-ai-timeout`):
  - Section: Runtime
  - Type: number input
  - Action: Request timeout in seconds (1–600).

- **Attempts** (`#settings-ai-attempts`):
  - Section: Runtime
  - Type: number input
  - Action: Maximum retry attempts for a single request (1–10).

- **Weight** (`#settings-ai-weight`):
  - Section: Runtime
  - Type: number input
  - Action: Routing weight used when provider strategy is round-robin.

- **Enabled** (`#settings-ai-enabled`):
  - Section: Runtime
  - Type: checkbox
  - Action: Whether the provider is available for agent turns.

- **Key state** (`#settings-ai-key-state`):
  - Section: Runtime
  - Type: status text
  - Action: Indicates whether an API key is saved, optional, or missing.
