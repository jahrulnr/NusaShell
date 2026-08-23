package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/domain"
)

// Dispatcher families collapse per-verb built-ins into ONE advertised tool
// per family (skill, memory, docs, ci_pipeline) selected by an `op` field.
//
// Design contract:
//   - Only the provider roster is compacted (toolDefinitions →
//     CompactFamilies). Toolbox.ListTools keeps returning the full per-verb
//     defs so internal consumers (review-agent whitelist, pipeline toolbox
//     filtering) stay untouched.
//   - A dispatcher call {name:"memory", args:{op:"save",…}} is rewritten to
//     the canonical implementation name memory_save BEFORE persistence and
//     execution (agent runner), so conversation history, learning-event
//     classification, untrusted-output wrapping, and the UI all keep seeing
//     stable legacy names.
//   - The legacy names stay executable inside Toolbox.Execute as hidden
//     aliases: models that emit them from history or habit keep working.
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
			Name: "skill",
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
			Name: "memory",
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

var (
	familyByRoot   = map[string]*dispatchFamily{}
	familyByMember = map[string]string{} // "memory_save" -> "memory"
)

func init() {
	for i := range dispatchFamilies {
		fam := &dispatchFamilies[i]
		familyByRoot[fam.root] = fam
		for _, op := range fam.members {
			familyByMember[fam.root+"_"+op] = fam.root
		}
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

// DispatchCanonical resolves a dispatcher-root call to its canonical per-op
// implementation name (memory + op=save → memory_save). It fails loud with
// the valid op list when op is missing, malformed, or unknown.
func DispatchCanonical(name string, argsJSON []byte) (string, error) {
	fam, ok := familyByRoot[name]
	if !ok {
		return "", fmt.Errorf("%q is not a dispatcher tool", name)
	}
	var args struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("%s requires an \"op\" field (one of: %s); args are not a JSON object: %v",
			name, strings.Join(fam.members, ", "), err)
	}
	op := strings.ToLower(strings.TrimSpace(args.Op))
	for _, valid := range fam.members {
		if op == valid {
			return name + "_" + valid, nil
		}
	}
	return "", fmt.Errorf("unknown %s op %q; valid ops: %s", name, args.Op, strings.Join(fam.members, ", "))
}

// CompactFamilies collapses per-verb family members into one dispatcher
// entry per family, preserving roster order (the family definition takes the
// position of its first member). Non-family tools pass through unchanged,
// including conditional ones (artifact_*, subagent*, web_answer, …).
func CompactFamilies(defs []ToolInfo) []ToolInfo {
	emitted := make(map[string]bool, len(dispatchFamilies))
	out := make([]ToolInfo, 0, len(defs))
	for _, def := range defs {
		root, isMember := familyByMember[def.Name]
		if !isMember {
			out = append(out, def)
			continue
		}
		if emitted[root] {
			continue
		}
		emitted[root] = true
		out = append(out, familyByRoot[root].def)
	}
	return out
}

// CanonicalizeToolCalls rewrites dispatcher-root calls in place to their
// canonical per-op names. Args are preserved verbatim (the extra "op" key is
// ignored by the legacy handlers' strict structs). Calls without a valid op
// are left untouched so Toolbox.Execute fails loud with the valid list.
func CanonicalizeToolCalls(toolCalls []domain.ToolCall) {
	for i := range toolCalls {
		tc := &toolCalls[i]
		if !IsDispatchRoot(tc.Name) {
			continue
		}
		if canon, err := DispatchCanonical(tc.Name, []byte(tc.Args)); err == nil {
			tc.Name = canon
		}
	}
}
