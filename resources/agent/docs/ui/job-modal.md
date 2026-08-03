# New job modal

Form to create a new scheduled job with name, schedule expression, mode (agent prompt or plugin tool call), and optional repeat limit.

**How to open:** Click the + New job button in the Jobs view.

## Job form

Name, trigger picker (schedule vs event), schedule (every 30m / 2h / 1d / 5-field cron / ISO timestamp) or event pattern + plugin scope + throttle/max-fires, mode selector, agent prompt + model picker or plugin tool schema form, and optional repeat count. Schedule is validated on blur with a live description or error. Event pattern hints show available event types from the selected plugin's manifest automation.emits. The mode-dependent fields toggle via hidden + aria-hidden. Agent mode shows a required Prompt textarea plus Provider/Model/Effort selects (Default = shell active settings). Tool mode shows Plugin and Tool dropdowns populated from the live catalog, then a schema-driven arg form (primitives/enums as native inputs, object/array as JSON textareas); a JSON fallback textarea appears when no schema is available. An optional 'Emit on complete' section allows configuring an onComplete emit event type and payload for soft chains. The Edit button on a row opens this modal prefilled and saves via job.update. The dialog supports Escape and overlay click to close, and returns focus to the trigger on close.

- **Name** (`#job-field-name`):
  - Section: Job form
  - Type: text input
  - Action: Job display name.

- **Trigger kind** (`#job-field-trigger`):
  - Section: Job form
  - Type: select
  - Action: Picker for schedule (time-based) vs event (plugin-triggered). Toggles schedule/event field visibility.

- **Schedule fields** (`#job-schedule-fields`):
  - Section: Job form
  - Type: region
  - Action: Container for schedule input, shown when trigger kind is 'schedule'.

- **Schedule** (`#job-field-schedule`):
  - Section: Job form
  - Type: text input
  - Action: Schedule expression (every 30m / 2h / 1d / cron / ISO). Validated on blur. Visible only when trigger kind is 'schedule'.

- **Schedule help** (`#job-schedule-help`):
  - Section: Job form
  - Type: status text
  - Action: Live validation result for the schedule expression.

- **Event fields** (`#job-event-fields`):
  - Section: Job form
  - Type: region
  - Action: Container for event trigger inputs (pattern, plugin, throttle, max fires), shown when trigger kind is 'event'.

- **Event pattern** (`#job-field-event-pattern`):
  - Section: Job form
  - Type: text input
  - Action: Glob pattern matched against automation event types (e.g. mail.new, files.*).

- **Event help** (`#job-event-help`):
  - Section: Job form
  - Type: status text
  - Action: Shows available event types from the selected plugin's manifest automation.emits.

- **Event plugin scope** (`#job-field-event-plugin`):
  - Section: Job form
  - Type: select
  - Action: Optional plugin scope for the event trigger (Any plugin or a specific pluginId).

- **Throttle (ms)** (`#job-field-throttle-ms`):
  - Section: Job form
  - Type: number input
  - Action: Minimum gap between fires for this event job (coalesce burst events).

- **Max fires/hour** (`#job-field-max-fires`):
  - Section: Job form
  - Type: number input
  - Action: Hard cap on dispatches per rolling hour for this event job.

- **Mode** (`#job-field-mode`):
  - Section: Job form
  - Type: select
  - Action: Switches between agent prompt and plugin tool call mode.

- **Mode help** (`#job-mode-help`):
  - Section: Job form
  - Type: status text
  - Action: One-line description of the selected mode (agent = uses AI model; tool = no model, direct plugin call).

- **Prompt** (`#job-field-prompt`):
  - Section: Job form
  - Type: textarea
  - Action: Agent prompt for agent-mode jobs. Required when mode is agent.

- **Provider** (`#job-field-provider`):
  - Section: Job form
  - Type: select
  - Action: AI provider for agent-mode jobs. Default = shell active settings provider.

- **Model** (`#job-field-model`):
  - Section: Job form
  - Type: select
  - Action: AI model for agent-mode jobs. Default = shell active settings model. Options filtered by selected provider.

- **Effort** (`#job-field-effort`):
  - Section: Job form
  - Type: select
  - Action: Reasoning effort for agent-mode jobs. Only shown when the selected model advertises supported efforts.

- **Plugin** (`#job-field-plugin-id`):
  - Section: Job form
  - Type: select
  - Action: Plugin dropdown for tool-mode jobs, populated from the live plugin catalog. Starts the plugin if stopped.

- **Tool** (`#job-field-tool-name`):
  - Section: Job form
  - Type: select
  - Action: Tool dropdown for tool-mode jobs, populated after a plugin is selected.

- **Schema arg form** (`#job-tool-schema-form`):
  - Section: Job form
  - Type: container
  - Action: Dynamic form fields generated from the selected tool's inputSchema. Primitives and enums render as native inputs; object/array properties render as JSON textareas.

- **Args JSON** (`#job-field-args`):
  - Section: Job form
  - Type: textarea
  - Action: JSON arguments for the tool call.

- **Repeat count** (`#job-field-repeat`):
  - Section: Job form
  - Type: number input
  - Action: How many times to fire (blank = repeat forever).

- **`#job-field-oncomplete-type`** (missing map entry)

- **`#job-field-oncomplete-payload`** (missing map entry)

- **Save** (`#job-modal-save`):
  - Section: Job form
  - Type: button
  - Action: Creates the job via job.add and closes the modal.
