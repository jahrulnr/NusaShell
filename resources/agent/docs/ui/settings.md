# Settings

System configuration, connection status, agent runtime preferences, and app info.

**How to open:** Click the settings gear icon in the title bar, or use the Settings navigation if added.

## System

Shows the running NusaShell version, backend connection state, WebSocket URL, and a manual ping button.

- **NusaShell version** (`#settings-version`):
  - Section: System
  - Type: text
  - Action: Displays the running NusaShell version.

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

## Agent runtime

Configures how the agent picks and retries models: provider strategy, attempt budget, vision mode, and streaming.

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

- **Stream responses** (`#settings-ai-stream`):
  - Section: Agent runtime
  - Type: checkbox
  - Action: Enables streaming assistant responses when the provider supports it.

## About

Product name and build label.
