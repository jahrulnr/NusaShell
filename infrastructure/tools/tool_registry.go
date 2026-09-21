package tools

import (
	"context"

	"nusashell/application"
)

// toolHandler executes one tool call from its raw JSON arguments.
type toolHandler func(ctx context.Context, argsJSON []byte) (string, error)

// toolEntry is one registry row: the advertised definition plus the handler
// that executes it, so advertisement and execution cannot drift. A nil
// handler means the tool is advertised here but executed by the agent layer
// (read_media and generate_media — see
// application/agent/agent_round_tools.go). A nil enabled predicate means
// always advertised; a predicate returning false keeps the entry executable
// but unadvertised (legacy names). describe, when set, supplies the
// advertised Description at ListTools time for entries whose text depends
// on runtime configuration; it runs only after enabled passes so Execute
// never pays for it.
type toolEntry struct {
	info     application.ToolInfo
	handler  toolHandler
	enabled  func(t *Toolbox) bool
	describe func(t *Toolbox) string
}

// neverAdvertised marks execute-only entries (legacy tool names): Execute
// can route them but ListTools never advertises them.
func neverAdvertised(*Toolbox) bool { return false }

// toolRegistry assembles the ordered registry. Slice order IS the
// advertised roster order (pinned by TestListToolsRosterGolden): typed
// tools in their historical order, then the conditional advertisements
// (subagent, web_answer, generate_media), then the execute-only legacy
// names. File/exec/dispatcher tools are not here — ListTools appends them
// separately and Execute routes them in the pre-router.
func (t *Toolbox) toolRegistry() []toolEntry {
	entries := make([]toolEntry, 0, 26)
	entries = append(entries, t.todoToolEntries()...)
	entries = append(entries, t.mcpToolEntries()...)
	entries = append(entries, t.readMediaToolEntry())
	entries = append(entries, t.webToolEntries()...)
	entries = append(entries, t.subagentToolEntry())
	entries = append(entries, t.webAnswerToolEntry())
	entries = append(entries, t.generateMediaToolEntry())
	entries = append(entries, t.legacySubagentToolEntries()...)
	return entries
}
