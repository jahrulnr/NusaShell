package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Dispatcher families expose ONE advertised tool per family (skill, memory,
// docs, memory_project, automation, automation_schedule) selected by a required `op` argument. Root+op is the
// SINGLE naming layer everywhere: provider roster, execution routing,
// persisted history, hydration, and tests. There are no per-verb aliases —
// a call named like an old verb is simply an unknown tool.
//
// Adding a verb = add one op to the family spec below plus its Execute case.
// No new provider-facing schema is required (sub-linear prompt growth).
//
// See docs/design/tool-dispatchers.md.

type dispatchFamily struct {
	root    string   // advertised tool name ("memory")
	members []string // valid ops in canonical order ({"save",…})
	def     ToolInfo // advertised definition sent to providers
}

var dispatchFamilies = []dispatchFamily{
	{
		root:    "automation",
		members: []string{"run", "wait", "status", "logs", "cancel", "steer", "list", "read", "validate", "create", "enable", "disable", "delete"},
		def: ToolInfo{
			Name:        "automation",
			Description: "Manage durable automations; \"op\" selects: run {workflow_id,async?}; wait {run_id,timeout_ms?}; status {run_id}; logs {job_id,after?,limit?}; cancel {run_id}; steer {run_id,text}; list {}; read {workflow_id}; validate {yaml}; create {yaml,name?,enabled?}; enable {workflow_id}; disable {workflow_id}; delete {workflow_id} (destructive; run history is retained).",
			InputSchema: objSchema(
				pEnum("op", "Operation", "run", "wait", "status", "logs", "cancel", "steer", "list", "read", "validate", "create", "enable", "disable", "delete"),
				pStr("workflow_id", "Workflow id (run/read/enable/disable/delete)"),
				pStr("run_id", "Run id (wait/status/cancel/steer)"),
				pStr("job_id", "Job run id (logs)"),
				pStr("yaml", "Workflow YAML (validate/create)"),
				pStr("name", "Optional workflow name (create)"),
				pStr("text", "Steering instruction (steer)"),
				pBool("async", "Return after queueing instead of waiting for completion (run)"),
				pInt("timeout_ms", "Maximum wait in milliseconds (wait; default 300000, max 3600000)"),
				pInt("after", "Log sequence cursor (logs)"),
				pInt("limit", "Maximum log chunks (logs; default 200)"),
				pBool("enabled", "Enable immediately after creation (create; default true)"),
			),
		},
	},
	{
		root:    "automation_schedule",
		members: []string{"once", "every"},
		def: ToolInfo{
			Name:        "automation_schedule",
			Description: "Create durable automation schedules; \"op\" selects: once {at,yaml,name?}; every {cron? or interval?,timezone?,yaml,name?}. NusaShell owns the timer.",
			InputSchema: objSchema(
				pEnum("op", "Operation", "once", "every"),
				pStr("at", "RFC3339 fire time (once)"),
				pStr("cron", "Five-field calendar expression (every)"),
				pStr("interval", "Elapsed duration such as 1h (every)"),
				pStr("timezone", "IANA timezone (every)"),
				pStr("yaml", "Workflow YAML or jobs YAML"),
				pStr("name", "Optional workflow name"),
			),
		},
	},
	{
		root:    "skill",
		members: []string{"list", "search", "save", "delete"},
		def: ToolInfo{
			Name:        "skill",
			Description: "Skill library; \"op\" selects: list {limit?,status?} returns routable (trusted|validated) skills by default; search {query,limit?,status?} returns discovery metadata only (id/name/description/owned_by/status), never SKILL.md content — after selecting a skill, MUST read its absolute SKILL.md with file_read before applying it; save creates a learned experimental SKILL.md with {name,content,description?} (omit id and omit path), versions an existing learned experimental SKILL.md with {id,name,content,description?} (omit path; id is the folder id, which may be learned-<name>), or writes a relative support file with {name or id, path, content} (skill must already exist and be agent-mutable; never pass an absolute SKILL.md path); delete {id,owned_by?} removes a learned candidate/experimental skill. Trusted curated skills cannot be mutated or deleted by the agent. See docs(op=\"read\", id=\"skills\") for the path layout.",
			InputSchema: objSchema(
				pEnum("op", "Operation", "list", "search", "save", "delete"),
				pStr("query", "Search query (op=search)"),
				pInt("limit", "Max results (list default 100, search default 50)"),
				pStr("status", "Optional status filter (list/search). Empty = routable trusted|validated only."),
				pStr("name", "Skill name, lowercase-with-hyphens (op=save)"),
				pStr("path", "Relative support-file path inside an existing skill (op=save), e.g. references/errors.md. Omit path to create or update SKILL.md. Do not pass SKILL.md or an absolute path."),
				pStr("id", "Existing skill folder id for SKILL.md update (op=save). Omit id to create. Required when the folder id differs from name (for example learned-tool-mapping)."),
				pStr("description", "Short description up to 1024 chars (op=save SKILL.md mode)"),
				pStr("content", "SKILL.md body, or support-file bytes when path is set (op=save)"),
				pStr("owned_by", "Skill owner for delete (optional; user/learned/builtin/plugin:<id>)"),
			),
		},
	},
	{
		root:    "memory",
		members: []string{"search", "get", "list"},
		def: ToolInfo{
			Name:        "memory",
			Description: "Durable memory records (read-only); \"op\" selects: search {query,type?,status?,scope?,project?,limit?} token AND match over retrievable records (multi-word terms need not be a contiguous phrase); get {id} one record; list {type?,status?,scope?,project?,limit?} retrievable records. Agents do not write, replace, or delete memory — the learner owns writes.",
			InputSchema: objSchema(
				pEnum("op", "Operation", "search", "get", "list"),
				pStr("query", "Search query (op=search)"),
				pStr("id", "Memory record id (op=get)"),
				pStr("type", "Optional record type filter"),
				pStr("status", "Optional status filter"),
				pStr("scope", "Optional scope level filter"),
				pStr("project", "Optional project/workspace label"),
				pInt("limit", "Max results (search default 20, list default 50)"),
			),
		},
	},
	{
		root:    "docs",
		members: []string{"list", "search", "read"},
		def: ToolInfo{
			Name:        "docs",
			Description: "NusaShell documentation corpus; \"op\" selects: list {limit?} page ids/titles for vocabulary discovery; search {query,limit?} BM25-ranked page ids/titles/snippets (multi-word terms need not be contiguous); read {id} full authoritative page. When terminology is uncertain or search returns no results, use list, then MUST read relevant pages before answering. After any search hit, MUST read the relevant page before relying on its facts. Long pages are truncated in-band (~32KiB) with overflow_path in the platform temp dir — continue with file_read using next_offset_bytes.",
			InputSchema: objSchema(
				pEnum("op", "Operation", "list", "search", "read"),
				pStr("query", "Search query (op=search)"),
				pInt("limit", "Max results (op=list/search; default 50 for list, 10 for search)"),
				pStr("id", "Documentation page id (op=read)"),
			),
		},
	},
	{
		root:    "memory_project",
		members: []string{"query", "list", "read", "admit", "skip", "archive", "lint", "audit", "gate", "pattern_track", "path", "script_path"},
		def: ToolInfo{
			Name:        "memory_project",
			Description: "Per-workspace project memory (skill-compatible anchored markdown); advertised only when a workspace is set. \"op\" selects: query {topic?|kind?|related?|id?, archive?, full?, limit?} AND selectors (TSV or --full bodies, matching memory-query.sh); list files in read priority as absolute paths (memory-list.sh); read {kind} or {id}; admit {kind,content,id?} upsert then lint (debug also pattern-tracks); skip {reason} negative admission with no disk write; archive {id}; lint {kind?} (memory-lint.sh stdout; fails on issues); audit (memory-audit.sh); gate {reason?} (memory-gate.sh; reason is --no-update); pattern_track {kind} (memory-pattern-track.sh); path {kind,create?} (memory-path.sh); script_path {name,create?} (memory-script-path.sh). Never store user preferences here — those belong in user.md (file_patch/file_write) or structured memory records. See docs(op=\"read\", id=\"memory-project\").",
			InputSchema: objSchema(
				pEnum("op", "Operation", "query", "list", "read", "admit", "skip", "archive", "lint", "audit", "gate", "pattern_track", "path", "script_path"),
				pStr("topic", "Topic selector (op=query)"),
				pStr("kind", "Kind file stem (query/read/admit/lint/pattern_track/path); aliases: decision→decisions"),
				pStr("related", "Entry ID whose inbound+outbound neighbors to return (op=query)"),
				pStr("id", "Entry ID (query/read/admit/archive)"),
				pBool("archive", "Include archive/ in query results"),
				pBool("full", "Return anchored bodies instead of compact TSV (op=query)"),
				pInt("limit", "Max query hits (0 = unlimited)"),
				pStr("content", "Entry body to admit (BEGIN/END wrappers optional)"),
				pStr("reason", "Why nothing was written (op=skip required; op=gate is --no-update)"),
				pStr("name", "Shortcut script file name (op=script_path)"),
				pBool("create", "Create the kind file or scripts dir (op=path / script_path)"),
			),
		},
	},
	{
		root:    "conversation",
		members: []string{"list", "search", "read", "info", "send"},
		def: ToolInfo{
			Name: "conversation",
			Description: "Conversation rooms and transcripts; \"op\" selects: " +
				"list {limit?,offset?} visible rooms (newest activity first, excludes self and hidden pipeline/background rooms); " +
				"search {query,id?,limit?,offset?} without id: rooms matching id/title/summary/message text (match field names the hit); with id: message snippets inside that room only; " +
				"info {id,chunk?} metadata (turn_count, chunk_count, summary_preview) — chunk omits active transcript, chunk=N is 0-based archive; " +
				"read {id,chunk?,start?,end?} visible user/assistant/tool messages by inclusive turn index (0-based, oldest first; omit start and end for the last 5 turns; start=0 end=0 is turn 0 only); " +
				"send {id,content} deliver a peer message to another visible room.",
			InputSchema: objSchema(
				pEnum("op", "Operation", "list", "search", "read", "info", "send"),
				pStr("id", "Conversation id (search scope / read / info / send target)"),
				pStr("content", "Message text to send (send)"),
				pStr("query", "Search query: room id/title/summary/message, or in-room message text when id is set (search)"),
				pInt("chunk", "Optional 0-based archived pre-compaction chunk index (read/info). Omit for the active transcript."),
				pInt("start", "Inclusive first turn index (read). Omit with end for the last 5 turns."),
				pInt("end", "Inclusive last turn index (read). Omit with start to read a single turn."),
				pInt("limit", "Max results (list/search default 20)"),
				pInt("offset", "Pagination offset (list/search default 0)"),
			),
		},
	},
}

var familyByRoot = map[string]*dispatchFamily{}

func init() {
	for i := range dispatchFamilies {
		familyByRoot[dispatchFamilies[i].root] = &dispatchFamilies[i]
	}
}

// ---- schema helpers (local, JSON-schema literals) ----

type schemaProp struct {
	name   string
	schema map[string]any
}

func pStr(name, desc string) schemaProp {
	return schemaProp{name, map[string]any{"type": "string", "description": desc}}
}

func pInt(name, desc string) schemaProp {
	return schemaProp{name, map[string]any{"type": "integer", "description": desc}}
}

func pBool(name, desc string) schemaProp {
	return schemaProp{name, map[string]any{"type": "boolean", "description": desc}}
}

func pEnum(name, desc string, vals ...string) schemaProp {
	anyVals := make([]any, len(vals))
	for i, v := range vals {
		anyVals[i] = v
	}
	return schemaProp{name, map[string]any{"type": "string", "description": desc, "enum": anyVals}}
}

// objSchema builds a dispatcher input schema. The first property is the
// required `op` selector; everything else is an optional per-op parameter.
func objSchema(op schemaProp, optional ...schemaProp) map[string]any {
	properties := make(map[string]any, 1+len(optional))
	properties[op.name] = op.schema
	for _, p := range optional {
		if p.name == op.name {
			continue // `op` is fixed; skip accidental duplicates
		}
		properties[p.name] = p.schema
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             []string{"op"},
		"additionalProperties": false,
	}
}

// ---- public surface ----

// IsDispatchRoot reports whether name is an advertised dispatcher family root.
func IsDispatchRoot(name string) bool {
	_, ok := familyByRoot[name]
	return ok
}

// DispatchOp validates the `op` of a dispatcher-root call payload and
// returns it. It fails loud with the valid op list when op is missing,
// malformed, or unknown.
func DispatchOp(name string, argsJSON []byte) (string, error) {
	fam, ok := familyByRoot[name]
	if !ok {
		return "", fmt.Errorf("%q is not a dispatcher tool", name)
	}
	op := OpArg(argsJSON)
	for _, valid := range fam.members {
		if op == valid {
			return op, nil
		}
	}
	return "", fmt.Errorf("unknown %s op %q; valid ops: %s", name, op, strings.Join(fam.members, ", "))
}

// OpArg extracts the raw `op` string from a dispatcher call payload — empty
// when missing or malformed. For classification sites that treat unknown
// ops as non-matching; use DispatchOp to fail loud instead.
func OpArg(argsJSON []byte) string {
	var args struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(args.Op))
}

// DispatcherToolInfos returns the advertised family definitions. These are
// the single source of truth for their provider-facing schemas — Toolbox
// no longer carries per-verb duplicates for dispatcher families.
func DispatcherToolInfos() []ToolInfo {
	out := make([]ToolInfo, 0, len(dispatchFamilies))
	for i := range dispatchFamilies {
		out = append(out, dispatchFamilies[i].def)
	}
	return out
}

// FilterDispatcherToolInfos drops memory_project when the turn has no
// workspace so the model cannot call a tool that would only error.
func FilterDispatcherToolInfos(workspace string) []ToolInfo {
	infos := DispatcherToolInfos()
	if strings.TrimSpace(workspace) != "" {
		return infos
	}
	out := make([]ToolInfo, 0, len(infos))
	for _, info := range infos {
		if info.Name == "memory_project" {
			continue
		}
		out = append(out, info)
	}
	return out
}
