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

- **Pin / Unpin skill** (`#skill-pin-btn`):
  - Section: File editor
  - Type: button
  - Action: Toggles the pinned state of the selected skill. Pinned skills are excluded from curator archival.

## Curator

Shows the skill curator's last-run timestamp and running state. Run executes the curator to transition stale skills to archived; Dry-run previews changes without applying them.

- **Curator status** (`#skills-curator-status`):
  - Section: Curator
  - Type: status text
  - Action: Shows the curator's last-run timestamp and whether it is currently running.

- **Run curator** (`#skills-curator-run`):
  - Section: Curator
  - Type: button
  - Action: Executes the skill curator to transition stale skills to archived based on usage and provenance.

- **Dry-run curator** (`#skills-curator-dry-run`):
  - Section: Curator
  - Type: button
  - Action: Previews curator changes without applying them.

## Pending writes

Lists skill writes authored by the agent that await user approval. Each row shows the action, skill id, and file path, with Approve and Reject buttons.

- **Pending count** (`#skills-pending-count`):
  - Section: Pending writes
  - Type: status text
  - Action: Shows the number of pending skill writes awaiting approval.

- **Refresh pending** (`#skills-pending-refresh`):
  - Section: Pending writes
  - Type: button
  - Action: Reloads the list of pending skill writes from the backend.

- **Pending writes list** (`#skills-pending-list`):
  - Section: Pending writes
  - Type: list
  - Action: Lists agent-authored skill writes awaiting user approval, with Approve and Reject buttons per item.

## Archived skills

Lists skills the curator has archived. Each row shows the skill name and description with a Restore button.

- **Archived count** (`#skills-archived-count`):
  - Section: Archived skills
  - Type: status text
  - Action: Shows the number of archived skills.

- **Refresh archived** (`#skills-archived-refresh`):
  - Section: Archived skills
  - Type: button
  - Action: Reloads the list of archived skills from the backend.

- **Archived skills list** (`#skills-archived-list`):
  - Section: Archived skills
  - Type: list
  - Action: Lists skills the curator has archived, with a Restore button per item.
