# Providers

Configure persistent Anthropic, OpenAI, OpenRouter, and Codex cards, add unlimited custom providers by wire format (Messages, Responses, Chat, Codex), and manage spawn-only ACP subagent binaries. Codex prefers Sign in with ChatGPT or Import from Codex CLI (optional token paste fallback); other provider keys are optional and live in the local SQLite credential store. ACP env values stay on disk in acp-agents.json; the wire API only returns env keys.

**How to open:** Click the Providers item in the left sidebar.

## Header

View title plus Add custom provider (unlimited compatible providers) and Add ACP agent (spawn-only subprocesses).

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

The registry always shows persistent Anthropic, OpenAI, OpenRouter, and Codex cards, even before configuration. Anthropic uses the Anthropic Messages driver; OpenAI uses the OpenAI Responses driver; OpenRouter uses the OpenRouter driver; Codex uses the Codex driver with OAuth Sign in / Import from CLI as the primary auth paths. Custom forms expose Messages, Responses, Chat, and Codex API kinds. Selecting a card opens the detail pane for editing its base URL, credential, enabled state, prompt-cache TTL, importing models, and testing connectivity. These models appear in the Agent composer.

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
  - Notes: Edit form for the selected provider, including selectable prompt-cache TTL chips.

- **Prompt cache TTL** (`.provider-cache-ttl-chip`):
  - Section: Providers
  - Type: chip-group
  - Action: Selects the prompt-cache duration sent to this provider when Settings prompt caching is on, or off to skip caching for this provider. Messages and OpenRouter chat offer 5m/1h/off; Responses and other Chat hosts offer 30m/off.
  - Notes: Buttons inside the provider detail pane (class provider-cache-ttl-chip). The last chip is off. Empty stored TTL still defaults to the first duration. The registry card shows the selected value only.

## Codex accounts and runtime

On the Codex detail pane, the ChatGPT Accounts card shows multi-account OAuth identity, plan, usage bars, Switch/Remove, Sign in with ChatGPT, Import from Codex CLI, and Refresh circuits. The Codex Runtime card shows managed CLI binary status and Download.

- **Codex account list** (`#codex-account-list`):
  - Section: Providers
  - Type: list
  - Notes: Rows from ai.codex.usage (or ai.codex.accounts.list fallback) with Switch/Remove actions.

- **Import from Codex CLI** (`#codex-import-cli-btn`):
  - Section: Providers
  - Type: button
  - Action: Imports ChatGPT auth from the local Codex CLI auth.json via ai.codex.import.

- **Refresh Codex circuits** (`#codex-refresh-circuits-btn`):
  - Section: Providers
  - Type: button
  - Action: Polls account usage circuits via ai.codex.refresh-circuits then reloads the accounts list.

- **Sign in with ChatGPT** (`#codex-login-btn`):
  - Section: Providers
  - Type: button
  - Action: Starts Codex OAuth PKCE login via ai.codex.login.

- **Codex runtime status** (`#codex-runtime-status`):
  - Section: Providers
  - Type: text
  - Notes: Shows installed version/path, downloading, or not-installed from ai.codex.runtime.status.

- **Download Codex runtime** (`#codex-runtime-download-btn`):
  - Section: Providers
  - Type: button
  - Action: Downloads the managed Codex CLI binary via ai.codex.runtime.download.

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
