# Providers

Provider configuration: a two-tab workspace. The Providers tab lists configured LLM connections with a detail inspector (connection fields, models, Codex account, enable toggle, cache TTL and thinking-summary chips); the ACP tab manages spawn-only subagent workers (registry, probe, auth, modes, models).

**How to open:** Click the Providers item in the left rail, or open #providers.

## Tabs and toolbar

Two tabs switch between LLM providers and ACP subagents; each panel has its own search, counts, sync state, refresh, and add action. On mobile each tab starts with its own list and opens one selected detail at a time.

- **Provider tabs** (`#providers-tabs`):

- **Providers tab** (`#providers-tab`):

- **ACP tab** (`#acp-tab`):

- **Provider search** (`#providers-search`):

- **Configured count** (`#providers-count`):

- **Providers sync state** (`#providers-sync-state`):

- **Refresh providers** (`#providers-refresh`):

- **Add provider** (`#providers-add`):

- **Add provider** (`#providers-empty-add`):

## Configured providers

The list shows provider connections; the inspector renders connection fields, enable toggle, and the imported model list with filter. For Codex, the account and runtime card follows the connection/model grid in normal page flow. On mobile, Back returns to the same filtered provider list, and models and account details scroll with the page.

- **Provider registry** (`#providers-list`):

- **Provider inspector** (`#providers-inspector`):

- **Inspector empty state** (`#providers-empty`):

- **Provider detail** (`#providers-detail`):

- **Back to connections** (`#providers-mobile-back`):
  - Type: button
  - Action: Returns to the filtered provider list while keeping the selected connection.

- **Provider mark** (`#providers-detail-mark`):

- **Provider driver** (`#providers-detail-driver`):

- **Provider name** (`#providers-detail-title`):

- **Provider ID** (`#providers-detail-id`):

- **Edit provider** (`#providers-edit`):

- **Test connection** (`#providers-test`):

- **Import models** (`#providers-import`):

- **Delete provider** (`#providers-delete`):

- **Provider status** (`#providers-detail-status`):

- **Last latency** (`#providers-detail-latency`):

- **Model count** (`#providers-detail-model-count`):

- **Connection card** (`#providers-overview-title`):

- **Connection fields** (`#providers-connection-list`):

- **Enabled row** (`#providers-enabled-row`):

- **Enabled** (`#providers-enabled`):

- **Models card** (`#providers-models-title`):

- **Imported model count** (`#providers-models-summary`):

- **Filter models** (`#providers-model-search`):

- **Model list** (`#providers-model-list`):

- **Prompt cache TTL** (`provider-cache-ttl`):
  - Type: chip-group
  - Action: Selects the prompt-cache duration sent to this provider when Settings prompt caching is on, or off to skip caching for this provider. Messages and OpenRouter chat offer 5m/1h/off; Responses and other Chat hosts offer 30m/off.
  - Notes: Buttons inside the provider detail pane (element id provider-cache-ttl). The last chip is off. Empty stored TTL still defaults to the first duration.

- **Thinking summary** (`provider-reasoning-summary`):
  - Type: chip-group
  - Action: Selects how verbose the visible Codex thinking summary should be: auto, concise, detailed, or none.
  - Notes: Available only for Codex-kind providers. The selected value is stored per provider and applies on the next turn; none suppresses the visible summary without disabling model reasoning.

- **Provider note** (`#providers-detail-note`):

- **List title** (`#providers-index-title`):

- **List count** (`#providers-index-count`):

## Codex accounts and runtime

The account card signs in with a device code: Sign in shows a code, Open sign-in page, Copy code, and Cancel sign in. The browser checks approval at the supplied interval until completion or the 15-minute expiry, then refreshes accounts. It never opens the backend machine browser. Import CLI reads local CLI credentials and Refresh reloads circuit state. A separate sibling runtime card reports version/path and downloads the runtime on demand.

Account quota shows both returned windows with used/available percentages and reset date/time. Optional limit_window_seconds labels actual 5-hour, Weekly, or Monthly periods; missing durations use neutral window labels and missing usage is unavailable.

- **Codex card** (`#providers-codex-card`):

- **Codex card title** (`#providers-codex-title`):

- **Sign in with ChatGPT** (`#providers-codex-login`):
  - Type: button
  - Action: Starts Codex OAuth PKCE login via ai.codex.login.

- **Import from Codex CLI** (`#providers-codex-import`):
  - Type: button
  - Action: Imports ChatGPT auth from the local Codex CLI auth.json via ai.codex.import.

- **Refresh Codex circuits** (`#providers-codex-refresh`):
  - Type: button
  - Action: Polls account usage circuits via ai.codex.refresh-circuits then reloads the accounts list.

- **Codex account status** (`#providers-codex-status`):

- **Codex account list** (`#providers-account-list`):

- **Codex runtime card** (`codex-runtime-card`):
  - Type: container
  - Notes: Injected inside the Codex card for codex providers: reports the embedded Codex runtime state.

- **Codex runtime status** (`codex-runtime-status`):
  - Type: text
  - Notes: Shows installed version/path, downloading, or not-installed from ai.codex.runtime.status.

- **Download Codex runtime** (`codex-runtime-download-btn`):
  - Type: button
  - Action: Downloads the managed Codex CLI binary via ai.codex.runtime.download.

- **Device-code sign in** (`#providers-device-login`):
  - Type: status and actions
  - Action: Shows verification link and code with Copy code and Cancel sign in. Checks approval until completion or expiry; errors restore Sign in for retry.

## ACP agents

The ACP panel registers spawn-only workers: search, add, edit, probe, refresh catalog, and delete, with detail cards for connection, auth methods, modes, and advertised models. On mobile, Back returns to the ACP list without clearing its search or selected worker.

The Models card includes Search ACP models. It filters the advertised catalog by model ID, name, description, or tier; no matching model is shown as an explicit empty result, and clearing the query restores the catalog without changing the preferred model.

- **ACP panel** (`#acp-panel`):

- **ACP search** (`#acp-search`):

- **ACP count** (`#acp-count`):

- **ACP sync state** (`#acp-sync-state`):

- **Refresh ACP agents** (`#acp-refresh`):

- **Add ACP agent** (`#acp-add`):

- **ACP list title** (`#acp-index-title`):

- **ACP list count** (`#acp-index-count`):

- **ACP agent registry** (`#acp-list`):

- **ACP inspector** (`#acp-inspector`):

- **ACP agent detail** (`#acp-detail`):

- **Back to connections** (`#acp-mobile-back`):
  - Type: button
  - Action: Returns to the ACP list while keeping its search and selected worker.

- **ACP mark** (`#acp-detail-mark`):

- **ACP transport** (`#acp-detail-transport`):

- **ACP agent name** (`#acp-detail-title`):

- **ACP agent ID** (`#acp-detail-id`):

- **Edit ACP agent** (`#acp-edit`):

- **Probe ACP agent** (`#acp-probe`):

- **Refresh catalog** (`#acp-refresh-catalog`):

- **Delete ACP agent** (`#acp-delete`):

- **ACP connection fields** (`#acp-connection-list`):

- **Enabled row** (`#acp-enabled-row`):

- **Enabled** (`#acp-enabled`):

- **Authentication card** (`#acp-auth-card`):

- **Authentication title** (`#acp-auth-title`):

- **Auth methods summary** (`#acp-auth-summary`):

- **Auth methods list** (`#acp-auth-list`):

- **Modes card** (`#acp-modes-card`):

- **Modes title** (`#acp-modes-title`):

- **Modes list** (`#acp-modes-list`):

- **Models card** (`#acp-models-card`):

- **Models title** (`#acp-models-title`):

- **Advertised models** (`#acp-models-list`):

- **Mode count** (`#acp-detail-mode-count`):

- **Model count** (`#acp-detail-model-count`):

- **ACP status** (`#acp-detail-status`):

- **ACP note** (`#acp-detail-note`):

- **Search ACP models** (`#acp-model-search`):
  - Type: search
  - Action: Filters advertised ACP model rows without changing the preferred model.

- **Advertised ACP model count** (`#acp-models-summary`):
  - Type: text
