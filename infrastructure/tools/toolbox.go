// Package tools implements the agent's built-in toolbox (skill_*,
// memory_*, docs_*) plus dynamic mcp__<server>__<tool> tools.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"

	"github.com/jahrulnr/searchwire"
)

func ptrBool(v bool) *bool { return &v }

type Toolbox struct {
	Skills          application.SkillStore
	Memory          application.MemoryStore // legacy — used by lifecycle/learning subsystems
	Primary         application.PrimaryStore
	Fragments       application.FragmentStore
	Docs            application.DocsSource
	Plugins         application.PluginStore
	PluginInstaller application.PluginInstaller
	Todos           application.ConversationTodoPort
	Searcher        *searchwire.Searcher // zero-config searcher for web_search + web_fetch
	Settings        application.SettingsStore
	Credentials     application.CredentialStore
	AskQuestions    *application.AskQuestionService
	Automation      *application.Automation
	MCP             interface {
		Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
		ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
		CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
	}
	Acp interface {
		SpawnSubagents(ctx context.Context, argsJSON []byte) (string, error)
		SteerAcpRun(ctx context.Context, argsJSON []byte) (string, error)
		StopAcpRun(ctx context.Context, argsJSON []byte) (string, error)
		WaitAcpRun(ctx context.Context, argsJSON []byte) (string, error)
		EnabledAcpAgents() []*domain.AcpAgent
	}
	Steerer interface {
		SteerHeadlessTurn(conversationID, text string) error
	}
	Artifacts application.CanvasArtifactStore
}

// webAnswerSearcher builds a searchwire.Searcher on-demand from the web
// answer settings + stored API key. Returns nil if web_answer is not
// configured (no provider or no API key).
func (t *Toolbox) webAnswerSearcher() *searchwire.Searcher {
	if t.Settings == nil || t.Credentials == nil {
		return nil
	}
	s := t.Settings.Get()
	provider := strings.TrimSpace(s.WebAnswerProvider)
	if provider == "" {
		return nil
	}
	key, has, _ := t.Credentials.Get("web_answer")
	if !has || strings.TrimSpace(key) == "" {
		return nil
	}
	model := strings.TrimSpace(s.WebAnswerModel)
	disabled := false
	cfg := searchwire.Config{
		Timeout: 120 * time.Second,
		// Explicitly disable all answer providers so env var fallback
		// (e.g. OPENROUTER_API_KEY) doesn't silently enable a provider
		// the user didn't select in Settings.
		OpenRouter: searchwire.OpenRouterConfig{Enabled: &disabled},
		OpenAI:     searchwire.OpenAIConfig{Enabled: &disabled},
		Perplexity: searchwire.PerplexityConfig{Enabled: &disabled},
		Anthropic:  searchwire.AnthropicConfig{Enabled: &disabled},
		XAI:        searchwire.XAIConfig{Enabled: &disabled},
	}
	switch provider {
	case "brave":
		cfg.Brave = searchwire.BraveConfig{APIKey: key}
	case "openrouter":
		cfg.OpenRouter = searchwire.OpenRouterConfig{Enabled: ptrBool(true), APIKey: key, Model: model}
	case "openai":
		cfg.OpenAI = searchwire.OpenAIConfig{Enabled: ptrBool(true), APIKey: key, Model: model}
	case "perplexity":
		cfg.Perplexity = searchwire.PerplexityConfig{Enabled: ptrBool(true), APIKey: key, Preset: model}
	case "anthropic":
		cfg.Anthropic = searchwire.AnthropicConfig{Enabled: ptrBool(true), APIKey: key, Model: model}
	case "xai":
		cfg.XAI = searchwire.XAIConfig{Enabled: ptrBool(true), APIKey: key, Model: model}
	default:
		return nil
	}
	return searchwire.New(cfg)
}

func (t *Toolbox) ListTools() []application.ToolInfo {
	tools := []application.ToolInfo{
		{Name: "skill_list", Description: "List available skills with their names and descriptions.", InputSchema: obj("object", props("limit", intSchema("Max results, default 100")))},
		{Name: "skill_search", Description: "Search installed skills by name or description (case-insensitive substring match).", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results, default 50")), "query")},
		{Name: "skill_read", Description: "Read a text file inside an installed skill (default SKILL.md). Pass path for support files (e.g. references/x.md) and offset/max_chars for pagination of long files.", InputSchema: obj("object", props("name", str("Skill id (from skill_list or skill_search)"), "path", str("Relative file path inside the skill folder; defaults to SKILL.md"), "offset", intSchema("Character offset for pagination (default 0)"), "max_chars", intSchema("Maximum characters to return (default 20000, max 100000)")), "name")},
		{Name: "skill_save", Description: "Create or update a skill, or write a support file inside an existing skill. When path is set, write content to that file (e.g. references/errors.md, templates/config.yaml, scripts/verify.sh) — the skill must already exist. When path is omitted, create or update the skill's SKILL.md body and metadata (name, description). Skills should be reusable procedures or domain knowledge, not one-off task notes.", InputSchema: obj("object", props("id", str("Existing skill id to update (omit to create new; ignored when path is set)"), "name", str("Skill name (lowercase with hyphens, matches folder name)"), "description", str("Short description (max 1024 chars); ignored when path is set"), "path", str("Relative file path inside the skill folder (e.g. references/x.md); defaults to SKILL.md when omitted"), "content", str("Full file content")), "name", "content")},
		{Name: "skill_files", Description: "List the files inside an installed skill folder (SKILL.md + support files) with size and editability, to discover references/guides before skill_read.", InputSchema: obj("object", props("name", str("Skill id")), "name")},
		{Name: "memory_save", Description: "Save a fact to long-term memory as a searchable fragment. Fragments are unlimited and indexed by content + metadata (category, project, task, tags). Use memory_search to retrieve them later.", InputSchema: obj("object", props("content", str("Fact or observation to remember"), "category", strEnum("Memory category", "project", "user", "task", "general"), "project", str("Optional project/workspace label"), "task", str("Optional task label"), "tags", arr("Optional tags for filtering")), "content")},
		{Name: "memory_replace", Description: "Update an existing memory entry. For primary memory, use old_text (substring match). For fragments, use id to update by identifier.", InputSchema: obj("object", props("target", strEnum("Update target: \"primary\" (always-injected working set) or \"fragment\" (searchable archive)", "primary", "fragment"), "old_text", str("For primary: unique substring of the entry to replace"), "id", str("For fragment: fragment id to update"), "content", str("New content")), "target", "content")},
		{Name: "memory_search", Description: "Search memory fragments by content (BM25) with optional metadata filters (category, project, task, tags). Returns ranked results with scores.", InputSchema: obj("object", props("query", str("Search query"), "category", strEnum("Optional category filter", "project", "user", "task", "general"), "project", str("Optional project filter"), "task", str("Optional task filter"), "tags", arr("Optional tags filter (ALL must match)"), "limit", intSchema("Max results, default 20")), "query")},
		{Name: "memory_list", Description: "List memory entries. Target \"primary\" lists the always-injected working set; target \"fragments\" lists the searchable archive (with optional metadata filters).", InputSchema: obj("object", props("target", strEnum("List target: \"primary\" or \"fragments\" (default)", "primary", "fragments"), "category", strEnum("Optional fragment category filter", "project", "user", "task", "general"), "project", str("Optional fragment project filter"), "limit", intSchema("Max fragment results, default 50")))},
		{Name: "memory_delete", Description: "Delete a memory fragment by id. Primary memory entries cannot be deleted (use memory_replace to update or memory_demote to move to fragments).", InputSchema: obj("object", props("id", str("Fragment id")), "id")},
		{Name: "memory_promote", Description: "Promote a fragment into primary memory (the always-injected working set, ~1k token cap). Use when a fragment contains a durable, frequently-needed fact. Background review agent only.", InputSchema: obj("object", props("id", str("Fragment id to promote")), "id")},
		{Name: "memory_demote", Description: "Demote a primary memory entry back to fragments. Use when a primary entry is stale or no longer frequently needed. Background review agent only.", InputSchema: obj("object", props("old_text", str("Substring of the primary entry to demote")), "old_text")},
		{Name: "todo", Description: "Replace the conversation task checklist (full replace, Claude TodoWrite style). Empty items clears the list. The user can delete items from the UI — treat deleted items as gone and do not re-add them. The optional `goal` argument sets a structured brief that survives compaction. Format: three short lines — Want: (what the user asked for, in their words), Plan: (key steps/approach), Done: (acceptance criteria — what the finished result looks like). Set once at the start of a task; do not repeat on every call. The current hydration checkpoint is reused until compaction; the goal remains in tool history and is included in the fresh post-compaction checkpoint.", InputSchema: obj("object", props("items", arrObj("Full replacement list of todo items (max 50)", props("id", str("Stable item id (unique within the list)"), "content", str("Short task description (max 500 chars)"), "status", strEnum("Item status; prefer exactly one in_progress at a time", "pending", "in_progress", "completed")), "id", "content", "status"), "goal", str("Structured brief: 'Want: <user intent in their words>\\nPlan: <key steps/approach>\\nDone: <acceptance criteria — what the finished result looks like>'. Set once at task start; survives compaction. Max ~10000 tokens.")), "items")},
		{Name: "ask_question", Description: "Pause and ask the user a structured clarifying question before continuing. Use only for genuine decisions the user must make — not for things you can figure out yourself. The user can answer via options or free text (when allowed). The turn blocks until the user answers or cancels.", InputSchema: obj("object", props("question", str("The question to show the user"), "options", arrObj("Selectable choices (1-8). Mark one default when possible.", props("id", str("Stable option id"), "label", str("Short option label"), "description", str("Optional one-line explanation"), "default", obj("boolean", nil), "icon", str("Optional emoji or short icon glyph"), "image", str("Optional image URL or compact data URI")), "id", "label"), "allow_free_text", obj("boolean", nil), "multi_select", obj("boolean", nil)), "question", "options")},
		{Name: "docs_search", Description: "Search the NusaShell documentation corpus.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results, default 10")), "query")},
		{Name: "docs_read", Description: "Read a documentation page by id (see docs_search results).", InputSchema: obj("object", props("id", str("Documentation page id")), "id")},
		{Name: "ci_pipeline_list", Description: "List pipeline definitions in the current workspace (.nusashell/pipeline.yaml).", InputSchema: obj("object", props("workspace", str("Workspace path")))},
		{Name: "ci_pipeline_read", Description: "Read and validate the workspace pipeline definition.", InputSchema: obj("object", props("workspace", str("Workspace path")))},
		{Name: "ci_pipeline_validate", Description: "Validate pipeline YAML and report structured errors.", InputSchema: obj("object", props("yaml", str("Pipeline YAML"), "workspace", str("Workspace path")))},
		{Name: "ci_run", Description: "Start a pipeline or saved automation. Set async=true to return immediately with a run_id while the pipeline runs in the background; then use ci_wait or ci_run_status to check on it. Without async, the call blocks until the pipeline finishes.", InputSchema: obj("object", props("workspace", str("Workspace path for .nusashell/pipeline.yaml"), "workflow_id", str("Saved automation id"), "async", obj("boolean", nil)))},
		{Name: "ci_wait", Description: "Block until a pipeline run reaches a terminal state (done, failed, cancelled, blocked) or the timeout expires. Use after ci_run with async=true. Returns the final run status and summary.", InputSchema: obj("object", props("run_id", str("Run id"), "timeout_ms", intSchema("Max wait in milliseconds (default 300000 = 5 min, max 3600000 = 1 h)")), "run_id")},
		{Name: "ci_run_status", Description: "Return run status, DAG summary, and failed jobs. Use this after ci_run; do not fetch full logs unless a job failed.", InputSchema: obj("object", props("run_id", str("Run id")), "run_id")},
		{Name: "ci_logs", Description: "Retrieve job logs (tail 200 by default). Prefer failed jobs.", InputSchema: obj("object", props("job_id", str("Job run id"), "run_id", str("Run id"), "limit", intSchema("Max chunks")), "job_id")},
		{Name: "ci_cancel", Description: "Cancel a running pipeline/automation run.", InputSchema: obj("object", props("run_id", str("Run id")), "run_id")},
		{Name: "ci_steer", Description: "Send additional instructions to a running agent step without canceling.", InputSchema: obj("object", props("run_id", str("Run id"), "text", str("Steer instructions")), "run_id", "text")},
		{Name: "automation_list", Description: "List saved automations with availability (runnable/blocked/disabled).", InputSchema: obj("object", nil)},
		{Name: "automation_read", Description: "Read one automation definition and capability bindings.", InputSchema: obj("object", props("id", str("Automation id")), "id")},
		{Name: "automation_validate", Description: "Validate a workflow YAML. Distinguishes INVALID vs BLOCKED (provider disabled/missing).", InputSchema: obj("object", props("yaml", str("Workflow YAML")), "yaml")},
		{Name: "automation_create", Description: "Create an automation from YAML (once/every/when triggers). NusaShell owns durable scheduling — do not keep timers yourself.", InputSchema: obj("object", props("yaml", str("Workflow YAML"), "enabled", obj("boolean", nil)), "yaml")},
		{Name: "automation_enable", Description: "Enable an automation and restore event subscriptions/schedules.", InputSchema: obj("object", props("id", str("Automation id")), "id")},
		{Name: "automation_disable", Description: "Disable an automation without deleting it.", InputSchema: obj("object", props("id", str("Automation id")), "id")},
		{Name: "automation_status", Description: "Inspect a run, including waiting/blocked states.", InputSchema: obj("object", props("run_id", str("Run id")), "run_id")},
		{Name: "schedule_once", Description: "Create a one-shot scheduled automation at an RFC3339 timestamp.", InputSchema: obj("object", props("at", str("RFC3339 time"), "yaml", str("Jobs YAML or a full workflow"), "name", str("Automation name")), "at")},
		{Name: "schedule_every", Description: "Create a recurring automation. cron uses calendar semantics; interval uses elapsed time. They are not equivalent.", InputSchema: obj("object", props("cron", str("5-field cron"), "interval", str("Go duration such as 1h"), "timezone", str("IANA timezone"), "yaml", str("Jobs YAML"), "name", str("Name")))},
		{Name: "wait_until", Description: "Explain or create a durable wait_until step. Waiting never keeps a runner occupied.", InputSchema: obj("object", props("at", str("RFC3339 time")), "at")},
		{Name: "sleep", Description: "Pause for the given number of seconds (max 300). Use for retry backoff or to wait between polls of an async ci_run. Does not consume a provider round — the turn resumes after the pause.", InputSchema: obj("object", props("seconds", intSchema("Seconds to sleep (1-300)")), "seconds")},
		{Name: "mcp_list", Description: "List configured MCP servers with their enabled status and runtime state (running/stopped).", InputSchema: obj("object", nil)},
		{Name: "tool_list", Description: "List tools from a running MCP server by name (names and descriptions, plus optional input schemas). When the server is omitted, lists tools across all running MCP servers.", InputSchema: obj("object", props("server", str("Optional MCP server name; when omitted, lists tools across all running servers")))},
		{Name: "tool_search", Description: "Search a running MCP server's tools by name or description (case-insensitive token match — any term matches). Returns matching tool names and descriptions.", InputSchema: obj("object", props("server", str("MCP server name"), "query", str("Search query")), "server", "query")},
		{Name: "tool_schema", Description: "Load one MCP tool's input schema by server and tool name. Useful when you need the exact argument shape before calling an mcp__<server>__<tool> tool.", InputSchema: obj("object", props("server", str("MCP server name"), "tool", str("Tool name within the server")), "server", "tool")},
		{Name: "mcp_register", Description: "Copy a new MCP plugin from an absolute staging folder into the installed plugin store, or replace an existing plugin with the same id. The source must contain manifest.json and must stay outside the installed plugins root. Check mcp_list and ask the user before replacing an existing id; then call mcp_enable.", InputSchema: obj("object", props("source", str("Absolute staging path to the plugin folder containing manifest.json")), "source")},
		{Name: "mcp_enable", Description: "Start/connect an MCP plugin and load its tools (alias of plugin.test). The plugin must be registered first (mcp_register or the Plugins view).", InputSchema: obj("object", props("id", str("Plugin id (e.g. nusashell.files)")), "id")},
		{Name: "mcp_disable", Description: "Stop/disconnect an MCP plugin. The definition stays installed; only the MCP subprocess is stopped. Tools from this server are no longer listed.", InputSchema: obj("object", props("id", str("Plugin id")), "id")},
		{Name: "mcp_unregister", Description: "Remove an MCP plugin entirely and delete its installed folder. Ask the user for confirmation first. Use mcp_disable when the plugin only needs to stop.", InputSchema: obj("object", props("id", str("Plugin id")), "id")},
		{Name: "mcp_install", Description: "Install an MCP plugin from the curated catalog or a GitHub repository (owner/repo or URL). After install, call mcp_enable with the resulting plugin id to connect and load its tools.", InputSchema: obj("object", props("source", strEnum("Install source", "catalog", "github"), "id", str("Catalog plugin id (required when source=catalog)"), "url", str("GitHub repo URL or owner/repo shorthand (required when source=github)"), "subdir", str("Optional subdirectory inside a monorepo (github)"), "ref", str("Optional branch or tag to pin (github)")), "source")},
		{Name: "mcp_server_add", Description: "Register a manual MCP server (no manifest needed) by command/args/env — e.g. npx servers. Use for generic MCP servers; use mcp_register for NusaShell plugin folders. After adding, call mcp_enable with the server id to connect and load its tools.", InputSchema: obj("object", props("name", str("Human-readable server name"), "command", str("Command to launch the server (e.g. npx, node, python)"), "args", arr("Arguments (e.g. -y @modelcontextprotocol/server-github)"), "env", obj("object", props("additional", str("KEY=VALUE entries")), "additional"), "id", str("Optional stable id (default auto-generated)")), "name", "command")},
		{Name: "read_image", Description: "Load an image from the conversation into your context so you can see it. Pass file_path (the absolute path shown in the image placeholder). When your active model supports vision, the image is attached to your context directly. For non-vision models, the image is described using a vision fallback model and the text description is returned.", InputSchema: obj("object", props("file_path", str("Absolute path of the image file on disk (shown in the image placeholder)"), "question", str("Optional question about the image")), "file_path")},
		{Name: "read_audio", Description: "Load an audio file from the conversation into your context so you can hear it. Pass file_path (the absolute path shown in the audio placeholder). When your active model supports audio input, the audio is attached to your context directly. For non-audio models, the audio is transcribed/described using an audio fallback model and the text transcript is returned.", InputSchema: obj("object", props("file_path", str("Absolute path of the audio file on disk"), "question", str("Optional question about the audio")), "file_path")},
		{Name: "read_video", Description: "Load a video file from the conversation into your context so you can see it. Pass file_path (the absolute path shown in the video placeholder). When your active model supports video input, the video is attached to your context directly. For non-video models, the video is described using a video fallback model and the text description is returned.", InputSchema: obj("object", props("file_path", str("Absolute path of the video file on disk"), "question", str("Optional question about the video")), "file_path")},
		{Name: "web_search", Description: "Search the web for fresh information. Returns ranked results with title, URL, and snippet from multiple sources (Brave, Startpage, Wikipedia, GitHub). Use this when you need current information, documentation, or research. Follow up with web_fetch on promising URLs for full page content.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results (default 10)")), "query")},
		{Name: "web_fetch", Description: "Fetch a URL and return readable text (HTML stripped to title + visible text). Use after web_search to read full page content from a result URL. Accepts http/https only.", InputSchema: obj("object", props("url", str("URL to fetch"), "max_bytes", intSchema("Optional max bytes of extracted text (default 2MB)")), "url")},
	}
	if t.Artifacts != nil {
		tools = append(tools,
			application.ToolInfo{Name: "artifact_create", Description: "Create a new interactive artifact (HTML/CSS/JS) rendered in a sandboxed iframe in the UI. Use for prototypes, minigames, dashboards, visualizations, or any interactive content that mermaid or tables cannot express. External resources (CDNs, <script src>, <img>, <video>) are allowed — prefer reusing CDNs over inlining large libraries to save tokens. Max 64k tokens total. width and height are required (pixels) and control the iframe viewport. Recommended sizes: 640x480 for prototypes/games, 720x400 for dashboards/charts, 360x480 for widgets/calculators, 640x600 for tall content (timelines, lists).", InputSchema: obj("object", props("html", str("HTML body content (or a full <!DOCTYPE html> document)"), "css", str("Optional CSS, inlined into the document <head>"), "js", str("Optional JavaScript, inlined at end of <body>"), "title", str("Artifact title shown in the card header"), "width", intSchema("Required iframe width in pixels (e.g. 640 for prototypes, 720 for dashboards, 360 for widgets)"), "height", intSchema("Required iframe height in pixels (e.g. 480 for games, 400 for dashboards, 600 for tall content)")), "html", "width", "height")},
			application.ToolInfo{Name: "artifact_update", Description: "Update an existing artifact by id. Only the fields you pass are replaced; omitted fields keep their current value. Use this for small edits instead of re-outputting the whole artifact.", InputSchema: obj("object", props("id", str("Artifact id from artifact_create result"), "html", str("New HTML body (omit to keep current)"), "css", str("New CSS (omit to keep current)"), "js", str("New JS (omit to keep current)"), "title", str("New title (omit to keep current)")), "id")},
			application.ToolInfo{Name: "artifact_read", Description: "Read an existing artifact's full content by id.", InputSchema: obj("object", props("id", str("Artifact id")), "id")},
			application.ToolInfo{Name: "artifact_list", Description: "List artifacts in the current conversation with id, title, and updated_at.", InputSchema: obj("object", nil)},
			application.ToolInfo{Name: "artifact_delete", Description: "Delete an artifact by id.", InputSchema: obj("object", props("id", str("Artifact id")), "id")},
		)
	}
	if t.Acp != nil && len(t.Acp.EnabledAcpAgents()) > 0 {
		tools = append(tools,
			application.ToolInfo{Name: "subagent", Description: "Delegate a self-contained task to a configured ACP coding agent (Cursor, Claude Code, Codex CLI, etc). The ACP agent does not receive this conversation, MCP plugins, or NusaShell tools. Pass a compact brief with absolute paths. Always async: returns immediately with run ids (status \"starting\"); the tool call stays \"running\" until the subagent finishes, then is updated with the subagent's text summary and a new turn is triggered so you process the result. count (1-6) fans the same brief out to parallel sessions.", InputSchema: obj("object", props("prompt", str("Self-contained task brief"), "agent_id", str("Optional ACP agent id from Providers; omit to use the default enabled agent"), "workspace", str("Optional absolute workspace path (defaults to the conversation workspace)"), "mode_id", str("Optional ACP session mode id advertised by the agent"), "model_id", str("Optional ACP model id advertised by the agent"), "count", intSchema("Number of parallel spawns of the same brief (1-6, default 1)")), "prompt")},
			application.ToolInfo{Name: "subagent_steer", Description: "Send an additional instruction to a live ACP subagent without cancelling it. Applied at the next prompt boundary.", InputSchema: obj("object", props("id", str("ACP run id from subagent"), "text", str("Steer instruction")), "id", "text")},
			application.ToolInfo{Name: "subagent_stop", Description: "Cancel a live ACP subagent run.", InputSchema: obj("object", props("id", str("ACP run id")), "id")},
			application.ToolInfo{Name: "subagent_wait", Description: "Wait for an async ACP subagent run to finish.", InputSchema: obj("object", props("id", str("ACP run id"), "timeout_ms", intSchema("Optional wait timeout in milliseconds")), "id")},
		)
	}
	// MCP plugin tools are NOT advertised to the agent. The tool list must
	// stay stable for the lifetime of a conversation so the provider prompt
	// cache (OpenAI / Claude) is not invalidated. The agent can still inspect
	// MCP servers via mcp_list, tool_list, tool_search, and tool_schema, but
	// cannot call mcp__<server>__<tool> directly. MCP tools are available to
	// pipeline workflow steps (capability resolution) and the Plugins UI.
	if sw := t.webAnswerSearcher(); sw != nil && sw.CanAnswer() {
		providers := sw.AvailableAnswerProviders()
		providerList := strings.Join(providers, ", ")
		desc := "Get a web-grounded answer to a question using an LLM with built-in web search. Use when you need a synthesized answer rather than raw search results. Available providers: " + providerList + ". Omit provider to use the default priority order."
		tools = append(tools, application.ToolInfo{
			Name:        "web_answer",
			Description: desc,
			InputSchema: obj("object", props("question", str("Question to answer"), "provider", str("Optional provider: "+providerList)), "question"),
		})
	}
	return tools
}

func (t *Toolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	switch {
	case name == "skill_list":
		var args struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 100
		}
		skills := t.Skills.List()
		if limit < len(skills) {
			skills = skills[:limit]
		}
		type skillItem struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description,omitempty"`
		}
		items := make([]skillItem, 0, len(skills))
		for _, s := range skills {
			items = append(items, skillItem{Name: s.Name, Description: s.Description})
		}
		return yamlBlock(map[string]any{"count": len(items), "skills": items}), nil

	case name == "skill_search":
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		q := strings.ToLower(args.Query)
		type skillMatch struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description,omitempty"`
		}
		var matches []skillMatch
		for _, s := range t.Skills.List() {
			if !strings.Contains(strings.ToLower(s.Name+" "+s.Description), q) {
				continue
			}
			matches = append(matches, skillMatch{Name: s.Name, Description: s.Description})
			if len(matches) >= limit {
				break
			}
		}
		return yamlBlock(map[string]any{"query": args.Query, "count": len(matches), "matches": matches}), nil

	case name == "skill_read":
		var args struct {
			Name     string `json:"name"`
			Path     string `json:"path"`
			Offset   int    `json:"offset"`
			MaxChars int    `json:"max_chars"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Name) == "" {
			return "", fmt.Errorf("name is required (use skill_list to see available skills)")
		}
		if r, ok := t.Skills.(interface {
			ReadFile(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error)
		}); ok {
			file, err := r.ReadFile(args.Name, "", args.Path, args.Offset, args.MaxChars)
			if err != nil {
				// Legacy stores without real skill folders report unsupported;
				// fall back to SKILL.md Content so old behavior is preserved.
				msg := err.Error()
				if strings.Contains(msg, "not supported") || strings.Contains(msg, "unsupported") {
					for _, s := range t.Skills.List() {
						if s.Name == args.Name || s.ID == args.Name {
							return s.Content, nil
						}
					}
					return "", fmt.Errorf("skill %q not found; use skill_list or skill_search to see available skills", args.Name)
				}
				return "", fmt.Errorf("skill_read: %w", err)
			}
			var sb strings.Builder
			if file.Editable {
				sb.WriteString(file.Content)
			} else {
				sb.WriteString(fmt.Sprintf("[%s is not editable text; %d bytes]", file.Path, file.SizeBytes))
			}
			if file.Truncated {
				sb.WriteString(fmt.Sprintf("\n… [truncated — continue with offset=%d]", file.NextOffset))
			}
			meta := map[string]any{
				"skill":     args.Name,
				"path":      file.Path,
				"editable":  file.Editable,
				"truncated": file.Truncated,
			}
			if file.Truncated {
				meta["next_offset"] = file.NextOffset
			}
			return yamlMD(meta, sb.String()), nil
		}
		// Fallback: legacy Content-only stores (e.g. jsonstore or test stubs).
		for _, s := range t.Skills.List() {
			if s.Name == args.Name || s.ID == args.Name {
				return yamlMD(map[string]any{"skill": s.Name, "source": "legacy"}, s.Content), nil
			}
		}
		return "", fmt.Errorf("skill %q not found; use skill_list or skill_search to see available skills", args.Name)

	case name == "skill_files":
		var args struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Name) == "" {
			return "", fmt.Errorf("name is required")
		}
		if f, ok := t.Skills.(interface {
			Files(id, ownedBy string) ([]domain.SkillFileEntry, error)
		}); ok {
			entries, err := f.Files(args.Name, "")
			if err != nil {
				msg := err.Error()
				if strings.Contains(msg, "not supported") || strings.Contains(msg, "unsupported") {
					return "", fmt.Errorf("skill store does not support file listing")
				}
				return "", fmt.Errorf("skill_files: %w", err)
			}
			type fileEntry struct {
				Type      string `yaml:"type"`
				Path      string `yaml:"path"`
				SizeBytes int64  `yaml:"size,omitempty"`
				Editable  bool   `yaml:"editable,omitempty"`
			}
			entries2 := make([]fileEntry, 0, len(entries))
			for _, e := range entries {
				entries2 = append(entries2, fileEntry{Type: e.Type, Path: e.Path, SizeBytes: e.SizeBytes, Editable: e.Editable})
			}
			return yamlBlock(map[string]any{"skill": args.Name, "count": len(entries2), "files": entries2}), nil
		}
		return "", fmt.Errorf("skill store does not support file listing")

	case name == "skill_save":
		var args struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
			Content     string `json:"content"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return "", fmt.Errorf("skill name is required")
		}
		if strings.TrimSpace(args.Content) == "" {
			return "", fmt.Errorf("skill content is required")
		}
		// When path is set, write a support file (references/, templates/,
		// scripts/) inside an existing skill — mirrors skill_read's name+path
		// pattern. The skill must already exist; creation is not supported
		// in this mode.
		if path := strings.TrimSpace(args.Path); path != "" {
			if err := t.Skills.WriteFile(name, "", path, args.Content); err != nil {
				return "", fmt.Errorf("skill_save: %w", err)
			}
			return yamlBlock(map[string]any{"status": "saved", "skill": name, "path": path}), nil
		}
		var s *domain.Skill
		if args.ID != "" {
			existing, err := t.Skills.Get(args.ID, "")
			if err != nil {
				return "", fmt.Errorf("skill %q not found: %w", args.ID, err)
			}
			s = existing
		} else {
			s = &domain.Skill{
				ID:     domain.NewULID("skill"),
				State:  domain.SkillStateActive,
				Origin: domain.SkillOriginAgent,
			}
		}
		s.Name = name
		s.Description = strings.TrimSpace(args.Description)
		s.Content = args.Content
		s.UpdatedAt = time.Now().UTC()
		if err := t.Skills.Save(s); err != nil {
			return "", err
		}
		return yamlBlock(map[string]any{"status": "saved", "skill": s.Name, "id": s.ID}), nil

	case name == "memory_save":
		var args struct {
			Content  string   `json:"content"`
			Category string   `json:"category"`
			Project  string   `json:"project"`
			Task     string   `json:"task"`
			Tags     []string `json:"tags"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Content) == "" {
			return "", fmt.Errorf("content is required")
		}
		if t.Fragments == nil {
			return "", fmt.Errorf("fragment store not configured")
		}
		frag := &domain.MemoryFragment{
			Category: args.Category,
			Project:  args.Project,
			Task:     args.Task,
			Tags:     args.Tags,
			Content:  args.Content,
			Source:   "agent",
		}
		if err := t.Fragments.Save(frag); err != nil {
			return "", err
		}
		return yamlBlock(map[string]any{"status": "saved", "fragment_id": frag.ID, "category": frag.Category}), nil

	case name == "memory_replace":
		var args struct {
			Target  string `json:"target"`
			OldText string `json:"old_text"`
			ID      string `json:"id"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Content) == "" {
			return "", fmt.Errorf("content is required")
		}
		switch args.Target {
		case "primary":
			if t.Primary == nil {
				return "", fmt.Errorf("primary store not configured")
			}
			if strings.TrimSpace(args.OldText) == "" {
				return "", fmt.Errorf("old_text is required for primary target")
			}
			if err := t.Primary.Replace(args.OldText, args.Content); err != nil {
				return "", err
			}
			return yamlBlock(map[string]any{"status": "replaced", "target": "primary", "matched": args.OldText}), nil
		case "fragment":
			if t.Fragments == nil {
				return "", fmt.Errorf("fragment store not configured")
			}
			if args.ID == "" {
				return "", fmt.Errorf("id is required for fragment target")
			}
			frag := t.Fragments.Get(args.ID)
			if frag == nil {
				return "", fmt.Errorf("fragment %s not found", args.ID)
			}
			frag.Content = args.Content
			if err := t.Fragments.Save(frag); err != nil {
				return "", err
			}
			return yamlBlock(map[string]any{"status": "updated", "target": "fragment", "id": args.ID}), nil
		default:
			return "", fmt.Errorf("target must be \"primary\" or \"fragment\"")
		}

	case name == "memory_search":
		var args struct {
			Query    string   `json:"query"`
			Category string   `json:"category"`
			Project  string   `json:"project"`
			Task     string   `json:"task"`
			Tags     []string `json:"tags"`
			Limit    int      `json:"limit"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if t.Fragments == nil {
			return "", fmt.Errorf("fragment store not configured")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		hits := t.Fragments.Search(domain.FragmentSearchFilter{
			Query:    args.Query,
			Category: args.Category,
			Project:  args.Project,
			Task:     args.Task,
			Tags:     args.Tags,
			Limit:    limit,
		})
		frags := make([]map[string]any, 0, len(hits))
		for _, h := range hits {
			frags = append(frags, formatFragment(h.Fragment, h.Score))
		}
		return yamlBlock(map[string]any{"query": args.Query, "count": len(frags), "fragments": frags}), nil

	case name == "memory_list":
		var args struct {
			Target   string `json:"target"`
			Category string `json:"category"`
			Project  string `json:"project"`
			Limit    int    `json:"limit"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		if args.Target == "primary" {
			if t.Primary == nil {
				return yamlBlock(map[string]any{"target": "primary", "count": 0, "error": "primary store not configured"}), nil
			}
			mem := t.Primary.Load()
			entries := make([]map[string]any, 0, len(mem.Entries))
			for _, e := range mem.Entries {
				entries = append(entries, map[string]any{"id": e.ID, "content": e.Content})
			}
			return yamlBlock(map[string]any{"target": "primary", "count": len(entries), "entries": entries}), nil
		}
		// Default: list fragments
		if t.Fragments == nil {
			return yamlBlock(map[string]any{"target": "fragments", "count": 0, "error": "fragment store not configured"}), nil
		}
		frags := t.Fragments.List(domain.FragmentSearchFilter{
			Category: args.Category,
			Project:  args.Project,
			Limit:    limit,
		})
		items := make([]map[string]any, 0, len(frags))
		for _, f := range frags {
			items = append(items, formatFragment(f, 0))
		}
		return yamlBlock(map[string]any{"target": "fragments", "count": len(items), "fragments": items}), nil

	case name == "memory_delete":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if t.Fragments == nil {
			return "", fmt.Errorf("fragment store not configured")
		}
		if err := t.Fragments.Delete(args.ID); err != nil {
			return "", err
		}
		return yamlBlock(map[string]any{"status": "deleted", "fragment_id": args.ID}), nil

	case name == "memory_promote":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if t.Primary == nil || t.Fragments == nil {
			return "", fmt.Errorf("primary or fragment store not configured")
		}
		frag := t.Fragments.Get(args.ID)
		if frag == nil {
			return "", fmt.Errorf("fragment %s not found", args.ID)
		}
		mem := t.Primary.Load()
		entries := mem.Entries
		// Avoid duplicate promotion: skip if an entry with the same ID
		// or content already exists in primary.
		for _, e := range entries {
			if e.ID == frag.ID || e.Content == frag.Content {
				return yamlBlock(map[string]any{"status": "already_promoted", "fragment_id": args.ID}), nil
			}
		}
		entries = append(entries, domain.PrimaryEntry{
			ID:        frag.ID,
			Content:   frag.Content,
			Source:    "agent",
			UpdatedAt: time.Now().UTC(),
		})
		if err := t.Primary.Update(entries); err != nil {
			return "", err
		}
		// Remove the fragment from the archive so the entry does not appear
		// in both primary and fragment tiers simultaneously.
		if err := t.Fragments.Delete(frag.ID); err != nil {
			return "", fmt.Errorf("promoted to primary but failed to delete fragment %s: %w", frag.ID, err)
		}
		return yamlBlock(map[string]any{"status": "promoted", "fragment_id": args.ID}), nil

	case name == "memory_demote":
		var args struct {
			OldText string `json:"old_text"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.OldText) == "" {
			return "", fmt.Errorf("old_text is required")
		}
		if t.Primary == nil || t.Fragments == nil {
			return "", fmt.Errorf("primary or fragment store not configured")
		}
		mem := t.Primary.Load()
		var kept []domain.PrimaryEntry
		var demoted *domain.PrimaryEntry
		for i := range mem.Entries {
			e := &mem.Entries[i]
			if demoted == nil && strings.Contains(e.Content, args.OldText) {
				demoted = e
				continue
			}
			kept = append(kept, *e)
		}
		if demoted == nil {
			return "", fmt.Errorf("no primary entry matching %q", args.OldText)
		}
		if err := t.Primary.Update(kept); err != nil {
			return "", err
		}
		// Save the demoted entry as a fragment so it stays searchable.
		frag := &domain.MemoryFragment{
			ID:        demoted.ID,
			Category:  domain.FragmentCategoryGeneral,
			Content:   demoted.Content,
			Source:    "agent",
			CreatedAt: demoted.UpdatedAt,
		}
		if err := t.Fragments.Save(frag); err != nil {
			return "", fmt.Errorf("demoted from primary but failed to save as fragment: %w", err)
		}
		return yamlBlock(map[string]any{"status": "demoted", "matched": args.OldText, "fragment_id": frag.ID}), nil

	case name == "todo":
		return t.execTodo(ctx, argsJSON)

	case name == "ask_question":
		return t.execAskQuestion(ctx, argsJSON)

	case name == "docs_search":
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 10
		}
		hits := t.Docs.Search(args.Query, limit)
		type docHit struct {
			ID    string `yaml:"id"`
			Title string `yaml:"title"`
			Path  string `yaml:"path"`
		}
		results := make([]docHit, 0, len(hits))
		for _, h := range hits {
			results = append(results, docHit{ID: h.ID, Title: h.Title, Path: h.Path})
		}
		return yamlBlock(map[string]any{"query": args.Query, "count": len(results), "results": results}), nil

	case name == "docs_read":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		doc, err := t.Docs.Read(args.ID)
		if err != nil {
			return "", fmt.Errorf("document %q not found; use docs_search first", args.ID)
		}
		return yamlMD(map[string]any{"id": doc.ID, "title": doc.Title, "path": doc.Path}, doc.Content), nil

	case name == "mcp_install":
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
			return "", fmt.Errorf("plugin installer not available")
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
		return yamlBlock(map[string]any{"status": "installed", "name": p.Manifest.Name, "id": p.Manifest.ID, "version": p.Manifest.Version, "next": "mcp_enable id=" + p.Manifest.ID}), nil

	case name == "mcp_server_add":
		var args struct {
			ID      string            `json:"id"`
			Name    string            `json:"name"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Name) == "" {
			return "", fmt.Errorf("name is required")
		}
		if strings.TrimSpace(args.Command) == "" {
			return "", fmt.Errorf("command is required")
		}
		if t.Plugins == nil {
			return "", fmt.Errorf("plugin store not available")
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			id = domain.NewID("mcp")
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
				Transport: domain.PluginTransportStdio,
				Command:   strings.TrimSpace(args.Command),
				Args:      args.Args,
				Env:       args.Env,
			},
		}}
		if err := t.Plugins.Save(p); err != nil {
			return "", fmt.Errorf("mcp_server_add: %w", err)
		}
		if dropper, ok := t.MCP.(interface{ Drop(string) }); ok {
			dropper.Drop(p.Manifest.MCPServerID())
		}
		return yamlBlock(map[string]any{"status": "added", "name": p.Manifest.Name, "id": p.Manifest.ID, "next": "mcp_enable id=" + p.Manifest.ID}), nil

	case name == "mcp_register":
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
			return "", fmt.Errorf("plugin store not available")
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
		return yamlBlock(map[string]any{"status": "registered", "name": p.Manifest.Name, "id": p.Manifest.ID, "version": p.Manifest.Version, "next": "mcp_enable id=" + p.Manifest.ID}), nil

	case name == "mcp_enable":
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
			return "", fmt.Errorf("plugin runtime not available")
		}
		p, err := t.Plugins.Get(args.ID)
		if err != nil {
			return "", fmt.Errorf("plugin %q not found; register it first with mcp_register", args.ID)
		}
		ctxConn, cancelConn := context.WithTimeout(ctx, 20*time.Second)
		defer cancelConn()
		tools, err := t.MCP.Connect(ctxConn, p)
		if err != nil {
			return "", fmt.Errorf("mcp_enable %q: %w", args.ID, err)
		}
		return yamlBlock(map[string]any{"status": "enabled", "id": args.ID, "tools": len(tools)}), nil

	case name == "mcp_disable":
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
			return "", fmt.Errorf("plugin runtime not available")
		}
		dropper, ok := t.MCP.(interface{ Drop(string) })
		if !ok {
			return "", fmt.Errorf("mcp runtime does not support disconnect")
		}
		dropper.Drop("plugin:" + args.ID)
		return yamlBlock(map[string]any{"status": "disabled", "id": args.ID}), nil

	case name == "mcp_unregister":
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
			return "", fmt.Errorf("plugin store not available")
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
		return yamlBlock(map[string]any{"status": "unregistered", "id": args.ID}), nil

	case name == "mcp_list":
		if t.Plugins == nil {
			return yamlBlock(map[string]any{"count": 0, "plugins": []any{}}), nil
		}
		plugins, err := t.Plugins.List()
		if err != nil {
			return "", fmt.Errorf("mcp_list: %w", err)
		}
		type srvInfo struct {
			ID      string `yaml:"id"`
			Name    string `yaml:"name"`
			Command string `yaml:"command,omitempty"`
			Running bool   `yaml:"running"`
			Tools   int    `yaml:"tools"`
		}
		out := make([]srvInfo, 0, len(plugins))
		for _, p := range plugins {
			toolCount := 0
			running := false
			serverID := p.Manifest.MCPServerID()
			if tools, ok := t.MCP.ToolsFor(serverID); ok {
				running = true
				toolCount = len(tools)
			}
			out = append(out, srvInfo{
				ID:      serverID,
				Name:    p.Manifest.Name,
				Command: p.Manifest.MCP.Command,
				Running: running,
				Tools:   toolCount,
			})
		}
		return yamlBlock(map[string]any{"count": len(out), "plugins": out}), nil

	case name == "tool_list":
		var args struct {
			Server string `json:"server"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		type toolEntry struct {
			Name        string         `yaml:"name"`
			Server      string         `yaml:"server"`
			Description string         `yaml:"description,omitempty"`
			InputSchema map[string]any `yaml:"input_schema,omitempty"`
		}
		var entries []toolEntry
		plugins, _ := t.Plugins.List()
		for _, p := range plugins {
			if args.Server != "" && p.Manifest.Name != args.Server {
				continue
			}
			tools, ok := t.MCP.ToolsFor(p.Manifest.MCPServerID())
			if !ok {
				continue
			}
			for _, tool := range tools {
				var schema map[string]any
				if len(tool.InputSchema) > 0 {
					_ = json.Unmarshal(tool.InputSchema, &schema)
				}
				entries = append(entries, toolEntry{
					Name:   "mcp__" + p.Manifest.Name + "__" + tool.Name,
					Server: p.Manifest.Name, Description: tool.Description, InputSchema: schema,
				})
			}
		}
		if entries == nil {
			entries = []toolEntry{}
		}
		return yamlBlock(map[string]any{"count": len(entries), "tools": entries}), nil

	case name == "tool_search":
		var args struct {
			Server string `json:"server"`
			Query  string `json:"query"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		type match struct {
			Name        string `yaml:"name"`
			Server      string `yaml:"server"`
			Description string `yaml:"description,omitempty"`
		}
		tokens := strings.Fields(strings.ToLower(args.Query))
		var matches []match
		plugins, _ := t.Plugins.List()
		for _, p := range plugins {
			if args.Server != "" && p.Manifest.Name != args.Server {
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
				matches = append(matches, match{
					Name:   "mcp__" + p.Manifest.Name + "__" + tool.Name,
					Server: p.Manifest.Name, Description: tool.Description,
				})
			}
		}
		if matches == nil {
			matches = []match{}
		}
		return yamlBlock(map[string]any{
			"server": args.Server, "query": args.Query,
			"count": len(matches), "matches": matches,
		}), nil

	case name == "tool_schema":
		var args struct {
			Server string `json:"server"`
			Tool   string `json:"tool"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		plugins, _ := t.Plugins.List()
		for _, p := range plugins {
			if p.Manifest.Name != args.Server {
				continue
			}
			tools, ok := t.MCP.ToolsFor(p.Manifest.MCPServerID())
			if !ok {
				return "", fmt.Errorf("server %q is not running; call mcp_list to see running servers", args.Server)
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
				return yamlBlock(map[string]any{
					"server": args.Server, "tool": args.Tool,
					"input_schema": schema,
				}), nil
			}
			return "", fmt.Errorf("tool %q not found on server %q; use tool_list or tool_search to see available tools", args.Tool, args.Server)
		}
		return "", fmt.Errorf("server %q not found; use mcp_list to see configured servers", args.Server)
	case name == "subagent":
		if t.Acp == nil {
			return "", fmt.Errorf("no ACP agents configured")
		}
		return t.Acp.SpawnSubagents(ctx, argsJSON)
	case name == "subagent_steer":
		if t.Acp == nil {
			return "", fmt.Errorf("no ACP agents configured")
		}
		return t.Acp.SteerAcpRun(ctx, argsJSON)
	case name == "subagent_stop":
		if t.Acp == nil {
			return "", fmt.Errorf("no ACP agents configured")
		}
		return t.Acp.StopAcpRun(ctx, argsJSON)
	case name == "subagent_wait":
		if t.Acp == nil {
			return "", fmt.Errorf("no ACP agents configured")
		}
		return t.Acp.WaitAcpRun(ctx, argsJSON)
	case name == "web_search":
		if t.Searcher == nil {
			return "", fmt.Errorf("search is not available")
		}
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 10
		}
		resp, err := t.Searcher.Search(ctx, args.Query)
		if err != nil {
			return "", fmt.Errorf("search failed: %w", err)
		}
		if limit > len(resp.Results) {
			limit = len(resp.Results)
		}
		type searchResult struct {
			Title   string   `yaml:"title"`
			URL     string   `yaml:"url"`
			Snippet string   `yaml:"snippet,omitempty"`
			Sources []string `yaml:"sources,omitempty"`
		}
		results := make([]searchResult, 0, limit)
		for _, r := range resp.Results[:limit] {
			results = append(results, searchResult{Title: r.Title, URL: r.URL, Snippet: r.Snippet, Sources: r.Sources})
		}
		meta := map[string]any{"query": resp.Query, "count": len(results)}
		if len(resp.Errors) > 0 {
			type srcErr struct {
				Source string `yaml:"source"`
				Error  string `yaml:"error"`
			}
			errs := make([]srcErr, 0, len(resp.Errors))
			for _, e := range resp.Errors {
				errs = append(errs, srcErr{Source: e.Source, Error: e.Error})
			}
			meta["errors"] = errs
		}
		return yamlBlock(meta), nil
	case name == "web_fetch":
		if t.Searcher == nil {
			return "", fmt.Errorf("search is not available")
		}
		var args struct {
			URL      string `json:"url"`
			MaxBytes int64  `json:"max_bytes"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.URL) == "" {
			return "", fmt.Errorf("url is required")
		}
		page, err := t.Searcher.FetchWithLimit(ctx, args.URL, args.MaxBytes)
		if err != nil {
			var httpErr *searchwire.HTTPError
			if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
				return "", fmt.Errorf("fetch failed: %w (retry after %ds)", err, httpErr.RetryAfter)
			}
			return "", fmt.Errorf("fetch failed: %w", err)
		}
		var sb strings.Builder
		meta := map[string]any{
			"url":          page.URL,
			"status":       page.StatusCode,
			"content_type": page.ContentType,
			"truncated":    page.Truncated,
		}
		if page.FinalURL != page.URL {
			meta["final_url"] = page.FinalURL
		}
		if page.Redirects > 0 {
			meta["redirects"] = page.Redirects
		}
		if page.Title != "" {
			meta["title"] = page.Title
		}
		if len(page.Links) > 0 {
			type linkEntry struct {
				Text string `yaml:"text,omitempty"`
				Href string `yaml:"href"`
			}
			links := make([]linkEntry, 0, len(page.Links))
			for _, l := range page.Links {
				if len(links) >= 50 {
					break
				}
				links = append(links, linkEntry{Text: strings.TrimSpace(l.Text), Href: l.Href})
			}
			meta["links_count"] = len(page.Links)
			meta["links"] = links
		}
		if page.Truncated {
			meta["bytes"] = page.Bytes
		}
		sb.WriteString(page.Text)
		return yamlMD(meta, sb.String()), nil
	case name == "web_answer":
		sw := t.webAnswerSearcher()
		if sw == nil || !sw.CanAnswer() {
			return "", fmt.Errorf("web_answer is not configured — set a provider and API key in Settings → Web Answer")
		}
		var args struct {
			Question string `json:"question"`
			Provider string `json:"provider"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Question) == "" {
			return "", fmt.Errorf("question is required")
		}
		opts := searchwire.AnswerOptions{Provider: strings.TrimSpace(args.Provider)}
		answer, err := sw.AnswerWithOptions(ctx, args.Question, opts)
		if err != nil {
			return "", fmt.Errorf("answer failed: %w", err)
		}
		return yamlMD(map[string]any{"provider": answer.Provider}, answer.Text), nil
	case name == "sleep":
		var args struct {
			Seconds int `json:"seconds"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if args.Seconds < 1 {
			return "", fmt.Errorf("seconds must be at least 1")
		}
		if args.Seconds > 300 {
			args.Seconds = 300
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(args.Seconds) * time.Second):
		}
		return yamlBlock(map[string]any{"status": "slept", "seconds": args.Seconds}), nil
	}

	// Artifact tools: interactive HTML/CSS/JS documents rendered in the UI.
	if out, handled, err := t.executeArtifact(ctx, name, argsJSON); handled {
		return out, err
	}

	if out, handled, err := t.executeAutomation(ctx, name, argsJSON); handled {
		return out, err
	}

	// MCP plugin tools: mcp__<server>__<tool>. These are NOT advertised in
	// ListTools() to keep the provider tool list stable (prompt cache), but
	// the agent can still discover them via tool_list / tool_schema and call
	// them by name. Execution validates against the connected MCP server.
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		plugins, _ := t.Plugins.List()
		plugin, toolName, ok := matchMCPTool(rest, plugins)
		if !ok {
			return "", fmt.Errorf("malformed mcp tool name: %s", name)
		}
		var args map[string]any
		if len(argsJSON) > 0 {
			if err := json.Unmarshal(argsJSON, &args); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}
		}
		return t.MCP.CallTool(ctx, plugin.Manifest.MCPServerID(), toolName, args)
	}

	return "", fmt.Errorf("unknown tool: %s", name)
}

// execTodo replaces the conversation todo checklist (full-replace, Claude
// TodoWrite style). Empty items clears the list. The optional `goal` argument
// stays visible in tool history while the hydration checkpoint is reused, then
// is included in the fresh checkpoint after compaction. Requires a conversation
// id in the context (set via WithConversationID by the turn runner).
const (
	todoMaxItems        = 50
	todoMaxContentChars = 500
	todoMaxGoalChars    = 40000 // ~10k tokens (4 chars/token average)
)

func (t *Toolbox) execTodo(ctx context.Context, argsJSON []byte) (string, error) {
	if t.Todos == nil {
		return "", fmt.Errorf("todo tracking is not available")
	}
	conversationID := application.ConversationIDFromContext(ctx)
	if conversationID == "" {
		return "", fmt.Errorf("todo tool requires a conversation context")
	}
	var args struct {
		Items []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
			Status  string `json:"status"`
		} `json:"items"`
		Goal string `json:"goal"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if len(args.Items) > todoMaxItems {
		return "", fmt.Errorf("items must have at most %d entries", todoMaxItems)
	}
	goal := strings.TrimSpace(args.Goal)
	if len(goal) > todoMaxGoalChars {
		return "", fmt.Errorf("goal exceeds %d chars (~10k tokens)", todoMaxGoalChars)
	}
	items := make([]domain.TodoItem, 0, len(args.Items))
	seenIDs := make(map[string]bool, len(args.Items))
	for _, raw := range args.Items {
		id := strings.TrimSpace(raw.ID)
		content := strings.TrimSpace(raw.Content)
		status := domain.TodoStatus(raw.Status)
		if id == "" {
			return "", fmt.Errorf("each item requires a non-empty id")
		}
		if seenIDs[id] {
			return "", fmt.Errorf("duplicate item id: %s", id)
		}
		seenIDs[id] = true
		if content == "" {
			return "", fmt.Errorf("each item requires non-empty content")
		}
		if len(content) > todoMaxContentChars {
			return "", fmt.Errorf("item content exceeds %d chars", todoMaxContentChars)
		}
		if !domain.IsValidTodoStatus(status) {
			return "", fmt.Errorf("item status must be pending, in_progress, or completed")
		}
		items = append(items, domain.TodoItem{ID: id, Content: content, Status: status})
	}
	t.Todos.Set(conversationID, items)
	if goal != "" {
		t.Todos.SetGoal(conversationID, goal)
	}
	current := t.Todos.Get(conversationID)
	currentGoal := t.Todos.GetGoal(conversationID)
	summary := domain.SummarizeTodos(current)
	type todoItemOut struct {
		ID      string `yaml:"id"`
		Content string `yaml:"content"`
		Status  string `yaml:"status"`
	}
	itemOuts := make([]todoItemOut, 0, len(current))
	for _, item := range current {
		itemOuts = append(itemOuts, todoItemOut{ID: item.ID, Content: item.Content, Status: string(item.Status)})
	}
	out := map[string]any{
		"ok":           true,
		"conversation": conversationID,
		"total":        summary.Total,
		"pending":      summary.Pending,
		"in_progress":  summary.InProgress,
		"completed":    summary.Completed,
		"items":        itemOuts,
	}
	if currentGoal != "" {
		out["goal"] = currentGoal
	}
	return yamlBlock(out), nil
}

// execAskQuestion pauses the turn and asks the user a structured clarifying
// question. The tool blocks until the UI answers via agent.ask.answer RPC or
// the turn is cancelled. Requires a run id and conversation id in the context.
func (t *Toolbox) execAskQuestion(ctx context.Context, argsJSON []byte) (string, error) {
	if t.AskQuestions == nil {
		return "", fmt.Errorf("ask_question is not available in this runtime")
	}
	runID := application.RunIDFromContext(ctx)
	if runID == "" {
		return "", fmt.Errorf("ask_question requires a running turn context")
	}
	conversationID := application.ConversationIDFromContext(ctx)
	var args struct {
		Question      string                     `json:"question"`
		Options       []domain.AskQuestionOption `json:"options"`
		AllowFreeText *bool                      `json:"allow_free_text"`
		MultiSelect   *bool                      `json:"multi_select"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	allowFreeText := true
	if args.AllowFreeText != nil {
		allowFreeText = *args.AllowFreeText
	}
	multiSelect := false
	if args.MultiSelect != nil {
		multiSelect = *args.MultiSelect
	}
	req, err := domain.ValidateAskQuestionRequest(args.Question, args.Options, allowFreeText, multiSelect)
	if err != nil {
		return "", err
	}
	// Generate a tool call ID if not available from context. The tool
	// execution framework passes the call ID separately; for now we use
	// a composite key from runID + question hash to avoid collisions.
	callID := application.ToolCallIDFromContext(ctx)
	if callID == "" {
		callID = domain.NewID("ask")
	}
	ch, err := t.AskQuestions.Ask(runID, callID, conversationID, req)
	if err != nil {
		return "", err
	}
	select {
	case result := <-ch:
		if !result.OK {
			return "", fmt.Errorf("%s", result.Answer)
		}
		return yamlBlock(map[string]any{
			"ok":     true,
			"via":    result.Via,
			"answer": result.Answer,
		}), nil
	case <-ctx.Done():
		t.AskQuestions.Cancel(runID, callID, "Agent turn cancelled")
		return "", ctx.Err()
	}
}

// ---- json schema helpers ----

func obj(typ string, properties map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": typ}
	if len(properties) > 0 {
		m["properties"] = properties
	} else if typ == "object" {
		// Some providers (e.g. Bedrock) reject object schemas without a
		// "properties" key. Emit an empty object to stay OpenAI-compatible.
		m["properties"] = map[string]any{}
	}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func props(entries ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(entries); i += 2 {
		name, _ := entries[i].(string)
		out[name] = entries[i+1]
	}
	return out
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func arr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// arrObj builds an array-of-objects JSON schema with the given item
// properties, required fields, and description.
func arrObj(desc string, properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       obj("object", properties, required...),
	}
}

// strEnum builds a string schema restricted to the given enum values.
func strEnum(desc string, values ...string) map[string]any {
	enums := make([]any, len(values))
	for i, v := range values {
		enums[i] = v
	}
	return map[string]any{"type": "string", "description": desc, "enum": enums}
}

// matchMCPTool resolves mcp__<server>__<tool> against installed plugins,
// preferring the longest server-name prefix so names that contain "__" still
// route to the right plugin. The plugin's manifest name is the MCP server
// name used in tool naming.
func matchMCPTool(rest string, plugins []*domain.Plugin) (*domain.Plugin, string, bool) {
	var best *domain.Plugin
	bestTool := ""
	for _, p := range plugins {
		name := p.Manifest.Name
		prefix := name + "__"
		toolName, ok := strings.CutPrefix(rest, prefix)
		if !ok || toolName == "" {
			continue
		}
		if best == nil || len(name) > len(best.Manifest.Name) {
			best = p
			bestTool = toolName
		}
	}
	if best == nil {
		return nil, "", false
	}
	return best, bestTool, true
}

func (t *Toolbox) executeAutomation(ctx context.Context, name string, argsJSON []byte) (string, bool, error) {
	if t.Automation == nil {
		switch name {
		case "ci_pipeline_list", "ci_pipeline_read", "ci_pipeline_validate", "ci_run", "ci_wait", "ci_run_status",
			"ci_logs", "ci_cancel", "ci_steer", "automation_list", "automation_read", "automation_validate",
			"automation_create", "automation_enable", "automation_disable", "automation_status",
			"schedule_once", "schedule_every", "wait_until":
			return "", true, fmt.Errorf("automation is not configured")
		default:
			return "", false, nil
		}
	}
	a := t.Automation
	var args map[string]any
	_ = json.Unmarshal(argsJSON, &args)
	str := func(k string) string {
		v, _ := args[k].(string)
		return v
	}
	encode := func(v any, err error) (string, bool, error) {
		if err != nil {
			return "", true, err
		}
		return yamlBlock(v), true, nil
	}
	switch name {
	case "ci_pipeline_list", "ci_pipeline_read":
		w, r, err := a.ReadPipeline(ctx, str("workspace"))
		if err != nil {
			return "", true, err
		}
		return encode(map[string]any{"name": w.Name, "jobs": w.JobIDs(), "validation": r.Verdict()}, nil)
	case "ci_pipeline_validate", "automation_validate":
		raw := []byte(str("yaml"))
		if len(raw) == 0 && str("workspace") != "" {
			_, r, err := a.ReadPipeline(ctx, str("workspace"))
			return encode(r, err)
		}
		r, _ := a.ValidateYAML(raw)
		return encode(r, nil)
	case "ci_run":
		async, _ := args["async"].(bool)
		if id := str("workflow_id"); id != "" {
			var run *domain.WorkflowRun
			var err error
			if async {
				run, err = a.RunWorkflowAsync(ctx, id, "agent")
			} else {
				run, err = a.RunWorkflow(ctx, id, "agent")
			}
			return encode(run, err)
		}
		var run *domain.WorkflowRun
		var err error
		if async {
			run, err = a.StartPipelineAsync(ctx, str("workspace"), "agent")
		} else {
			run, err = a.StartPipeline(ctx, str("workspace"), "agent")
		}
		return encode(run, err)
	case "ci_wait":
		timeoutMs, _ := args["timeout_ms"].(float64)
		timeout := 5 * time.Minute
		if timeoutMs > 0 {
			timeout = time.Duration(timeoutMs) * time.Millisecond
		}
		if timeout > time.Hour {
			timeout = time.Hour
		}
		run, err := a.WaitRun(ctx, str("run_id"), timeout)
		if err != nil {
			return "", true, err
		}
		sum := run.Summary()
		return encode(map[string]any{
			"run_id": run.ID, "status": run.Status, "wake_at": run.WakeAt,
			"summary": sum, "blocked_reason": run.BlockedReason,
			"timed_out": !run.Status.IsTerminal(),
		}, nil)
	case "ci_run_status", "automation_status":
		run, err := a.Runs.Get(ctx, str("run_id"))
		if err != nil {
			return "", true, err
		}
		sum := run.Summary()
		return encode(map[string]any{
			"run_id": run.ID, "status": run.Status, "wake_at": run.WakeAt,
			"summary": sum, "blocked_reason": run.BlockedReason,
		}, nil)
	case "ci_logs":
		chunks, err := a.Logs.Read(ctx, str("job_id"), 0, 200)
		return encode(chunks, err)
	case "ci_cancel":
		return encode(map[string]bool{"ok": true}, a.Exec.Cancel(ctx, str("run_id")))
	case "ci_steer":
		runID := str("run_id")
		text := str("text")
		run, err := a.Runs.Get(ctx, runID)
		if err != nil {
			return "", true, fmt.Errorf("run not found: %w", err)
		}
		var convID string
		for _, j := range run.Jobs {
			for _, s := range j.Steps {
				if s.Status == domain.StatusRunning && s.ConversationID != "" {
					convID = s.ConversationID
				}
			}
		}
		if convID == "" {
			return "", true, fmt.Errorf("no running agent step to steer")
		}
		if t.Steerer == nil {
			return "", true, fmt.Errorf("steer is not configured")
		}
		if err := t.Steerer.SteerHeadlessTurn(convID, text); err != nil {
			return "", true, err
		}
		return encode(map[string]any{"steered": true, "conversation_id": convID}, nil)
	case "automation_list":
		list, err := a.Workflows.List(ctx)
		if err != nil {
			return "", true, err
		}
		type row struct {
			ID, Name, Availability string
			Enabled                bool
		}
		var out []row
		for _, w := range list {
			avail, _ := a.AvailabilityOf(ctx, w)
			out = append(out, row{ID: w.ID, Name: w.Name, Enabled: w.Enabled, Availability: avail})
		}
		return encode(out, nil)
	case "automation_read":
		w, err := a.Workflows.Get(ctx, str("id"))
		if err != nil {
			return "", true, err
		}
		avail, reason := a.AvailabilityOf(ctx, w)
		caps := []any{}
		for _, name := range w.ReferencedCapabilities() {
			b, _ := a.Caps.Resolve(ctx, name, domain.DefaultAutoStart)
			caps = append(caps, b)
		}
		return encode(map[string]any{"workflow": w, "availability": avail, "reason": reason, "capabilities": caps}, nil)
	case "automation_create":
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			return "", true, err
		}
		if n := str("name"); n != "" {
			w.Name = n
		}
		w.Enabled = true
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	case "automation_enable", "automation_disable":
		w, err := a.Workflows.Get(ctx, str("id"))
		if err != nil {
			return "", true, err
		}
		w.Enabled = name == "automation_enable"
		if w.Enabled {
			err = a.Auto.EnableWorkflow(ctx, w)
		} else {
			err = a.Workflows.Put(ctx, w)
		}
		return encode(map[string]any{"id": w.ID, "enabled": w.Enabled}, err)
	case "schedule_once":
		at, err := time.Parse(time.RFC3339, str("at"))
		if err != nil {
			return "", true, fmt.Errorf("at must be RFC3339")
		}
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			w = &domain.WorkflowDefinition{Name: str("name"), Jobs: []domain.Job{{ID: "run", Steps: []domain.Step{{Run: "true"}}}}}
		}
		if w.Name == "" {
			w.Name = str("name")
		}
		if w.Name == "" {
			w.Name = "once"
		}
		w.Enabled = true
		w.Triggers = []domain.Trigger{{ID: "t1", Kind: domain.TriggerOnce, Family: domain.FamilyOnce, At: &at}}
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	case "schedule_every":
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			w = &domain.WorkflowDefinition{Name: str("name"), Jobs: []domain.Job{{ID: "run", Steps: []domain.Step{{Run: "true"}}}}}
		}
		if w.Name == "" {
			w.Name = str("name")
		}
		tr := domain.Trigger{ID: "t1", Family: domain.FamilyEvery, Timezone: str("timezone")}
		if str("cron") != "" {
			tr.Kind = domain.TriggerCron
			tr.Cron = str("cron")
		} else {
			d, err := time.ParseDuration(str("interval"))
			if err != nil {
				return "", true, fmt.Errorf("interval or cron is required")
			}
			tr.Kind = domain.TriggerInterval
			tr.Interval = d
		}
		w.Triggers = []domain.Trigger{tr}
		w.Enabled = true
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	case "wait_until":
		return encode(map[string]string{
			"hint": "Use a workflow step wait_until: <RFC3339>. The run enters waiting and resumes after restart.",
			"at":   str("at"),
		}, nil)
	default:
		return "", false, nil
	}
}

// formatMemoryEntry renders one memory entry as a YAML-mapped map for
// yamlBlock output. It surfaces id, created_at, source, tags, and content
// so the agent can reason about recency, provenance, and clustering.
func formatMemoryEntry(e *domain.MemoryEntry) map[string]any {
	source := e.Source
	if source == "" {
		source = "user"
	}
	target := e.Target
	if target == "" {
		target = domain.MemoryTargetMemory
	}
	m := map[string]any{
		"id":         e.ID,
		"target":     target,
		"created_at": e.CreatedAt.UTC().Format(time.RFC3339),
		"source":     source,
		"content":    e.Content,
	}
	if len(e.Tags) > 0 {
		m["tags"] = e.Tags
	}
	return m
}

// formatFragment renders one memory fragment as a YAML-mapped map for
// yamlBlock output. Score is included when non-zero (BM25 search results).
func formatFragment(f *domain.MemoryFragment, score float64) map[string]any {
	m := map[string]any{
		"id":         f.ID,
		"category":   f.Category,
		"updated_at": f.UpdatedAt.UTC().Format(time.RFC3339),
		"content":    f.Content,
	}
	if f.Project != "" {
		m["project"] = f.Project
	}
	if f.Task != "" {
		m["task"] = f.Task
	}
	if len(f.Tags) > 0 {
		m["tags"] = f.Tags
	}
	if score > 0 {
		m["score"] = score
	}
	return m
}
