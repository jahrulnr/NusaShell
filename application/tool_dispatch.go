package application

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Dispatcher families expose ONE advertised tool per family (skill, memory,
// docs, ci_pipeline) selected by a required `op` argument. Root+op is the
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
		root:    "skill",
		members: []string{"list", "search", "read", "files", "save"},
		def: ToolInfo{
			Name:        "skill",
			Description: "Skill library; \"op\" selects: list {limit?}; search {query,limit?} name/description substring match; read {name,path?,offset?,max_chars?} SKILL.md or support file — always read a skill before relying on it; files {name} list folder contents; save {name,content,description?,id?,path?} create/update SKILL.md, or write a support file when path is set (skill must exist)",
			InputSchema: objSchema(
				pEnum("op", "Operation", "list", "search", "read", "files", "save"),
				pStr("query", "Search query (op=search)"),
				pInt("limit", "Max results (list default 100, search default 50)"),
				pStr("name", "Skill id (op=read/files) or skill name, lowercase-with-hyphens (op=save)"),
				pStr("path", "Relative file path inside the skill folder; defaults to SKILL.md (read) or targets a support file the skill must already have (save)"),
				pInt("offset", "Character offset for pagination (op=read, default 0)"),
				pInt("max_chars", "Max characters to return (op=read, default 20000 max 100000)"),
				pStr("id", "Existing skill id to update; omit to create (op=save without path)"),
				pStr("description", "Short description up to 1024 chars (op=save SKILL.md mode)"),
				pStr("content", "Full file content (op=save)"),
			),
		},
	},
	{
		root:    "memory",
		members: []string{"save", "replace", "search", "list", "delete"},
		def: ToolInfo{
			Name:        "memory",
			Description: "Long-term memory; \"op\" selects: save {content,category?,project?,task?,tags?} idempotent fragment dedup — durable knowledge only; replace {target:primary|fragment,content,old_text?,id?} edit primary document substring/whole body or one fragment; search {query,category?,project?,task?,tags?,limit?} BM25-ranked fragments; list {target?:primary|fragments,category?,project?,limit?}; delete {id}",
			InputSchema: objSchema(
				pEnum("op", "Operation", "save", "replace", "search", "list", "delete"),
				pStr("content", "Fact/observation to save, replacement body, or new fragment content"),
				pEnum("category", "Memory category (save/search/list)", "project", "user", "task", "general"),
				pStr("project", "Optional project/workspace label"),
				pStr("task", "Optional task label (save/search)"),
				pArr("tags", "Optional tags; search requires ALL to match"),
				pEnum("target", "replace: primary|fragment · list: primary|fragments (default fragments)", "primary", "fragment", "fragments"),
				pStr("old_text", "Primary substring to replace; omit to rewrite the entire body (op=replace target=primary)"),
				pStr("id", "Fragment id (op=replace target=fragment / op=delete)"),
				pStr("query", "Search query (op=search)"),
				pInt("limit", "Max results (search default 20, list default 50)"),
			),
		},
	},
	{
		root:    "docs",
		members: []string{"search", "read"},
		def: ToolInfo{
			Name:        "docs",
			Description: "NusaShell documentation corpus; \"op\" selects: search {query,limit?} ranked page ids; read {id} full page by id from search results",
			InputSchema: objSchema(
				pEnum("op", "Operation", "search", "read"),
				pStr("query", "Search query (op=search)"),
				pInt("limit", "Max results (op=search, default 10)"),
				pStr("id", "Documentation page id (op=read)"),
			),
		},
	},
	{
		root:    "ci_pipeline",
		members: []string{"list", "read", "validate"},
		def: ToolInfo{
			Name:        "ci_pipeline",
			Description: "Workspace CI pipeline definition (.nusashell/pipeline.yaml); \"op\" selects: list {workspace?}; read {workspace?} read + validate the definition; validate {yaml,workspace?} structured errors for YAML text",
			InputSchema: objSchema(
				pEnum("op", "Operation", "list", "read", "validate"),
				pStr("workspace", "Workspace path"),
				pStr("yaml", "Pipeline YAML text (op=validate)"),
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

func pEnum(name, desc string, vals ...string) schemaProp {
	anyVals := make([]any, len(vals))
	for i, v := range vals {
		anyVals[i] = v
	}
	return schemaProp{name, map[string]any{"type": "string", "description": desc, "enum": anyVals}}
}

func pArr(name, desc string) schemaProp {
	return schemaProp{name, map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string"}}}
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
		"type":       "object",
		"properties": properties,
		"required":   []string{"op"},
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
