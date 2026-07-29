# Skills

Managed file workspace for installing, inspecting, editing, and deleting agent skill packages.

**How to open:** Click the Skills item in the left sidebar.

## Skill library

Lists installed skills with their descriptions and file counts. Search filters the local library, while Install package opens a native picker for .skill and .zip archives.

- **Install package** (`#skills-install-btn`):
  - Section: Skill library
  - Type: button
  - Action: Opens a native file picker and installs a .skill or .zip package into NusaShell's managed skills directory.

- **Search skills** (`#skills-search`):
  - Section: Skill library
  - Type: search input
  - Action: Filters installed skills by name and description.

- **Installed skills** (`#skills-list`):
  - Section: Skill library
  - Type: listbox
  - Action: Lists installed skills and selects the package shown in the file tree.

- **Skills empty state** (`#skills-empty`):
  - Section: Skill library
  - Type: status
  - Action: Explains how to install the first skill, or reports when the desktop bridge is unavailable.

## Package files

Shows the selected skill package as a compact file tree. Text and binary entries are visually distinguished, and selecting a file opens it in the adjacent viewer.

- **Skill file tree** (`#skills-file-tree`):
  - Section: Package files
  - Type: tree
  - Action: Lists directories and files within the selected managed skill package.

- **Skill file count** (`#skills-file-count`):
  - Section: Package files
  - Type: status text
  - Action: Shows the number of files in the selected skill package.

## File editor

UTF-8 text files can be edited and saved in place. Binary or large files expose metadata without rendering their contents. Delete skill removes the entire managed package after confirmation.

- **Selected file** (`#skill-file-title`):
  - Section: File editor
  - Type: heading
  - Action: Shows the relative path of the file open in the viewer.

- **File metadata** (`#skill-file-meta`):
  - Section: File editor
  - Type: status text
  - Action: Shows file size and whether the selected file is editable UTF-8 text.

- **Skill text editor** (`#skill-editor`):
  - Section: File editor
  - Type: textarea
  - Action: Edits the selected UTF-8 text file inside the managed skill.

- **Binary file viewer** (`#skill-binary-view`):
  - Section: File editor
  - Type: read-only viewer
  - Action: Shows safe metadata instead of rendering binary or oversized file contents.

- **Save file** (`#skill-save-btn`):
  - Section: File editor
  - Type: button
  - Action: Writes changes to the selected text file inside the managed skill.

- **Delete skill** (`#skill-delete-btn`):
  - Section: File editor
  - Type: button
  - Action: Deletes the selected managed skill package after confirmation.
