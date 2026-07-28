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
