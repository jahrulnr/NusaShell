# Skills

The skills library: installable skill packages browsable, searched, inspected, edited, promoted, and rolled back from one workspace; learned and user-owned packages can also be deleted after confirmation.

**How to open:** Click the Skills item in the left rail, or open #skills.

## Header

Install button chooses a .skill or .zip package from disk.

- **Install skill** (`#skills-install`):
  - Section: Skills header
  - Type: button
  - Notes: Opens file picker to install a .skill or .zip archive.

- **Skill package input** (`#skill-file-input`):
  - Type: input
  - Notes: Hidden file picker accepting .skill and .zip packages.

## Catalog

Searchable, owner/category-labelled list of installed skills with sync state and result counts; two persistent splitters divide the workspace index and the sheet file nav.

The mobile toolbar keeps its full content height; status and Install actions wrap as needed and stay above the library list.

- **Skills count** (`#skills-count`):
  - Type: text

- **Skills search** (`#skills-search`):
  - Type: search

- **Skills catalog** (`#skills-list`):
  - Type: list

- **Workspace index splitter** (`skills-splitter-1`):
  - Type: separator
  - Action: Drags the index column between 150-450px; keyboard arrows step 16px; persists per session.

- **Sheet nav splitter** (`skills-splitter-2`):
  - Type: separator
  - Action: Drags the sheet file-nav column between 150-500px; drops out below 720px.

## Detail

Inspector shows owner, category, lifecycle state, usage stats, package files with a tree reader, and actions behind a menu. Delete is available for learned, user-owned, and built-in skills after confirmation; deleting a built-in records its removal so NusaShell does not automatically reinstall it. Plugin-owned skills point to uninstalling their owner, and Rollback remains available in the action menu. At widths through 720px, selecting a skill opens its detail page; Back preserves the selected package and search, while package files and their preview use separate steps.

- **Back to skills** (`#skills-mobile-back`):
  - Type: button
  - Action: Returns to the filtered skill list while keeping the selected package open.

- **Editor title** (`#skill-editor-title`):
  - Type: text
  - Notes: Shows the selected skill name or 'No file selected'.

- **Skill owner** (`#skill-detail-owner`):
  - Type: text
  - Notes: Owner label in the detail eyebrow.

- **Skill category** (`#skill-detail-category`):
  - Type: text
  - Notes: Category label in the detail eyebrow.

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
  - Action: Promotes an experimental or validated skill via skills.promote.

- **Rollback version** (`#skills-rollback-version`):
  - Type: select
  - Notes: Custom select of skill versions used by Rollback.

- **Rollback skill** (`#skills-rollback`):
  - Type: button
  - Action: Rolls the selected skill back to the chosen version via skills.rollback.

- **Delete skill** (`#skills-delete`):
  - Type: button
  - Action: Deletes a learned or user-owned skill after confirmation via skills.delete. Hidden for builtin and plugin-owned skills.

- **Back to package files** (`#skill-file-back`):
  - Type: button
  - Action: Returns from the file preview to the selected package tree without closing the package.

- **Skill file viewer** (`#skill-file-viewer`):
  - Section: Skills editor pane
  - Type: panel
  - Notes: Shows file content when a file is clicked in the skill tree.

- **Package file tree** (`#skill-files-tree`):
  - Type: list
  - Notes: Files inside the selected skill package; each row opens the reader.

- **Skill file content** (`#skill-file-content`):
  - Section: Skills file viewer
  - Type: pre
  - Notes: Pre-formatted text content of the opened skill file.

- **Skill actions menu** (`#skill-actions-trigger`):
  - Type: button
  - Action: Opens the promote/rollback/delete action menu.

- **Skill actions menu list** (`#skill-actions-menu`):
  - Type: menu

- **Skill ownership guidance** (`#skill-actions-guidance`):
  - Type: status
  - Notes: Explains when deletion is unavailable for built-in or plugin-owned skills.
