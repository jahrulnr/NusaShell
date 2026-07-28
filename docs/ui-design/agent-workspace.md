# Agent workspace visual contract

The Agent workspace follows the launcher language: dark graphite surfaces,
compact blue accents, restrained borders, and information-dense controls. It
must remain usable at Electron's 900 × 700 minimum window size.

## Agent

At wide widths, the conversation list and active conversation form two columns.
Below 1050 px they stack so the composer, model picker, and Stop/Send actions
never overflow horizontally. The left column contains deletable conversations;
the MCP catalog and MCP context are intentionally absent because the agent
discovers and controls MCP capabilities through progressive tools.

The model picker searches the complete imported catalog and identifies each
model's provider, context window, supported input modes, tool compatibility, and
reasoning effort. Attachments are accepted only when the selected model and
provider can handle them. A running turn streams into one pending assistant
message and exposes a Stop action. Completed assistant messages render
GitHub-Flavored Markdown, including tables and code blocks; user, pending, and
error messages remain plain text. Every scrollable renderer surface uses the
shared graphite scrollbar with a blue hover thumb; native light scrollbars are
not part of the NusaShell UI. The Agent view fills the shell viewport: the
conversation list and composer remain fixed while only the message thread
scrolls. Composer attach/stop/send actions are compact icon buttons; launcher
search filters installed plugins by name, ID, or description, and plugin or
editable-field right-clicks open the appropriate shell-owned context menu. The
composer status summarizes estimated `used/max` context tokens instead of
repeating the selected model name.

## AI providers

Providers use a responsive card grid rather than a shared settings form. Every
card shows identity, API family, configuration status, enablement, model count,
and actions. A provider is green only after its required connection fields are
configured; otherwise its status is grey.

Selecting a built-in card opens a titled details modal for that provider. It
does not show a provider-type selector. `+ Custom provider` opens the extended
form for name, stable ID, API family, connection details, retry tuning, and
enablement. Both forms have explicit close and cancel controls.

Provider details expose connection metadata, edit/delete actions, model import,
and the imported model list. A default model is optional. Imported or manually
added models become available to the Agent model picker immediately.
