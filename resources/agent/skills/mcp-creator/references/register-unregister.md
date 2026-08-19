# Register and unregister

`mcp_register` requires `source`, an absolute staging-folder path containing a
valid `manifest.json` and all declared files. The source must stay outside the
installed plugins directory. Registration validates the source, copies it into
the installed destination derived from the manifest id, and replaces an existing
plugin with the same id.

Call `mcp_list` first. If the id already exists, use `ask_question` to confirm
replacement before `mcp_register`. The runtime tool does not add its own
confirmation gate, so do not invoke replacement from an unattended job.

`mcp_unregister` requires `id` and permanently deletes that plugin's installed
folder. Use `mcp_disable` when the plugin only needs to stop. Before permanent
removal, use `ask_question` to confirm and never invoke it from an unattended
job. Bundled plugins must not be removed.
