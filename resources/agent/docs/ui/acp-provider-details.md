# ACP Provider Details

Deep view for one ACP agent: connection info, imported models, default model/mode selection, import catalog, edit, and live config-option snapshot.

**How to open:** Click Details on a connected ACP Agents card.

## ACP agent summary

Back link, agent display name, description, command, auth status, default model, default mode, and enabled status.

- **Back to ACP Agents** (`#acp-provider-details-back`):
  - Section: ACP provider details
  - Type: button
  - Action: Returns to the AI Providers view.

- **Agent title** (`#acp-provider-detail-title`):
  - Section: ACP provider details
  - Type: text
  - Action: Displays the ACP agent display name.

- **Agent description** (`#acp-provider-detail-subtitle`):
  - Section: ACP provider details
  - Type: text
  - Action: Displays the ACP agent manifest description.

- **Command** (`#acp-provider-detail-command`):
  - Section: ACP provider details
  - Type: text
  - Action: Displays the resolved command and args for the ACP agent.

- **Auth status** (`#acp-provider-detail-auth`):
  - Section: ACP provider details
  - Type: text
  - Action: Displays the ACP agent auth status (Connected, Needs auth, Not probed).

- **Default model** (`#acp-provider-detail-default-model`):
  - Section: ACP provider details
  - Type: text
  - Action: Displays the persisted default model id for this ACP agent.

- **Default mode** (`#acp-provider-detail-default-mode`):
  - Section: ACP provider details
  - Type: text
  - Action: Displays the effective default mode (preferredConfig.mode or manifest defaultMode).

- **Status** (`#acp-provider-detail-status`):
  - Section: ACP provider details
  - Type: text
  - Action: Displays whether the ACP agent is enabled or disabled.

## ACP agent actions

Edit the agent configuration or import its model catalog by probing a fresh session.

- **Edit** (`#acp-provider-detail-edit`):
  - Section: ACP provider details
  - Type: button
  - Action: Opens the ACP provider configure modal for this agent.

- **Import models** (`#acp-provider-import-models`):
  - Section: ACP provider details
  - Type: button
  - Action: Probes a fresh ACP session, discovers the model list and config-option snapshot, and persists them to the store.

- **Import error** (`#acp-provider-import-error`):
  - Section: ACP provider details
  - Type: text
  - Action: Shows the error message when an import-models probe fails.

## Models

Searchable list of imported models discovered from the last probe. Each row shows model id and label.

- **Model count** (`#acp-provider-model-count`):
  - Section: ACP provider details
  - Type: text
  - Action: Displays the number of imported models for this ACP agent.

- **Search models** (`#acp-provider-model-search`):
  - Section: ACP provider details
  - Type: search
  - Action: Filters the imported model list by id, label, or description.

- **Model list** (`#acp-provider-model-list`):
  - Section: ACP provider details
  - Type: container
  - Action: Renders the searchable list of imported models.

## Defaults

Default model and default mode (bypass / yolo) selects. Applied to every subagent run and ACP thread from this agent via preferredConfig.

- **Default model** (`#acp-provider-default-model-select`):
  - Section: ACP provider details
  - Type: select
  - Action: Sets the default model for this ACP agent via setDefaultModel; mirrored into preferredConfig.model.

- **Default mode (bypass / yolo)** (`#acp-provider-default-mode-select`):
  - Section: ACP provider details
  - Type: select
  - Action: Sets the default mode for this ACP agent via setDefaultMode; mirrored into preferredConfig.mode.

## Config options snapshot

Read-only snapshot of live session config options from the last probe, excluding model and mode which have their own controls above.

- **Config options card** (`#acp-provider-config-options-card`):
  - Section: ACP provider details
  - Type: container
  - Action: Wraps the read-only config-option snapshot from the last probe.

- **Config options** (`#acp-provider-config-options`):
  - Section: ACP provider details
  - Type: container
  - Action: Renders read-only rows for each live config option (excluding model and mode).
