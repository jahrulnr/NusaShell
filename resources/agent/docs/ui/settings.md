# Settings

System configuration, connection status, startup/background behavior, agent runtime preferences, and app info.

**How to open:** Click the settings gear icon in the title bar, or use the Settings navigation if added.

## System

Shows the running NusaShell version, backend connection state, WebSocket URL, and a manual ping button.

- **NusaShell version** (`#settings-version`):
  - Section: System
  - Type: text
  - Action: Displays the running NusaShell version.

- **Build type** (`#settings-build`):
  - Section: About
  - Type: text
  - Action: Displays dev for an unpackaged --dev run and production for packaged or non-dev runs.

- **Application version** (`#settings-about-version`):
  - Section: About
  - Type: text
  - Action: Displays the running NusaShell version in the About card.

- **Backend status dot** (`#settings-conn-dot`):
  - Section: System
  - Type: status indicator
  - Action: Visual dot showing whether the backend WebSocket is connected.

- **Backend status label** (`#settings-conn-label`):
  - Section: System
  - Type: status text
  - Action: Text label showing 'Connected' or 'Disconnected'.

- **WebSocket URL** (`#settings-ws-url`):
  - Section: System
  - Type: code
  - Action: Displays the WebSocket URL the app is connected to.

- **Ping** (`#ping-btn`):
  - Section: System
  - Type: button
  - Action: Sends a ping to the backend and shows 'pong: true' or an error.

- **Ping result** (`#ping-result`):
  - Section: System
  - Type: status text
  - Action: Displays the ping response or error after clicking Ping.

## Connection

Toggles for WebSocket auto-reconnect and auto-resubscribe.

- **Auto-reconnect** (`.settings-card input[type="checkbox"]`):
  - Section: Connection
  - Type: toggle
  - Action: When enabled, the renderer reconnects to the backend automatically if the WebSocket drops.

- **Auto-resubscribe** (`.settings-card input[type="checkbox"]`):
  - Section: Connection
  - Type: toggle
  - Action: When enabled, the renderer re-subscribes to backend events after reconnecting.

## Startup & background

Controls OS login autostart, tray-first launch, and whether closing the launcher hides to the tray so learning and scheduled work keep running. Descriptions wrap in the text column beside each fixed-width toggle. Quit from the tray menu for a full stop.

- **Startup & background card** (`#app-behavior-card`):
  - Section: Startup & background
  - Type: panel
  - Action: Holds login autostart, start-in-tray, and keep-in-background toggles for the desktop shell.

- **Launch at login** (`#settings-launch-at-login`):
  - Section: Startup & background
  - Type: toggle
  - Action: When enabled on a packaged build, writes OS login autostart so NusaShell starts after login. Disabled in unpackaged/dev builds.

- **Start in tray** (`#settings-start-hidden`):
  - Section: Startup & background
  - Type: toggle
  - Action: When launch-at-login starts the app, stay in the system tray until Open NusaShell is chosen.

- **Keep running when window is closed** (`#settings-keep-in-background`):
  - Section: Startup & background
  - Type: toggle
  - Action: Closing the launcher hides to the tray instead of quitting so learning and jobs keep running. Quit from the tray menu to stop fully.

- **Agent Canvas** (`#settings-canvas-enabled`):
  - Section: Startup & background
  - Type: toggle
  - Action: Enables or disables inline HTML/SVG/Mermaid rendering and the canvas sidebar. When off, canvas fences stay as source code blocks.

## Agent runtime

Configures how the agent picks and retries models: provider strategy, attempt budget, vision mode, user prompt, and streaming.

- **Agent runtime form** (`#ai-runtime-form`):
  - Section: Agent runtime
  - Type: form
  - Action: Contains the provider strategy, attempt budget, vision, and stream settings. Submit saves the runtime.

- **Provider strategy** (`#settings-ai-strategy`):
  - Section: Agent runtime
  - Type: select
  - Action: Controls model selection: Failover, Round robin, or Selected provider only.

- **Total attempt budget** (`#settings-ai-budget`):
  - Section: Agent runtime
  - Type: number input
  - Action: Maximum number of agent tool-call attempts per turn (1–32).

- **Vision** (`#settings-ai-vision`):
  - Section: Agent runtime
  - Type: select
  - Action: Controls image delivery. Automatic sends image parts first and retries once without them after a provider 4xx response; Disable omits image pixels.

- **User prompt** (`#settings-ai-user-prompt`):
  - Section: Agent runtime
  - Type: textarea
  - Action: Additional instructions appended after the system prompt. Use for personality, tone, or task-specific guidance.

- **Stream responses** (`#settings-ai-stream`):
  - Section: Agent runtime
  - Type: checkbox
  - Action: Enables streaming assistant responses when the provider supports it.

## Agent limits

Controls how many tool calls and rounds the agent can make per turn. Prevents runaway loops.

- **Agent limits form** (`#ai-limits-form`):
  - Section: Agent limits
  - Type: form
  - Action: Contains max tool rounds and max repeated tool calls. Submit saves the limits.

- **Max tool rounds** (`#settings-ai-max-tool-rounds`):
  - Section: Agent limits
  - Type: number input
  - Action: Maximum number of tool-call rounds per agent turn (1–100). Default 50.

- **Max repeated tool calls** (`#settings-ai-max-repeated-tool-calls`):
  - Section: Agent limits
  - Type: number input
  - Action: Stops the agent if the same tool call repeats this many times (1–200). Default 50.

## Context compaction

Controls how the agent compacts conversation history when the input token budget is exceeded.

- **Context compaction form** (`#ai-context-form`):
  - Section: Context compaction
  - Type: form
  - Action: Contains compaction toggle and token budget settings. Submit saves the context config.

- **Enable compaction** (`#settings-ai-compaction`):
  - Section: Context compaction
  - Type: toggle
  - Action: When enabled, the agent summarizes older conversation history to fit within the token budget.

- **Max input tokens** (`#settings-ai-max-input-tokens`):
  - Section: Context compaction
  - Type: number input
  - Action: Token budget for the input prompt before compaction triggers (1000–2000000). Default 12000.

- **Reserve tokens** (`#settings-ai-reserve-tokens`):
  - Section: Context compaction
  - Type: number input
  - Action: Tokens reserved for the model response, subtracted from the input budget (0–1000000). Default 3000.

- **Recent turns to keep** (`#settings-ai-recent-turns`):
  - Section: Context compaction
  - Type: number input
  - Action: Number of recent user-assistant turns preserved during compaction (1–100). Default 4.

- **Summary max characters** (`#settings-ai-summary-max-chars`):
  - Section: Context compaction
  - Type: number input
  - Action: Maximum character length of the compacted summary (100–1000000). Default 12000.

## About

Product name and build label.
