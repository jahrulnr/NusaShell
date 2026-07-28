# Add Plugin Modal

Install a plugin from a remote URL or a local path/zip.

**How to open:** Click the Add Plugin button in the sidebar or the Home empty-state prompt.

## Install options

Two input groups: install from a URL, or install from a local directory/zip path.

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

- **Install from local path** (`#install-local-input`):
  - Section: Install options
  - Type: text input
  - Action: Accepts a local directory or zip path. Press Enter or click Install to submit.
  - Related: Install (local) (`#install-local-btn`)

- **Install (local)** (`#install-local-btn`):
  - Section: Install options
  - Type: button
  - Action: Installs the plugin from the local path in the adjacent input.
  - Related: Install from local path (`#install-local-input`)

## Install status

Displays progress, success, or error text after an install attempt. The modal closes automatically on success.

- **Install status** (`#install-status`):
  - Section: Install status
  - Type: status text
  - Action: Shows installing, success, or error messages. The modal closes automatically after a successful install.
