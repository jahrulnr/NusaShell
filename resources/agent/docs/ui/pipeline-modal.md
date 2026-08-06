# Pipeline editor modal

Form to create or edit a multi-step pipeline with an event trigger, step list, and per-step configuration. Schedule is not offered in Beta.

**How to open:** Click the + New pipeline button in the Pipelines view, or Edit on a pipeline card.

## Pipeline form

The modal collects a pipeline name, optional description, event trigger (pattern and optional plugin scope), and a list of steps. Each step has an id, name, action type (agent prompt or plugin tool call), optional dependsOn (multi-select of other step IDs), and optional outputKey. Steps can be added or removed dynamically.

- **Save** (`#pipeline-modal-save`):
  - Section: Pipeline form
  - Type: button
  - Action: Creates or updates the pipeline via pipeline.add/update and closes the modal.

- **Pipeline name** (`#pipeline-field-name`):
  - Section: Pipeline form
  - Type: text input
  - Action: Sets the pipeline name.

- **`#pipeline-field-description`** (missing map entry)

- **Trigger type** (`#pipeline-field-trigger-kind`):
  - Section: Pipeline form
  - Type: select
  - Action: Event-only in Beta; schedule option is reserved and disabled.

- **`#pipeline-field-schedule`** (missing map entry)

- **`#pipeline-field-event-pattern`** (missing map entry)

- **`#pipeline-field-event-plugin`** (missing map entry)

- **Add step** (`#pipeline-add-step-btn`):
  - Section: Pipeline form
  - Type: button
  - Action: Adds a new step row to the pipeline.

- **Steps list** (`#pipeline-steps-list`):
  - Section: Pipeline form
  - Type: list
  - Action: Renders editable step rows with id, name, action, dependsOn, outputKey.
