package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
)

const (
	// mcpCallDefaultTimeout bounds a single mcp_call when the caller passes
	// no timeout_ms. stdio MCP servers carry no protocol-level timeout, so
	// without a deadline a hung server parks the call forever.
	mcpCallDefaultTimeout = 30 * time.Second
	// mcpCallMaxTimeout caps an explicit timeout_ms, matching the exec
	// tool's hard ceiling so a call cannot opt out of all bounds.
	mcpCallMaxTimeout = time.Hour
)

func (t *Toolbox) mcpToolEntries() []toolEntry {
	return []toolEntry{
		{
			info:    application.ToolInfo{Name: "mcp_list", Description: "List configured MCP servers with their enabled status and runtime state (running/stopped).", InputSchema: obj("object", nil)},
			handler: t.execMcpList,
		},
		{
			info:    application.ToolInfo{Name: "tool_list", Description: "List tools from a running MCP server. Accepts the plugin id (e.g. \"nusashell.terminal\"). When omitted, lists tools across all running MCP servers. Returns compact entries (ref, name, server, description) without parameter schemas — load the exact schema with tool_schema before first use of an unfamiliar tool. Oversized catalogs are truncated in-band (~32KiB) with overflow_path pointing at the full JSONL in the platform temp dir — continue with file_read.", InputSchema: obj("object", props("server", str("Plugin id; when omitted, lists all running servers")))},
			handler: t.execToolList,
		},
		{
			info:    application.ToolInfo{Name: "tool_schema", Description: "Load one MCP tool's input schema by plugin id and tool name. The tool name is the bare tool name (e.g. \"exec\"). Returns the schema as readable JSON — the only place schemas are served; mcp_search and tool_list stay schema-free so large catalogs remain token-cheap. Oversized schemas are truncated in-band (~32KiB) with overflow_path pointing at the full definition in the platform temp dir — continue with file_read.", InputSchema: obj("object", props("server", str("Plugin id (e.g. nusashell.terminal)"), "tool", str("Bare tool name within the server (e.g. \"exec\")")), "server", "tool")},
			handler: t.execToolSchema,
		},
		{
			info:    application.ToolInfo{Name: "mcp_search", Description: "Search running MCP servers' tools by name or description (case-insensitive token match — any term matches). When server is omitted, searches across ALL running servers. Returns compact matches (ref, name, description, ranked) without inlining parameter schemas — call tool_schema for exact argument fields when needed. The header reports count (matches returned), total (every match before the limit), and limit — when count < total the list was cut, so raise limit or narrow the query instead of assuming the catalog is exhausted. Oversized result sets are truncated in-band (~32KiB) with overflow_path pointing at the full JSONL in the platform temp dir — continue with file_read. This is the universal MCP discovery path on every provider — use mcp_search + mcp_call instead of guessing tool names.", InputSchema: obj("object", props("server", str("Optional: plugin id; when omitted, searches all running servers"), "query", str("Search query"), "limit", intSchema("Max results, default 20")), "query")},
			handler: t.execMcpSearch,
		},
		{
			info:    application.ToolInfo{Name: "mcp_call", Description: "Execute an MCP tool by ref. Get the ref from mcp_search or tool_list (format <plugin-id>:<tool>, e.g. nusashell.files:read). Pass `arguments_json` as a JSON object matching the tool's parameters schema — the exact arguments the tool expects, e.g. {\"path\":\"/etc/hosts\"}. Omit `arguments_json` entirely for parameterless tools (defaults to {}). The call is bounded by `timeout_ms` (default 30000, max 3600000): when it fires the call fails with a timeout error — retry with a larger timeout_ms only for tools that are legitimately slow. The ref binds to a specific running server + tool; if it was disabled or restarted since discovery, you get a STALE_TOOL_REF error — search again. Plugins that declare a usage contract (contract flag in mcp_list) must be read first via contract_read when required by the plugin_contract_mode setting. This is the only MCP execution path — mcp__<server>__<tool> names are not callable.", InputSchema: obj("object", props("ref", str("Tool ref from mcp_search / tool_list results (e.g. nusashell.files:read)"), "arguments_json", freeObj("Tool arguments as a JSON object matching the parameters schema (e.g. {\"path\":\"/etc/hosts\"}). Optional; defaults to {} — omit entirely for parameterless tools. Load the exact schema with tool_schema if unsure."), "timeout_ms", intSchema("Optional call timeout in milliseconds (default 30000, max 3600000)")), "ref")},
			handler: t.execMcpCall,
		},
		{
			info:    application.ToolInfo{Name: "contract_read", Description: "Read a plugin's usage contract (best-practice rules plus state & side-effect disclosure) declared in its manifest before working with that plugin's tools. Pass id=<plugin-id>, or id=all to read every contract-declaring plugin at once. Advisory by default (plugin_contract_mode defaults to hint); enforcement is only active when the setting is set to require.", InputSchema: obj("object", props("id", str("Plugin id (e.g. nusashell.files) or 'all'")), "id")},
			handler: t.execContractRead,
		},
		{
			info:    application.ToolInfo{Name: "mcp_register", Description: "Copy a new MCP plugin from an absolute staging folder into the installed plugin store, or replace an existing plugin with the same id. The source must contain manifest.json and must stay outside the installed plugins root. Check mcp_list and ask the user before replacing an existing id; then call mcp_enable.", InputSchema: obj("object", props("source", str("Absolute staging path to the plugin folder containing manifest.json")), "source")},
			handler: t.execMcpRegister,
		},
		{
			info:    application.ToolInfo{Name: "mcp_enable", Description: "Start/connect an MCP plugin so its tools become available. Returns only status + tool count — use tool_list or mcp_search to discover the tools. If already connected, returns already_enabled without reconnecting. The plugin must be registered first (mcp_register or the Plugins view).", InputSchema: obj("object", props("id", str("Plugin id (e.g. nusashell.files)")), "id")},
			handler: t.execMcpEnable,
		},
		{
			info:    application.ToolInfo{Name: "mcp_disable", Description: "Stop/disconnect an MCP plugin. The definition stays installed; only the MCP subprocess is stopped. Tools from this server are no longer listed.", InputSchema: obj("object", props("id", str("Plugin id")), "id")},
			handler: t.execMcpDisable,
		},
		{
			info:    application.ToolInfo{Name: "mcp_unregister", Description: "Remove an MCP plugin entirely and delete its installed folder. Ask the user for confirmation first. Use mcp_disable when the plugin only needs to stop.", InputSchema: obj("object", props("id", str("Plugin id")), "id")},
			handler: t.execMcpUnregister,
		},
		{
			info:    application.ToolInfo{Name: "mcp_install", Description: "Install an MCP plugin from the curated catalog or a GitHub repository (owner/repo or URL). After install, call mcp_enable with the resulting plugin id to connect and load its tools.", InputSchema: obj("object", props("source", strEnum("Install source", "catalog", "github"), "id", str("Catalog plugin id (required when source=catalog)"), "url", str("GitHub repo URL or owner/repo shorthand (required when source=github)"), "subdir", str("Optional subdirectory inside a monorepo (github)"), "ref", str("Optional branch or tag to pin (github)")), "source")},
			handler: t.execMcpInstall,
		},
		{
			info:    application.ToolInfo{Name: "mcp_server_add", Description: "Register a manual MCP server (no manifest needed). Transports: stdio (command/args/env, e.g. npx servers), sse, or http (Streamable HTTP) with url and optional headers for remote servers. Use for generic MCP servers; use mcp_register for NusaShell plugin folders. After adding, call mcp_enable with the server id to connect and load its tools.", InputSchema: obj("object", props("name", str("Human-readable server name"), "transport", strEnum("Transport kind", "stdio", "sse", "http"), "command", str("Command to launch the server (stdio transport, e.g. npx, node, python)"), "url", str("Server URL (required for sse/http transports, e.g. https://host/mcp)"), "args", arr("Arguments for the stdio command (e.g. -y @modelcontextprotocol/server-github)"), "env", obj("object", props("additional", str("KEY=VALUE entries for the stdio process")), "additional"), "headers", obj("object", props("additional", str("HTTP headers for sse/http transports, e.g. Authorization: Bearer <token>")), "additional"), "id", str("Optional stable id (default auto-generated)")), "name")},
			handler: t.execMcpServerAdd,
		},
	}
}

func (t *Toolbox) execMcpInstall(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Source string `json:"source"`
		ID     string `json:"id"`
		URL    string `json:"url"`
		Subdir string `json:"subdir"`
		Ref    string `json:"ref"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if t.PluginInstaller == nil {
		return "", depMissing("plugin installer not available")
	}
	var src domain.PluginInstallSource
	switch args.Source {
	case "catalog":
		src = domain.InstallSourceCatalog
		if strings.TrimSpace(args.ID) == "" {
			return "", fmt.Errorf("id is required when source=catalog")
		}
	case "github":
		src = domain.InstallSourceGitHub
		if strings.TrimSpace(args.URL) == "" {
			return "", fmt.Errorf("url is required when source=github (owner/repo or URL)")
		}
	default:
		return "", fmt.Errorf(`source must be "catalog" or "github"`)
	}
	ctxInst, cancelInst := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelInst()
	p, err := t.PluginInstaller.Install(ctxInst, domain.PluginInstallRequest{
		Source: src,
		ID:     args.ID,
		URL:    args.URL,
		Subdir: args.Subdir,
		Ref:    args.Ref,
	})
	if err != nil {
		return "", fmt.Errorf("mcp_install: %w", err)
	}
	if dropper, ok := t.MCP.(interface{ Drop(string) }); ok {
		dropper.Drop(p.Manifest.MCPServerID())
	}
	return yamlMD(map[string]any{"status": "installed", "name": p.Manifest.Name, "id": p.Manifest.ID, "version": p.Manifest.Version}, "Plugin installed. Call `mcp_enable id="+p.Manifest.ID+"` to connect and load its tools."), nil
}

func (t *Toolbox) execMcpServerAdd(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID        string            `json:"id"`
		Name      string            `json:"name"`
		Transport string            `json:"transport"`
		Command   string            `json:"command"`
		URL       string            `json:"url"`
		Args      []string          `json:"args"`
		Env       map[string]string `json:"env"`
		Headers   map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Name) == "" {
		return "", fmt.Errorf("name is required")
	}
	if t.Plugins == nil {
		return "", depMissing("plugin store not available")
	}
	transport := domain.PluginTransport(strings.TrimSpace(args.Transport))
	if transport == "" {
		transport = domain.PluginTransportStdio
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		id = domain.NewID(domain.IDPrefixMcp)
	} else if !domain.ValidatePluginID(id) {
		return "", fmt.Errorf("id %q is not a valid plugin identifier", id)
	}
	// Reuse the same storage model as CLI MCP servers (manual manifest).
	p := &domain.Plugin{Manifest: domain.PluginManifest{
		ID:      id,
		Name:    strings.TrimSpace(args.Name),
		Version: "0.1.0",
		Icon:    "🧩",
		MCP: domain.PluginMCPConfig{
			Transport: transport,
			Command:   strings.TrimSpace(args.Command),
			URL:       strings.TrimSpace(args.URL),
			Args:      args.Args,
			Env:       args.Env,
			Headers:   args.Headers,
		},
	}}
	if err := p.Manifest.Validate(); err != nil {
		return "", fmt.Errorf("mcp_server_add: %w", err)
	}
	if err := t.Plugins.Save(p); err != nil {
		return "", fmt.Errorf("mcp_server_add: %w", err)
	}
	if dropper, ok := t.MCP.(interface{ Drop(string) }); ok {
		dropper.Drop(p.Manifest.MCPServerID())
	}
	return yamlMD(map[string]any{"status": "added", "name": p.Manifest.Name, "id": p.Manifest.ID}, "Server added. Call `mcp_enable id="+p.Manifest.ID+"` to connect and load its tools."), nil
}

func (t *Toolbox) execMcpRegister(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Source) == "" {
		return "", fmt.Errorf("source is required: absolute path to a plugin folder containing manifest.json")
	}
	if t.Plugins == nil {
		return "", depMissing("plugin store not available")
	}
	absSource, err := filepath.Abs(args.Source)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	p, err := t.Plugins.Install(absSource)
	if err != nil {
		return "", fmt.Errorf("mcp_register: %w", err)
	}
	// Drop any stale cached connection so the new manifest is used.
	if mcp, ok := t.MCP.(interface{ Drop(string) }); ok {
		mcp.Drop(p.Manifest.MCPServerID())
	}
	return yamlMD(map[string]any{"status": "registered", "name": p.Manifest.Name, "id": p.Manifest.ID, "version": p.Manifest.Version}, "Plugin registered. Call `mcp_enable id="+p.Manifest.ID+"` to connect and load its tools."), nil
}

func (t *Toolbox) execMcpEnable(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.ID) == "" {
		return "", fmt.Errorf("id is required (use mcp_list to see registered plugins)")
	}
	if t.Plugins == nil || t.MCP == nil {
		return "", depMissing("plugin runtime not available")
	}
	p, err := t.Plugins.Get(args.ID)
	if err != nil {
		return "", fmt.Errorf("plugin %q not found; register it first with mcp_register", args.ID)
	}
	// Idempotency: if the server is already connected, return a short
	// already_enabled signal without reconnecting. The agent should use
	// tool_list or mcp_search to discover tools on an enabled server.
	if tools, ok := t.MCP.ToolsFor(p.Manifest.MCPServerID()); ok {
		return yamlMD(map[string]any{
			"status": "already_enabled",
			"server": p.Manifest.ID,
			"tools":  len(tools),
		}, "Plugin is already connected. Use tool_list or mcp_search to discover its tools."), nil
	}
	ctxConn, cancelConn := context.WithTimeout(ctx, 20*time.Second)
	defer cancelConn()
	tools, err := t.MCP.Connect(ctxConn, p)
	if err != nil {
		return "", fmt.Errorf("mcp_enable %q: %w", args.ID, err)
	}
	return yamlMD(map[string]any{
		"status": "enabled",
		"server": p.Manifest.ID,
		"tools":  len(tools),
	}, "Plugin connected. Use tool_list or mcp_search to discover its tools."), nil
}

func (t *Toolbox) execMcpDisable(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	if t.MCP == nil {
		return "", depMissing("plugin runtime not available")
	}
	dropper, ok := t.MCP.(interface{ Drop(string) })
	if !ok {
		return "", fmt.Errorf("mcp runtime does not support disconnect")
	}
	dropper.Drop("plugin:" + args.ID)
	return yamlBlock(map[string]any{"status": "disabled"}), nil
}

func (t *Toolbox) execMcpUnregister(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.ID) == "" {
		return "", fmt.Errorf("id is required")
	}
	if t.Plugins == nil {
		return "", depMissing("plugin store not available")
	}
	if _, err := t.Plugins.Get(args.ID); err != nil {
		return "", fmt.Errorf("plugin %q not found", args.ID)
	}
	if err := t.Plugins.Delete(args.ID); err != nil {
		return "", fmt.Errorf("mcp_unregister: %w", err)
	}
	if dropper, ok := t.MCP.(interface{ Drop(string) }); ok {
		dropper.Drop("plugin:" + args.ID)
	}
	return yamlBlock(map[string]any{"status": "unregistered"}), nil
}

func (t *Toolbox) execMcpList(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Plugins == nil {
		return yamlBlock(map[string]any{"count": 0, "plugins": []any{}}), nil
	}
	plugins, err := t.Plugins.List()
	if err != nil {
		return "", fmt.Errorf("mcp_list: %w", err)
	}
	items := make([]any, 0, len(plugins))
	for _, p := range plugins {
		toolCount := 0
		running := false
		serverID := p.Manifest.MCPServerID()
		if tools, ok := t.MCP.ToolsFor(serverID); ok {
			running = true
			toolCount = len(tools)
		}
		item := map[string]any{
			"name":    p.Manifest.Name,
			"id":      p.Manifest.ID,
			"running": running,
			"tools":   toolCount,
		}
		// Contract flag is omitted (not false) for contract-less plugins
		// so the historical output shape stays unchanged.
		if p.Manifest.ContractEntry() != "" {
			item["contract"] = true
		}
		items = append(items, item)
	}
	return yamlJSONL(map[string]any{"count": len(items)}, items), nil
}

func (t *Toolbox) execToolList(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Server string `json:"server"`
	}
	_ = json.Unmarshal(argsJSON, &args)
	var items []any
	plugins, _ := t.Plugins.List()
	for _, p := range plugins {
		if args.Server != "" && !pluginMatchesServer(p, args.Server) {
			continue
		}
		tools, ok := t.MCP.ToolsFor(p.Manifest.MCPServerID())
		if !ok {
			continue
		}
		for _, tool := range tools {
			// Schema is intentionally NOT inlined: discovery output stays
			// compact so catalogs with hundreds of tools remain token-cheap.
			// The full input schema is available via tool_schema. Oversized
			// catalogs spill to the platform temp dir through capJSONL so the
			// model can file_read the remainder instead of losing tools.
			items = append(items, map[string]any{
				"ref":         p.Manifest.ID + ":" + tool.Name,
				"name":        tool.Name,
				"server":      p.Manifest.ID,
				"description": tool.Description,
			})
		}
	}
	return capJSONL("tool_list", map[string]any{"count": len(items)}, items), nil
}

func (t *Toolbox) execToolSchema(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Server string `json:"server"`
		Tool   string `json:"tool"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	plugins, _ := t.Plugins.List()
	for _, p := range plugins {
		if !pluginMatchesServer(p, args.Server) {
			continue
		}
		tools, ok := t.MCP.ToolsFor(p.Manifest.MCPServerID())
		if !ok {
			return "", fmt.Errorf("plugin id %q is not running; call mcp_list to see running servers", args.Server)
		}
		for _, tool := range tools {
			if tool.Name != args.Tool {
				continue
			}
			var schema map[string]any
			if len(tool.InputSchema) > 0 {
				_ = json.Unmarshal(tool.InputSchema, &schema)
			}
			if schema == nil {
				schema = obj("object", nil)
			}
			// Emit the full tool definition as a single JSONL line:
			// name, description, and the complete input_schema object.
			// Monster schemas spill through capJSONL under the same
			// overflow contract as tool_list / mcp_search.
			items := []any{map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  schema,
			}}
			return capJSONL("tool_schema", map[string]any{}, items), nil
		}
		return "", fmt.Errorf("tool %q not found on plugin %q; use tool_list to see available tools", args.Tool, args.Server)
	}
	return "", fmt.Errorf("plugin id %q not found; use mcp_list to see configured servers", args.Server)
}

func (t *Toolbox) execMcpSearch(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Server string `json:"server"`
		Query  string `json:"query"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 20
	}
	items := t.collectMCPToolMatches(args.Server, args.Query)
	items = rankMCPItems(items, args.Query)
	matched := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	// total counts every match before the limit slice, so a cut is never
	// silent: the model sees count < total, knows the catalog continues,
	// and re-queries with a larger limit (or a narrower query) instead of
	// assuming the match list is exhausted. Oversized result sets spill
	// through capJSONL with overflow_path / next_offset_bytes.
	meta := map[string]any{"count": len(items), "total": matched, "limit": limit}
	return capJSONL("mcp_search", meta, items), nil
}

func (t *Toolbox) execMcpCall(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		Ref           string          `json:"ref"`
		ArgumentsJSON json.RawMessage `json:"arguments_json"`
		TimeoutMs     int             `json:"timeout_ms"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Ref) == "" {
		return "", fmt.Errorf("ref is required (use mcp_search to find tools and their refs)")
	}
	// Tolerant parsing: accept arguments_json as a JSON string
	// or as a JSON object directly (canonical form matching MCP spec).
	// Omitting arguments_json entirely defaults to {}.
	var toolArgs map[string]any
	if len(args.ArgumentsJSON) == 0 || string(args.ArgumentsJSON) == "null" {
		toolArgs = map[string]any{}
	} else if len(args.ArgumentsJSON) > 0 && args.ArgumentsJSON[0] == '"' {
		// arguments_json is a JSON string containing escaped JSON.
		var encoded string
		if err := json.Unmarshal(args.ArgumentsJSON, &encoded); err != nil {
			return "", fmt.Errorf("arguments_json must be a JSON object or a JSON string encoding a JSON object: %v", err)
		}
		if err := json.Unmarshal([]byte(encoded), &toolArgs); err != nil {
			return "", fmt.Errorf("arguments_json must be a JSON object (e.g. {\"path\":\"/etc/hosts\"}): %v", err)
		}
	} else {
		// Canonical form: arguments_json is already a JSON object.
		if err := json.Unmarshal(args.ArgumentsJSON, &toolArgs); err != nil {
			return "", fmt.Errorf("arguments_json must be a JSON object (e.g. {\"path\":\"/etc/hosts\"}): %v", err)
		}
	}
	idx := strings.LastIndex(args.Ref, ":")
	if idx <= 0 || idx == len(args.Ref)-1 {
		return "", fmt.Errorf("malformed ref %q: expected <plugin-id>:<tool> form from mcp_search", args.Ref)
	}
	server, toolName := args.Ref[:idx], args.Ref[idx+1:]
	plugins, _ := t.Plugins.List()
	var plugin *domain.Plugin
	for _, p := range plugins {
		if p.Manifest.ID == server {
			plugin = p
			break
		}
	}
	if plugin == nil {
		return "", fmt.Errorf("unknown plugin id %q in ref; use mcp_search to find tools", server)
	}
	// Staleness check: verify the server is still connected and the
	// tool still exists. If the plugin was disabled/restarted since
	// the mcp_search, reject with STALE_TOOL_REF so the agent knows
	// to search again.
	tools, connected := t.MCP.ToolsFor(plugin.Manifest.MCPServerID())
	if !connected {
		return "", fmt.Errorf("STALE_TOOL_REF: plugin %q is not running; call mcp_search again", plugin.Manifest.ID)
	}
	var target *contracts.MCPToolDTO
	for i := range tools {
		if tools[i].Name == toolName {
			target = &tools[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("STALE_TOOL_REF: tool %q not found on plugin %q; call mcp_search again", toolName, plugin.Manifest.ID)
	}
	// Missing-args guard: an empty arguments payload against a tool whose
	// input schema declares required properties means the caller (or a
	// weak function-calling model that resolved the free-form object to
	// {}) sent no arguments. Fail loud with the missing field names so
	// the agent can load tool_schema and retry — forwarding {} to the
	// server would surface only an opaque downstream error. Parameterless
	// tools (no required fields) still proceed with empty args.
	if len(toolArgs) == 0 && len(target.InputSchema) > 0 {
		if missing := requiredSchemaFields(target.InputSchema); len(missing) > 0 {
			return "", fmt.Errorf("MISSING_ARGS: %s requires [%s]; load tool_schema and retry with arguments_json as a JSON object", args.Ref, strings.Join(missing, ", "))
		}
	}
	// Usage-contract gate: plugins declaring contract.entry are subject
	// to plugin_contract_mode (off/hint/require). Runs last so ref and
	// staleness errors always win over gate errors.
	advisory, err := t.contractCheck(ctx, plugin)
	if err != nil {
		return "", err
	}
	// Bound the call: stdio MCP servers have no protocol-level timeout,
	// so without a deadline a hung server parks the tool call forever.
	timeout := mcpCallDefaultTimeout
	if args.TimeoutMs > 0 {
		timeout = time.Duration(args.TimeoutMs) * time.Millisecond
	}
	if timeout > mcpCallMaxTimeout {
		timeout = mcpCallMaxTimeout
	}
	callCtx, cancelCall := context.WithTimeout(ctx, timeout)
	defer cancelCall()
	result, err := t.MCP.CallTool(callCtx, plugin.Manifest.MCPServerID(), toolName, toolArgs)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return "", fmt.Errorf("mcp_call timed out after %s; the tool may still be running server-side — retry with a larger timeout_ms if the operation is legitimately slow", timeout)
		}
		return "", err
	}
	if advisory != "" {
		return result + "\n\n[contract notice] " + advisory, nil
	}
	return result, nil
}

// collectMCPToolMatches gathers MCP tools matching any query token by
// substring over name + description (unchanged recall), with the same
// ref-shaped items as tool_list.
func (t *Toolbox) collectMCPToolMatches(server, query string) []any {
	tokens := strings.Fields(strings.ToLower(query))
	var items []any
	plugins, _ := t.Plugins.List()
	for _, p := range plugins {
		if server != "" && !pluginMatchesServer(p, server) {
			continue
		}
		tools, ok := t.MCP.ToolsFor(p.Manifest.MCPServerID())
		if !ok {
			continue
		}
		for _, tool := range tools {
			hay := strings.ToLower(tool.Name + " " + tool.Description)
			hit := false
			for _, tok := range tokens {
				if strings.Contains(hay, tok) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			// Schema is intentionally NOT inlined: search stays compact so
			// catalogs with hundreds of tools remain token-cheap. Full input
			// schema is available via tool_schema.
			items = append(items, map[string]any{
				"ref":         p.Manifest.ID + ":" + tool.Name,
				"name":        tool.Name,
				"server":      p.Manifest.ID,
				"description": tool.Description,
			})
		}
	}
	return items
}

// rankMCPItems reorders MCP tool results by BM25 relevance over server +
// name + description (separator-aware tokens, so "read file" ranks
// read_file first). Substring recall is unchanged — BM25 only ranks;
// zero-score items keep their original relative order at the tail.
func rankMCPItems(items []any, query string) []any {
	if len(items) <= 1 || strings.TrimSpace(query) == "" {
		return items
	}
	docs := make([]jsonstore.BM25Doc, len(items))
	for i, it := range items {
		m := it.(map[string]any)
		docs[i] = jsonstore.BM25Doc{
			ID:   strconv.Itoa(i),
			Text: fmt.Sprintf("%v %v %v", m["server"], m["name"], m["description"]),
		}
	}
	results := jsonstore.NewBM25(docs).Search(query, len(items))
	rank := make(map[int]int, len(results))
	for i, r := range results {
		if idx, err := strconv.Atoi(r.ID); err == nil {
			rank[idx] = i
		}
	}
	sort.SliceStable(items, func(a, b int) bool {
		ra, oka := rank[a]
		rb, okb := rank[b]
		if oka != okb {
			return oka
		}
		if oka {
			return ra < rb
		}
		return a < b
	})
	return items
}

// pluginMatchesServer reports whether the plugin matches the given server
// identifier. The only accepted form is the plugin id (e.g.
// "nusashell.terminal") — the same value mcp_list returns in its "id"
// field and the same value used as the prefix in tool refs
// (<plugin-id>:<tool>). Display names and "plugin:"-prefixed server ids
// are NOT accepted; this keeps tool discovery unambiguous across thousands
// of MCP tools with similar display names.
func pluginMatchesServer(p *domain.Plugin, server string) bool {
	return p.Manifest.ID == server
}
