# Providers

Configure persistent Anthropic, OpenAI, and OpenRouter cards, add unlimited custom OpenRouter-compatible providers by wire format (Messages, Responses, Chat), and manage spawn-only ACP subagent binaries. Chat API keys live in the local SQLite credential store. ACP env values stay on disk in acp-agents.json; the wire API only returns env keys.

**How to open:** Click the Providers item in the left sidebar.

## Header

View title plus Add custom provider (unlimited OpenRouter-compatible gateways) and Add ACP agent (spawn-only subprocesses).

- **Providers header actions** (`#providers-header-actions`):
  - Section: Providers
  - Type: container

- **Add custom provider** (`#add-provider-btn`):
  - Section: Providers
  - Type: button
  - Action: Opens the custom OpenRouter-compatible provider editor; users can save any number of custom providers.

- **Add ACP agent** (`#add-acp-agent-btn`):
  - Section: Providers
  - Type: button
  - Action: Opens a generic command/args/env form to register a spawn-only ACP binary.

## Chat providers

The registry always shows persistent Anthropic, OpenAI, and OpenRouter cards, even before configuration. Anthropic uses the Anthropic Messages driver; OpenAI uses the OpenAI Responses driver; OpenRouter and custom providers use the OpenRouter driver. OpenRouter and custom forms expose Messages, Responses, and Chat API kinds. Selecting a card opens the detail pane for editing its base URL, API key, enabled state, importing models, and testing connectivity. These models appear in the Agent composer.

- **Chat providers section** (`#provider-llm-section`):
  - Section: Providers
  - Type: container

- **Provider registry** (`#provider-registry`):
  - Section: Providers
  - Type: list
  - Notes: Always contains Anthropic, OpenAI, and OpenRouter cards, followed by any number of custom provider cards. OpenRouter and custom cards show their selected API kind.

- **Provider detail** (`#provider-detail`):
  - Section: Providers
  - Type: container
  - Notes: Edit form for the selected provider.

## ACP subagents

A separate registry for Agent Client Protocol binaries. Register a generic command + args + env. Probe discovers auth methods, modes, and models at runtime. ACP agents never appear in the composer; the parent agent spawns them with the subagent tool. Command is immutable after save.

- **ACP subagents section** (`#provider-acp-section`):
  - Section: Providers
  - Type: container

- **ACP agent registry** (`#acp-agent-registry`):
  - Section: Providers
  - Type: list
  - Notes: Cards for spawn-only ACP binaries. Not shown in the Agent composer.

- **ACP agent detail** (`#acp-agent-detail`):
  - Section: Providers
  - Type: panel
  - Notes: Edit label/args/env, probe, authenticate, refresh catalog, and map advertised modes to risk tiers. Command is read-only after save.
