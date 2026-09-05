# Skills

Browse and read installed skill packages (SKILL.md + support files). Skills come from three owners: user, builtin, and plugin:<id>. Lower-priority owners (plugin) are shadowed when a higher-priority owner (user/builtin) defines the same skill ID.

**How to open:** Click the Skills item in the left sidebar.

## Header

View title and an Install skill button for .skill or .zip archives.

- **Install skill button** (`#install-skill-btn`):
  - Section: Skills header
  - Type: button
  - Notes: Opens file picker to install a .skill or .zip archive.

## Catalog

Searchable list of installed skills. Each row shows the skill name, a status badge (experimental/validated/trusted/…), an owner badge (user/builtin/plugin:<id>), version, and last-updated time. Shadowed skills are dimmed. The count reflects persisted skills.

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

Read-only file viewer for the selected skill plus lifecycle controls. Shows the skill name, description, owner, status badge, and Version/ActiveVersion. Experimental and validated skills can be promoted (skills.promote). Rollback picks a version from the custom select and calls skills.rollback. Learned and user-owned skills can be deleted (skills.delete) after a confirm dialog; builtin and plugin-owned skills stay hidden from Delete. User-owned trusted skills can still be saved with skills.save; learned experimental skills are not overwritten in place from this viewer. Plugin-owned skills are read-only. On phones the catalog, file tree, and file viewer stack vertically inside one scrollable workspace so every file remains reachable.

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

- **Skill status** (`#skill-status-badge`):
  - Section: Skills
  - Type: text
  - Notes: Promotion status: candidate, experimental, validated, trusted, deprecated, or retired.

- **Skill version** (`#skill-version-meta`):
  - Section: Skills
  - Type: text
  - Notes: Shows Version and ActiveVersion for the selected skill.

- **Promote skill** (`#skills-promote`):
  - Section: Skills
  - Type: button
  - Action: Promotes an experimental or validated skill via skills.promote.

- **Rollback version** (`#skills-rollback-version`):
  - Section: Skills
  - Type: select
  - Notes: Custom select of skill versions used by Rollback.

- **Rollback skill** (`#skills-rollback`):
  - Section: Skills
  - Type: button
  - Action: Rolls the selected skill back to the chosen version via skills.rollback.

- **Delete skill** (`#skills-delete`):
  - Section: Skills
  - Type: button
  - Action: Deletes a learned or user-owned skill after confirmation via skills.delete. Hidden for builtin and plugin-owned skills.

- **Skill file viewer** (`#skill-file-viewer`):
  - Section: Skills editor pane
  - Type: panel
  - Notes: Shows file content when a file is clicked in the skill tree.

- **`#skill-files-tree`** (missing map entry)

- **Skill file content** (`#skill-file-content`):
  - Section: Skills file viewer
  - Type: pre
  - Notes: Pre-formatted text content of the opened skill file.
