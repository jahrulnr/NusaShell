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

- **Custom MCP** (`#custom-mcp-tab`):
  - Section: Modal tabs
  - Type: tab
  - Action: Shows the native MCP form.

- **NusaShell Plugin** (`#nusashell-plugin-tab`):
  - Section: Modal tabs
  - Type: tab
  - Action: Shows URL, folder, and archive install controls.

- **MCP name** (`#native-mcp-name`):
  - Section: Custom MCP
  - Type: text input
  - Action: Sets the display name for the native MCP.

- **MCP id** (`#native-mcp-id`):
  - Section: Custom MCP
  - Type: text input
  - Action: Sets the publisher.name plugin id.

- **Transport** (`#native-mcp-transport`):
  - Section: Custom MCP
  - Type: select
  - Action: Chooses stdio, HTTP, or SSE.

- **Command** (`#native-mcp-command`):
  - Section: Custom MCP
  - Type: text input
  - Action: Sets the stdio command.

- **Arguments** (`#native-mcp-args`):
  - Section: Custom MCP
  - Type: textarea
  - Action: Sets one stdio argument per line.

- **Server URL** (`#native-mcp-url`):
  - Section: Custom MCP
  - Type: text input
  - Action: Sets the HTTP/SSE server URL.

- **Environment JSON** (`#native-mcp-env`):
  - Section: Custom MCP
  - Type: textarea
  - Action: Sets optional stdio environment variables as JSON.

- **Headers JSON** (`#native-mcp-headers`):
  - Section: Custom MCP
  - Type: textarea
  - Action: Sets optional HTTP/SSE headers as JSON.

- **Import JSON** (`#native-mcp-import`):
  - Section: Custom MCP
  - Type: textarea
  - Action: Accepts a Cursor-style single-server mcpServers JSON object.

- **Fill from JSON** (`#native-mcp-import-btn`):
  - Section: Custom MCP
  - Type: button
  - Action: Parses JSON into the form fields.

- **Save MCP** (`#native-mcp-save`):
  - Section: Custom MCP
  - Type: button
  - Action: Writes or updates the native MCP manifest and refreshes inventory.

- **MCP status** (`#native-mcp-status`):
  - Section: Custom MCP
  - Type: status text
  - Action: Shows native MCP save or validation errors.

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
