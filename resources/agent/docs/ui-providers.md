# Providers

Configure persistent Anthropic, OpenAI, OpenRouter, Codex, and Gemini cards, add unlimited custom providers by wire format (Messages, Responses, Chat, Gemini, Codex), and manage spawn-only ACP subagent binaries. Codex prefers Sign in with ChatGPT or Import from Codex CLI (optional token paste fallback); other provider keys are optional and live in the local credential store. ACP env values are stored server-side; the UI shows only env keys.

**How to open:** Click the Providers item in the left sidebar.

## Header

View title plus Add custom provider (unlimited compatible providers) and Add ACP agent (spawn-only subprocesses).

- **Add custom provider** (`#add-provider-btn`):
  - Type: button
  - Action: Opens the custom OpenRouter-compatible provider editor; users can save any number of custom providers.

- **Add ACP agent** (`#add-acp-agent-btn`):
  - Type: button
  - Action: Opens a generic command/args/env form to register a spawn-only ACP binary.

## Chat providers

The registry always shows persistent Anthropic, OpenAI, OpenRouter, Codex, and Gemini cards, even before configuration. Codex offers OAuth Sign in / Import from CLI as the primary auth paths. Custom forms expose Messages, Responses, Chat, Gemini, and Codex API kinds. Selecting a card opens the detail pane for editing its base URL, credential, enabled state, prompt-cache TTL, importing models, and testing connectivity. These models appear in the Agent composer.

- **Provider registry** (`#provider-registry`):
  - Type: list
  - Notes: Always contains the five built-in cards (Anthropic, OpenAI, OpenRouter, Codex, Gemini), followed by any number of custom provider cards. OpenRouter and custom cards show their selected API kind.

- **Provider detail** (`#provider-detail`):
  - Type: container
  - Notes: Edit form for the selected provider, including selectable prompt-cache TTL chips.

- **Prompt cache TTL** (`.provider-cache-ttl-chip`):
  - Type: chip-group
  - Action: Selects the prompt-cache duration sent to this provider when Settings prompt caching is on, or off to skip caching for this provider. Messages and OpenRouter chat offer 5m/1h/off; Responses and other Chat hosts offer 30m/off.
  - Notes: Buttons inside the provider detail pane. The last chip is off. Empty stored TTL still defaults to the first duration. The registry card shows the selected value only.

- **Thinking summary** (`.provider-cache-ttl-chip`):
  - Type: chip-group
  - Action: Selects how verbose the visible Codex thinking summary should be: auto, concise, detailed, or none.
  - Notes: Available only for Codex-kind providers. The selected value is stored per provider and applies on the next turn; none suppresses the visible summary without disabling model reasoning.

## Codex accounts and runtime

On the Codex detail pane, the ChatGPT Accounts card shows multi-account OAuth identity, plan, usage bars, Switch/Remove, Sign in with ChatGPT, Import from Codex CLI, and Refresh circuits. The Codex Runtime card shows managed CLI binary status and Download.

- **Codex account list** (`#codex-account-list`):
  - Type: list
  - Notes: Signed-in ChatGPT account rows with Switch/Remove actions.

- **Import from Codex CLI** (`#codex-import-cli-btn`):
  - Type: button
  - Action: Imports ChatGPT auth from the local Codex CLI.

- **Refresh Codex circuits** (`#codex-refresh-circuits-btn`):
  - Type: button
  - Action: Polls account usage circuits, then reloads the accounts list.

- **Sign in with ChatGPT** (`#codex-login-btn`):
  - Type: button
  - Action: Starts Codex OAuth sign-in.

- **Codex runtime status** (`#codex-runtime-status`):
  - Type: text
  - Notes: Shows installed version/path, downloading, or not installed.

- **Download Codex runtime** (`#codex-runtime-download-btn`):
  - Type: button
  - Action: Downloads the managed Codex CLI binary.

## ACP subagents

A separate registry for Agent Client Protocol binaries. Register a generic command + args + env. Probe contacts the binary and caches the advertised auth methods; Refresh catalog imports the advertised modes and models. ACP agents never appear in the composer; the parent agent spawns them with the subagent tool. Command is immutable after save.

- **ACP agent registry** (`#acp-agent-registry`):
  - Type: list
  - Notes: Cards for spawn-only ACP binaries. Not shown in the Agent composer.

- **ACP agent detail** (`#acp-agent-detail`):
  - Type: panel
  - Notes: Edit label/args/env, probe, authenticate, refresh catalog, and map advertised modes to risk tiers. Command is read-only after save.
