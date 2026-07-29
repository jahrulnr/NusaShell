# Add Plugin Modal

Install a plugin from a remote URL or a locally selected folder/archive.

**How to open:** Click the Add Plugin button in the sidebar or the Home empty-state prompt.

## Install options

Install from a URL, choose a local plugin folder with the native directory picker, or choose a zip/tar.gz archive with the native file picker. The selected path is read-only.

- **Add Plugin modal** (`#add-plugin-modal`):
  - Section: Install options
  - Type: modal
  - Action: Modal overlay containing URL and local install options. Click the backdrop to close.

- **Close modal** (`#modal-close`):
  - Section: Install options
  - Type: icon button
  - Action: Closes the Add Plugin modal.

- **Install from URL** (`#install-url-input`):
  - Section: Install options
  - Type: text input
  - Action: Accepts a URL to a plugin zip/archive. Press Enter or click Install to submit.
  - Related: Install (URL) (`#install-url-btn`)

- **Install (URL)** (`#install-url-btn`):
  - Section: Install options
  - Type: button
  - Action: Installs the plugin from the URL in the adjacent input.
  - Related: Install from URL (`#install-url-input`)

- **Selected local plugin** (`#install-local-input`):
  - Section: Install options
  - Type: read-only text input
  - Action: Displays the folder or archive returned by the native picker; paths cannot be typed manually.
  - Related: Choose folder (`#pick-local-folder-btn`), Choose archive (`#pick-local-archive-btn`), Install (local) (`#install-local-btn`)

- **Choose folder** (`#pick-local-folder-btn`):
  - Section: Install options
  - Type: button
  - Action: Opens the operating system directory picker and stores the selected plugin folder in the read-only path field.
  - Related: Selected local plugin (`#install-local-input`), Install (local) (`#install-local-btn`)

- **Choose archive** (`#pick-local-archive-btn`):
  - Section: Install options
  - Type: button
  - Action: Opens the operating system file picker for zip, tar.gz, or tgz plugin archives.
  - Related: Selected local plugin (`#install-local-input`), Install (local) (`#install-local-btn`)

- **Install (local)** (`#install-local-btn`):
  - Section: Install options
  - Type: button
  - Action: Installs the folder or archive selected by the native picker.
  - Related: Selected local plugin (`#install-local-input`)

## Install status

Displays progress, success, or error text after an install attempt. The modal closes automatically on success.

- **Install status** (`#install-status`):
  - Section: Install status
  - Type: status text
  - Action: Shows installing, success, or error messages. The modal closes automatically after a successful install.
