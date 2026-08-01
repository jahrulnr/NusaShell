# Agent workspace visual contract

The Agent workspace follows the instrument-workbench language defined in
[`shell-workbench.md`](./shell-workbench.md): dark graphite surfaces, phosphor
only for selected/live/action state, restrained borders, and information-dense
controls. It must remain usable at Electron's 900 × 700 minimum window size.

## Agent

At wide widths, the fixed conversation rail and active conversation form two
columns in a full-bleed workbench. The message runway consumes the available
width, while the composer is a raised command dock instead of another narrow
panel inside a card. At the desktop minimum width the shell sidebar becomes
icon-only so the conversation remains usable; at substantially smaller browser
preview widths the conversation rail may hide. The left rail contains deletable conversations;
the MCP catalog and MCP context are intentionally absent because the agent
discovers and controls MCP capabilities through progressive tools.

The model picker searches the complete imported catalog and identifies each
model's provider, context window, supported input modes, tool compatibility, and
reasoning effort. Images are sent optimistically unless runtime settings disable
them and retry once without pixels after a provider 4xx; text attachments become
named text context for any chat model. A running turn streams into one pending assistant
message and exposes a Stop action. Completed assistant messages render
GitHub-Flavored Markdown, including tables and code blocks, on an editorial
full-width runway rather than inside a heavy chat bubble. User turns remain
compact right-aligned cards with persisted attachment previews, timestamps, and
a copy action. When a provider returns reasoning or a thinking summary, it is
persisted with the assistant message and appears before the answer in a muted,
collapsed `Thinking` disclosure. Opening it reveals sanitized Markdown; models
that return no reasoning do not leave an empty placeholder. Completed tool executions appear before the answer in a
collapsible vertical activity timeline; the timeline reports only persisted
tool names and success/failure results and must not imply live progress that the
backend has not emitted. Pending and error messages remain plain text. Every scrollable renderer surface uses the
shared graphite scrollbar with a blue hover thumb; native light scrollbars are
not part of the NusaShell UI. The Agent view fills the shell viewport: the
conversation list and composer remain fixed while only the message thread
scrolls. Composer attach/stop/send actions are compact icon buttons; launcher
search lives on Home and filters installed plugins by name, ID, or description.
Plugin or editable-field right-clicks open the appropriate shell-owned context
menu; edit actions use Electron's clipboard bridge. The
composer status summarizes estimated `used/max` context tokens instead of
repeating the selected model name. The badge reflects approximate *current
prompt window* fill (from `agent.context` estimates and local `chars/4`
estimates), not cumulative billed input tokens summed across tool rounds. The
composer starts as a single text row,
grows with wrapped or explicit lines, and caps at ten rows before its textarea
scrolls internally; the controls footer stays fixed below it.

## AI providers

Providers use a responsive card grid rather than a shared settings form. Every
card shows identity, API family, configuration status, enablement, model count,
and actions. A provider is green only after its required connection fields are
configured; otherwise its status is grey.

Selecting a built-in card opens a titled details modal for that provider. It
does not show an internal provider-type selector, but it always exposes the API
mode: OpenAI-compatible providers can choose Chat Completions or Responses,
while native Anthropic uses Messages. `+ Custom provider` opens the extended
form for name, stable ID, API mode, connection details, retry tuning, and
enablement. Both forms have explicit close and cancel controls.

Provider details expose connection metadata, edit/delete actions, model import,
and the imported model list. A default model is optional. Imported or manually
added models become available to the Agent model picker immediately.

## Logs

The Logs view uses the same full-height workspace principle as Agent. Its
header and source filters stay fixed in the content area, while the bordered
log card expands through the remaining viewport and owns the vertical scroll.
Do not cap the card at a viewport percentage or fixed pixel height.
