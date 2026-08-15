# Skills

Author and manage markdown instruction packs the agent can load on demand via `skill_run`.

**How to open:** Click the Skills item in the left sidebar.

## Header

View title and a New skill button.

- **New skill** (`#new-skill-btn`):
  - Section: Skills
  - Type: button
  - Action: Creates a new skill and opens the editor.

## Catalog

Searchable list of installed skills. The count reflects persisted skills.

- **Skills count** (`#skills-count`):
  - Section: Skills
  - Type: text

- **Skills search** (`#skills-search`):
  - Section: Skills
  - Type: search

- **Skills catalog** (`#skills-list`):
  - Section: Skills
  - Type: list

## Editor

Edit the selected skill's name, description, and markdown content. Run opens the skill in a new Agent conversation as its system context. Save and Delete persist changes.

- **Editor title** (`#skill-editor-title`):
  - Section: Skills
  - Type: text

- **Editor meta** (`#skill-editor-meta`):
  - Section: Skills
  - Type: text

- **Run skill** (`#skill-run-btn`):
  - Section: Skills
  - Type: button
  - Action: Opens the skill in a new Agent conversation as its system context.

- **Save skill** (`#skill-save-btn`):
  - Section: Skills
  - Type: button
  - Action: Persists the edited skill.

- **Delete skill** (`#skill-delete-btn`):
  - Section: Skills
  - Type: button
  - Action: Deletes the selected skill.

- **Editor form** (`#skill-editor-form`):
  - Section: Skills
  - Type: container

- **Skill name** (`#skill-name`):
  - Section: Skills
  - Type: input

- **Skill description** (`#skill-description`):
  - Section: Skills
  - Type: input

- **Skill markdown** (`#skill-content`):
  - Section: Skills
  - Type: textarea
  - Notes: Injected verbatim into the agent context when loaded.

- **Editor empty state** (`#skill-editor-empty`):
  - Section: Skills
  - Type: text
