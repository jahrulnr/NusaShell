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
scrolls internally. Its compact footer keeps attachment, model, and workspace
context in one flexible cluster, with context usage and turn actions aligned as
a separate trailing cluster. Long model and workspace labels truncate instead
of crowding actions; the full model label remains available as a tooltip. At
very narrow widths the action cluster moves to a second row.

### Subagent activity

When the parent agent delegates work through the `subagent` tool, an in-chat
Subagent run card appears in the message thread. The card head shows the provider
badge, run title, and a live status chip; clicking it opens the full Subagent
side pane, a right-hand drawer that renders the complete live stream with
sanitized Markdown text bubbles, collapsed reasoning disclosures, expandable
tool terminals, plan steps, and permission/ask cards. While the run is active,
the card also shows a compact mini activity stream below the head — roughly ten
monospace rows that mirror the side pane as one-line rows (Thinking, text
snippets, tool calls with success/failure marks, and plan progress). The mini
stream auto-scrolls to the bottom while the user is pinned to the tail; scrolling
up pauses stickiness until the user returns to the bottom, so the user can
inspect earlier activity without the log jumping. Clicks inside the mini stream
do not toggle the drawer. When the run ends, the mini stream freezes in place
until the parent turn seals the tool card; the side pane keeps the full frozen
history for review. The two surfaces share one event fan-out: the side pane is
the authoritative full log, the in-chat mini stream is a bounded tail preview.

### Agent Canvas

A shell-owned preview pane sits beside the conversation as a third grid column
when open. It is hidden until a Sidebar or Preview action promotes a canvas
fence, or until a conversation with a persisted `activeCanvasArtifactId` is
reopened. Completed assistant messages auto-render `svg` and `mermaid` fences
inline in a light, bounded container above the source block; mermaid is
lazy-loaded via dynamic `import()` only when a mermaid fence is present, and
runs with `securityLevel: 'strict'` so it compiles to static SVG. Before render,
flowchart sources get a deterministic heal pass that quotes unquoted edge labels
containing risky shape tokens (`[]`, `()`, `{}`, `#`) or HTML so common agent
slips still draw; Show source keeps the original fence. Each canvas
fence (`html`/`htm`/`svg`/`mermaid`) gains a Sidebar action; `html` fences also
gain a Preview action that opens the pane with a sandboxed iframe
(`sandbox="allow-scripts"`, no `allow-same-origin`, CSP with an empty external
allowlist in v1). The pane chrome is restrained instrument-workbench: a
monospace kind badge (HTML/SVG/MERMAID), a title, and compact Refresh,
Download source, and Close icon buttons. The body is a light surface so
rendered HTML/SVG read as authored content, not shell chrome. At the desktop
minimum width the pane becomes a full-bleed overlay. Artifacts persist per
conversation (max 20 and 3 MB total, oldest non-active evicted) and survive
compaction; switching conversations hides the pane and reopens it only for the
newly selected conversation's active artifact. A Settings toggle disables the
canvas entirely so fences stay as source code blocks. The canvas is shell
chrome, not a plugin window; it does not expand the deferred host-isolation or
MCP/AI behavioral-security scope.

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
