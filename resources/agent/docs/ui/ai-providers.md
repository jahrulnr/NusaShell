# AI Providers

Registry of built-in and custom AI provider connections. Enable, disable, and configure providers here.

**How to open:** Click the AI Providers item in the left sidebar.

## Provider registry

Cards for OpenRouter, OmniRoute, 9Router, OpenAI, Claude API, and any custom providers. Each card shows status, model count, and primary action.

- **Provider registry** (`#provider-registry`):
  - Section: Provider registry
  - Type: list
  - Action: Container for provider cards.

- **Provider preset card** (`[data-provider-preset]`):
  - Section: Provider registry
  - Type: article
  - Action: Built-in provider card. Shows provider name, kind, status, enable toggle, and Configure/Details action.

- **Provider card action** (`.provider-card-action`):
  - Section: Provider registry
  - Type: button
  - Action: Label changes between Configure (not set up) and Details (configured). Opens the provider editor or provider details.

- **Enable provider** (`.provider-toggle`):
  - Section: Provider registry
  - Type: toggle button
  - Action: Enables or disables the provider for agent turns. Disabled until the provider is configured.

- **Provider status** (`.provider-status`):
  - Section: Provider registry
  - Type: status text
  - Action: Shows 'Not configured', 'Needs API key', or 'Configured · N models'.

## Add custom provider

Button to create a new custom OpenAI-compatible provider.

- **Add custom provider** (`#add-custom-provider`):
  - Section: Add custom provider
  - Type: button
  - Action: Opens the provider editor for a new custom OpenAI-compatible provider.

## ACP agents

Registry of ACP (Agent Client Protocol) external agents. Enable and configure providers such as Cursor, Codex, Claude Code, and Gemini.

- **`#acp-provider-registry`** (missing map entry)

- **`#acp-provider-card`** (missing map entry)

- **`#acp-provider-toggle`** (missing map entry)

- **`#acp-provider-status`** (missing map entry)

- **`#acp-provider-configure`** (missing map entry)

## ACP provider configure modal

Modal for editing an ACP provider's command, args, and enabled state.

- **`#acp-provider-modal-overlay`** (missing map entry)

- **`#acp-provider-form`** (missing map entry)

- **`#acp-provider-title`** (missing map entry)

- **`#acp-provider-subtitle`** (missing map entry)

- **`#acp-provider-close`** (missing map entry)

- **`#acp-provider-id`** (missing map entry)

- **`#acp-provider-enabled`** (missing map entry)

- **`#acp-provider-command`** (missing map entry)

- **`#acp-provider-args`** (missing map entry)

- **`#acp-provider-auth-method`** (missing map entry)
