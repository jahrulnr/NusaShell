# Skills

Browse and read installed skill packages (SKILL.md + support files). Skills come from four owners: user, builtin, learned (saved by the agent or background learner), and plugin:<id>. Lower-priority owners are shadowed when a higher-priority owner defines the same skill ID (user > builtin > plugin > learned).

**How to open:** Click the Skills item in the left sidebar.

## Header

View title and an Install skill button for .skill or .zip archives.

- **Install skill button** (`#install-skill-btn`):
  - Section: Skills header
  - Type: button
  - Notes: Opens file picker to install a .skill or .zip archive.

- **Skill file input** (`#skill-file-input`):
  - Section: Skills header
  - Type: file-input
  - Notes: Hidden file input accepting .skill and .zip files for skill installation.

## Catalog

Searchable list of installed skills. Each row shows the skill name, a status badge (experimental/validated/trusted/…), an owner badge shown only for non-user owners (builtin, learned, or the plugin name for plugin:<id> rows; user-owned rows render no owner badge), version, and last-updated time. Shadowed skills are dimmed. The count reflects persisted skills.

- **Skills count** (`#skills-count`):
  - Type: text

- **Skills search** (`#skills-search`):
  - Type: search

- **Skills catalog** (`#skills-list`):
  - Type: list

## Detail

Read-only file viewer for the selected skill plus lifecycle controls. Shows the skill name, description, owner, status badge, and Version/ActiveVersion. Experimental and validated skills can be promoted. Rollback picks a version from the custom select. Learned and user-owned skills can be deleted after a confirm dialog; builtin and plugin-owned skills stay hidden from Delete. The viewer never edits file content — changes come from the agent's skills tool, which creates learned skills and grows them as new versions rather than overwriting in place. Plugin-owned skills are read-only. On phones the catalog, file tree, and file viewer stack vertically inside one scrollable workspace so every file remains reachable.

- **Editor title** (`#skill-editor-title`):
  - Type: text
  - Notes: Shows the selected skill name or 'No file selected'.

- **Editor meta** (`#skill-editor-meta`):
  - Type: text

- **Editor empty state** (`#skill-editor-empty`):
  - Type: text
  - Notes: Empty state shown when no file is selected. Prompts user to pick a file from the skill tree.

- **Skill status** (`#skill-status-badge`):
  - Type: text
  - Notes: Promotion status: candidate, experimental, validated, trusted, deprecated, or retired.

- **Skill version** (`#skill-version-meta`):
  - Type: text
  - Notes: Shows Version and ActiveVersion for the selected skill.

- **Promote skill** (`#skills-promote`):
  - Type: button
  - Action: Promotes an experimental or validated skill.

- **Rollback version** (`#skills-rollback-version`):
  - Type: select
  - Notes: Custom select of skill versions used by Rollback.

- **Rollback skill** (`#skills-rollback`):
  - Type: button
  - Action: Rolls the selected skill back to the chosen version.

- **Delete skill** (`#skills-delete`):
  - Type: button
  - Action: Deletes a learned or user-owned skill after confirmation. Hidden for builtin and plugin-owned skills.

- **Skills workspace grid** (`#skills-workspace`):
  - Section: Skills workspace
  - Type: container
  - Notes: 3-column grid with draggable splitters.

- **Catalog/tree splitter** (`#skills-splitter-1`):
  - Section: Skills workspace
  - Type: dragger
  - Notes: Draggable divider between Catalog and Skill tree panes. Persists width in this browser.

- **Tree/editor splitter** (`#skills-splitter-2`):
  - Section: Skills workspace
  - Type: dragger
  - Notes: Draggable divider between Skill tree and Editor panes. Persists width in this browser.

- **Skill tree meta** (`#skill-tree-meta`):
  - Section: Skills tree pane
  - Type: text
  - Notes: Shows skill name or 'No skill selected' in the tree pane header.

- **Skill files tree** (`#skill-files-tree`):
  - Section: Skills tree pane
  - Type: tree
  - Action: Lists the files inside the selected skill package; clicking a file opens it in the read-only viewer.
  - Notes: Middle pane of the skills workspace, between the catalog and the editor.

- **Skill tree empty state** (`#skill-tree-empty`):
  - Section: Skills tree pane
  - Type: panel
  - Notes: Empty state shown when no skill is selected or skill has no files.

- **Skill file viewer** (`#skill-file-viewer`):
  - Section: Skills editor pane
  - Type: panel
  - Notes: Shows file content when a file is clicked in the skill tree.

- **Skill file path** (`#skill-file-path`):
  - Section: Skills file viewer
  - Type: text
  - Notes: Path of the currently opened file.

- **Skill file size** (`#skill-file-size`):
  - Section: Skills file viewer
  - Type: text
  - Notes: Size of the currently opened file.

- **Skill file content** (`#skill-file-content`):
  - Section: Skills file viewer
  - Type: pre
  - Notes: Pre-formatted text content of the opened skill file.
