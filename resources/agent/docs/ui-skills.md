# Skills

Browse and read installed skill packages (SKILL.md + support files). Skills come from three owners: user, builtin, and plugin:<id>. Lower-priority owners (plugin) are shadowed when a higher-priority owner (user/builtin) defines the same skill ID.

**How to open:** Click the Skills item in the left sidebar.

## Header

View title and a New skill button.

- **`#new-skill-btn`** (missing map entry)

## Catalog

Searchable list of installed skills. Each row shows the skill name, an owner badge (user/builtin/plugin:<id>), and last-updated time. Shadowed skills are dimmed. The count reflects persisted skills.

- **Skills count** (`#skills-count`):
  - Section: Skills
  - Type: text

- **Skills search** (`#skills-search`):
  - Section: Skills
  - Type: search

- **Skills catalog** (`#skills-list`):
  - Section: Skills
  - Type: list

## Detail

Read-only viewer for the selected skill. Shows the skill name, description, and owner, plus a file tree (SKILL.md + support files) on the left and the selected file's content on the right. Plugin-owned skills are read-only. On phones the catalog, file tree, and file viewer stack vertically inside one scrollable workspace so every file remains reachable.

- **Editor title** (`#skill-editor-title`):
  - Section: Skills
  - Type: text
  - Notes: Shows the selected skill name or 'No file selected'.

- **Editor meta** (`#skill-editor-meta`):
  - Section: Skills
  - Type: text

- **Editor empty state** (`#skill-editor-empty`):
  - Section: Skills
  - Type: text
  - Notes: Empty state shown when no file is selected. Prompts user to pick a file from the skill tree.

- **Skill file viewer** (`#skill-file-viewer`):
  - Section: Skills editor pane
  - Type: panel
  - Notes: Shows file content when a file is clicked in the skill tree.

- **`#skill-files-tree`** (missing map entry)

- **Skill file content** (`#skill-file-content`):
  - Section: Skills file viewer
  - Type: pre
  - Notes: Pre-formatted text content of the opened skill file.
