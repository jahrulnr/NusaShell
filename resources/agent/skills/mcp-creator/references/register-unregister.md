# Register and unregister

`mcp_register` accepts either `folder` (one child name) or `path` (absolute
path) under `{userData}/plugins/`. The folder must already exist and contain a
valid manifest plus declared files. Paths outside that direct-child boundary,
missing folders, invalid manifests, and repository/plugin paths are rejected.

Registration asks for confirmation, validates the existing folder, admits it to
plugin inventory, and returns its install path. It does not download archives or
copy from arbitrary locations.

`mcp_unregister` accepts a plugin id only after the matching valid folder still
exists directly under `{userData}/plugins/`. It asks for confirmation, stops and
removes the user plugin, and refreshes inventory. Bundled plugins are protected.

Both operations are unavailable to jobs and non-interactive agent turns.
