// Package tools implements the agent's built-in toolbox (skill_*,
// memory_*, docs_*) plus dynamic mcp__<server>__<tool> tools.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
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
	Memory          application.MemoryStore
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
		{Name: "skill_save", Description: "Create or update a skill. When id is omitted a new skill is created; otherwise the existing skill with that id is updated. Skills should be reusable procedures or domain knowledge, not one-off task notes.", InputSchema: obj("object", props("id", str("Existing skill id to update (omit to create new)"), "name", str("Skill name (lowercase with hyphens, matches folder name)"), "description", str("Short description (max 1024 chars)"), "content", str("Full skill markdown content")), "name", "description", "content")},
		{Name: "skill_files", Description: "List the files inside an installed skill folder (SKILL.md + support files) with size and editability, to discover references/guides before skill_read.", InputSchema: obj("object", props("name", str("Skill id")), "name")},
		{Name: "memory_save", Description: "Save a fact to long-term memory with optional tags.", InputSchema: obj("object", props("content", str("Fact to remember"), "tags", arr("Optional tags")), "content")},
		{Name: "memory_search", Description: "Search memory entries by substring match over content and tags.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results, default 10")), "query")},
		{Name: "memory_list", Description: "List all memory entries.", InputSchema: obj("object", nil)},
		{Name: "memory_delete", Description: "Delete a memory entry by id.", InputSchema: obj("object", props("id", str("Memory entry id")), "id")},
		{Name: "todo", Description: "Replace the conversation task checklist (full replace, Claude TodoWrite style). Empty items clears the list. The user can delete items from the UI — treat deleted items as gone and do not re-add them.", InputSchema: obj("object", props("items", arrObj("Full replacement list of todo items (max 50)", props("id", str("Stable item id (unique within the list)"), "content", str("Short task description (max 500 chars)"), "status", strEnum("Item status; prefer exactly one in_progress at a time", "pending", "in_progress", "completed")), "id", "content", "status")), "items")},
		{Name: "ask_question", Description: "Pause and ask the user a structured clarifying question before continuing. Use only for genuine decisions the user must make — not for things you can figure out yourself. The user can answer via options or free text (when allowed). The turn blocks until the user answers or cancels.", InputSchema: obj("object", props("question", str("The question to show the user"), "options", arrObj("Selectable choices (1-8). Mark one default when possible.", props("id", str("Stable option id"), "label", str("Short option label"), "description", str("Optional one-line explanation"), "default", obj("boolean", nil), "icon", str("Optional emoji or short icon glyph"), "image", str("Optional image URL or compact data URI")), "id", "label"), "allow_free_text", obj("boolean", nil), "multi_select", obj("boolean", nil)), "question", "options")},
		{Name: "docs_search", Description: "Search the NusaShell documentation corpus.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results, default 10")), "query")},
		{Name: "docs_read", Description: "Read a documentation page by id (see docs_search results).", InputSchema: obj("object", props("id", str("Documentation page id")), "id")},
		{Name: "ci_pipeline_list", Description: "List pipeline definitions in the current workspace (.nusashell/pipeline.yaml).", InputSchema: obj("object", props("workspace", str("Workspace path")))},
		{Name: "ci_pipeline_read", Description: "Read and validate the workspace pipeline definition.", InputSchema: obj("object", props("workspace", str("Workspace path")))},
		{Name: "ci_pipeline_validate", Description: "Validate pipeline YAML and report structured errors.", InputSchema: obj("object", props("yaml", str("Pipeline YAML"), "workspace", str("Workspace path")))},
		{Name: "ci_run", Description: "Start a pipeline or saved automation.", InputSchema: obj("object", props("workspace", str("Workspace path for .nusashell/pipeline.yaml"), "workflow_id", str("Saved automation id")))},
		{Name: "ci_run_status", Description: "Return run status, DAG summary, and failed jobs. Use this after ci_run; do not fetch full logs unless a job failed.", InputSchema: obj("object", props("run_id", str("Run id")), "run_id")},
		{Name: "ci_logs", Description: "Retrieve job logs (tail 200 by default). Prefer failed jobs.", InputSchema: obj("object", props("job_id", str("Job run id"), "run_id", str("Run id"), "limit", intSchema("Max chunks")), "job_id")},
		{Name: "ci_cancel", Description: "Cancel a running pipeline/automation run.", InputSchema: obj("object", props("run_id", str("Run id")), "run_id")},
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
		{Name: "mcp_list", Description: "List configured MCP servers with their enabled status and runtime state (running/stopped).", InputSchema: obj("object", nil)},
		{Name: "tool_list", Description: "List tools from a running MCP server by name (names and descriptions, plus optional input schemas). When the server is omitted, lists tools across all running MCP servers.", InputSchema: obj("object", props("server", str("Optional MCP server name; when omitted, lists tools across all running servers")))},
		{Name: "tool_search", Description: "Search a running MCP server's tools by name or description (case-insensitive token match — any term matches). Returns matching tool names and descriptions.", InputSchema: obj("object", props("server", str("MCP server name"), "query", str("Search query")), "server", "query")},
		{Name: "tool_schema", Description: "Load one MCP tool's input schema by server and tool name. Useful when you need the exact argument shape before calling an mcp__<server>__<tool> tool.", InputSchema: obj("object", props("server", str("MCP server name"), "tool", str("Tool name within the server")), "server", "tool")},
		{Name: "mcp_register", Description: "Register a new MCP plugin from a local folder (or update an existing one). The folder must contain manifest.json. After registering, call mcp_enable to connect and load its tools.", InputSchema: obj("object", props("source", str("Absolute path to the plugin folder containing manifest.json")), "source")},
		{Name: "mcp_enable", Description: "Start/connect an MCP plugin and load its tools (alias of plugin.test). The plugin must be registered first (mcp_register or the Plugins view).", InputSchema: obj("object", props("id", str("Plugin id (e.g. nusashell.files)")), "id")},
		{Name: "mcp_disable", Description: "Stop/disconnect an MCP plugin. The definition stays installed; only the MCP subprocess is stopped. Tools from this server are no longer listed.", InputSchema: obj("object", props("id", str("Plugin id")), "id")},
		{Name: "mcp_unregister", Description: "Remove an MCP plugin entirely (deletes its folder under the plugins data dir). Use mcp_disable first if you only want to stop it.", InputSchema: obj("object", props("id", str("Plugin id")), "id")},
		{Name: "mcp_install", Description: "Install an MCP plugin from the curated catalog or a GitHub repository (owner/repo or URL). After install, call mcp_enable with the resulting plugin id to connect and load its tools.", InputSchema: obj("object", props("source", strEnum("Install source", "catalog", "github"), "id", str("Catalog plugin id (required when source=catalog)"), "url", str("GitHub repo URL or owner/repo shorthand (required when source=github)"), "subdir", str("Optional subdirectory inside a monorepo (github)"), "ref", str("Optional branch or tag to pin (github)")), "source")},
		{Name: "mcp_server_add", Description: "Register a manual MCP server (no manifest needed) by command/args/env — e.g. npx servers. Use for generic MCP servers; use mcp_register for NusaShell plugin folders. After adding, call mcp_enable with the server id to connect and load its tools.", InputSchema: obj("object", props("name", str("Human-readable server name"), "command", str("Command to launch the server (e.g. npx, node, python)"), "args", arr("Arguments (e.g. -y @modelcontextprotocol/server-github)"), "env", obj("object", props("additional", str("KEY=VALUE entries")), "additional"), "id", str("Optional stable id (default auto-generated)")), "name", "command")},
		{Name: "read_image", Description: "Load an image from the conversation into your context so you can see it. Pass file_path (the absolute path shown in the image placeholder). When your active model supports vision, the image is attached to your context directly. For non-vision models, the image is described using a vision fallback model and the text description is returned.", InputSchema: obj("object", props("file_path", str("Absolute path of the image file on disk (shown in the image placeholder)"), "question", str("Optional question about the image")), "file_path")},
		{Name: "read_audio", Description: "Load an audio file from the conversation into your context so you can hear it. Pass file_path (the absolute path shown in the audio placeholder). When your active model supports audio input, the audio is attached to your context directly. For non-audio models, the audio is transcribed/described using an audio fallback model and the text transcript is returned.", InputSchema: obj("object", props("file_path", str("Absolute path of the audio file on disk"), "question", str("Optional question about the audio")), "file_path")},
		{Name: "read_video", Description: "Load a video file from the conversation into your context so you can see it. Pass file_path (the absolute path shown in the video placeholder). When your active model supports video input, the video is attached to your context directly. For non-video models, the video is described using a video fallback model and the text description is returned.", InputSchema: obj("object", props("file_path", str("Absolute path of the video file on disk"), "question", str("Optional question about the video")), "file_path")},
		{Name: "web_search", Description: "Search the web for fresh information. Returns ranked results with title, URL, and snippet from multiple sources (Brave, Startpage, Wikipedia, GitHub). Use this when you need current information, documentation, or research. Follow up with web_fetch on promising URLs for full page content.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results (default 10)")), "query")},
		{Name: "web_fetch", Description: "Fetch a URL and return readable text (HTML stripped to title + visible text). Use after web_search to read full page content from a result URL. Accepts http/https only.", InputSchema: obj("object", props("url", str("URL to fetch"), "max_bytes", intSchema("Optional max bytes of extracted text (default 2MB)")), "url")},
	}
	if t.Acp != nil && len(t.Acp.EnabledAcpAgents()) > 0 {
		tools = append(tools,
			application.ToolInfo{Name: "subagent", Description: "Delegate a self-contained task to a configured ACP coding agent (Cursor, Claude Code, Codex CLI, etc). The ACP agent does not receive this conversation, MCP plugins, or NusaShell tools. Pass a compact brief with absolute paths. Set async true to return immediately with run ids; otherwise wait until each spawn finishes. count (1-6) fans the same brief out to parallel sessions.", InputSchema: obj("object", props("prompt", str("Self-contained task brief"), "agent_id", str("Optional ACP agent id from Providers; omit to use the default enabled agent"), "workspace", str("Optional absolute workspace path (defaults to the conversation workspace)"), "mode_id", str("Optional ACP session mode id advertised by the agent"), "model_id", str("Optional ACP model id advertised by the agent"), "async", obj("boolean", nil), "count", intSchema("Number of parallel spawns of the same brief (1-6, default 1)")), "prompt")},
			application.ToolInfo{Name: "subagent_steer", Description: "Send an additional instruction to a live ACP subagent without cancelling it. Applied at the next prompt boundary.", InputSchema: obj("object", props("id", str("ACP run id from subagent"), "text", str("Steer instruction")), "id", "text")},
			application.ToolInfo{Name: "subagent_stop", Description: "Cancel a live ACP subagent run.", InputSchema: obj("object", props("id", str("ACP run id")), "id")},
			application.ToolInfo{Name: "subagent_wait", Description: "Wait for an async ACP subagent run to finish.", InputSchema: obj("object", props("id", str("ACP run id"), "timeout_ms", intSchema("Optional wait timeout in milliseconds")), "id")},
		)
	}
	if t.Plugins != nil {
		plugins, _ := t.Plugins.List()
		for _, p := range plugins {
			// Dynamic parity: only tools of plugins that are explicitly
			// ENABLED (connected via mcp_enable / plugin.test) are exposed to
			// the agent. Idle plugins stay out of the tool definitions to
			// save tokens and match the NusaShell flow (mcp_list → mcp_enable).
			dt, ok := t.MCP.ToolsFor(p.Manifest.MCPServerID())
			if !ok {
				continue
			}
			for _, tool := range dt {
				var schema map[string]any
				if len(tool.InputSchema) > 0 {
					_ = json.Unmarshal(tool.InputSchema, &schema)
				}
				if schema == nil {
					schema = obj("object", nil)
				}
				tools = append(tools, application.ToolInfo{
					Name:        "mcp__" + p.Manifest.Name + "__" + tool.Name,
					Description: tool.Description,
					InputSchema: schema,
				})
			}
		}
	}
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
		var sb strings.Builder
		for _, s := range skills {
			sb.WriteString("- ")
			sb.WriteString(s.Name)
			if s.Description != "" {
				sb.WriteString(": ")
				sb.WriteString(s.Description)
			}
			sb.WriteString("\n")
		}
		if sb.Len() == 0 {
			return "No skills in the library.", nil
		}
		return strings.TrimSpace(sb.String()), nil

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
		var sb strings.Builder
		found := 0
		for _, s := range t.Skills.List() {
			if !strings.Contains(strings.ToLower(s.Name+" "+s.Description), q) {
				continue
			}
			sb.WriteString(fmt.Sprintf("- %s", s.Name))
			if s.Description != "" {
				sb.WriteString(": ")
				sb.WriteString(s.Description)
			}
			sb.WriteString("\n")
			found++
			if found >= limit {
				break
			}
		}
		if found == 0 {
			return "No skills matched.", nil
		}
		return strings.TrimSpace(sb.String()), nil

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
			return sb.String(), nil
		}
		// Fallback: legacy Content-only stores (e.g. jsonstore or test stubs).
		for _, s := range t.Skills.List() {
			if s.Name == args.Name || s.ID == args.Name {
				return s.Content, nil
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
			var sb strings.Builder
			for _, e := range entries {
				kind := "f"
				if e.Type == "directory" {
					kind = "d"
				}
				fmt.Fprintf(&sb, "%s  %s", kind, e.Path)
				if e.Type == "file" {
					fmt.Fprintf(&sb, "  (%d B%s)", e.SizeBytes, map[bool]string{true: "", false: ", binary"}[e.Editable])
				}
				sb.WriteString("\n")
			}
			if sb.Len() == 0 {
				return fmt.Sprintf("skill %q has no files", args.Name), nil
			}
			return strings.TrimSpace(sb.String()), nil
		}
		return "", fmt.Errorf("skill store does not support file listing")

	case name == "skill_save":
		var args struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
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
		return fmt.Sprintf("Saved skill %q (id=%s).", s.Name, s.ID), nil

	case name == "memory_save":
		var args struct {
			Content string   `json:"content"`
			Tags    []string `json:"tags"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Content) == "" {
			return "", fmt.Errorf("content is required")
		}
		e := &domain.MemoryEntry{ID: domain.NewID("mem"), Content: args.Content, Tags: args.Tags, Source: "agent", CreatedAt: time.Now().UTC()}
		if err := t.Memory.Save(e); err != nil {
			return "", err
		}
		return fmt.Sprintf("Saved memory entry %s.", e.ID), nil

	case name == "memory_search":
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
		entries := t.Memory.List()
		sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
		var sb strings.Builder
		q := strings.ToLower(args.Query)
		found := 0
		for _, e := range entries {
			if !strings.Contains(strings.ToLower(e.Content+" "+strings.Join(e.Tags, " ")), q) {
				continue
			}
			sb.WriteString(formatMemoryEntry(e))
			found++
			if found >= limit {
				break
			}
		}
		if found == 0 {
			return "No memory entries matched.", nil
		}
		return strings.TrimSpace(sb.String()), nil

	case name == "memory_list":
		entries := t.Memory.List()
		sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
		var sb strings.Builder
		for _, e := range entries {
			sb.WriteString(formatMemoryEntry(e))
		}
		if sb.Len() == 0 {
			return "No memory entries.", nil
		}
		return strings.TrimSpace(sb.String()), nil

	case name == "memory_delete":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if err := t.Memory.Delete(args.ID); err != nil {
			return "", err
		}
		return fmt.Sprintf("Deleted memory entry %s.", args.ID), nil

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
		if len(hits) == 0 {
			return "No documentation matched.", nil
		}
		var sb strings.Builder
		for _, h := range hits {
			sb.WriteString(fmt.Sprintf("- [%s] %s (%s)\n", h.ID, h.Title, h.Path))
		}
		return strings.TrimSpace(sb.String()), nil

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
		return doc.Content, nil

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
		return fmt.Sprintf("installed plugin %q (%s v%s) — run mcp_enable with id=%s to connect", p.Manifest.Name, p.Manifest.ID, p.Manifest.Version, p.Manifest.ID), nil

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
		return fmt.Sprintf("added MCP server %q (id=%s) — run mcp_enable with id=%s to connect", p.Manifest.Name, p.Manifest.ID, p.Manifest.ID), nil

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
		return fmt.Sprintf("registered plugin %q (%s v%s) — run mcp_enable with id=%s to connect", p.Manifest.Name, p.Manifest.ID, p.Manifest.Version, p.Manifest.ID), nil

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
		return fmt.Sprintf("enabled plugin %q — %d tool(s) available", args.ID, len(tools)), nil

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
		return fmt.Sprintf("disabled plugin %q (definition kept)", args.ID), nil

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
		return fmt.Sprintf("unregistered plugin %q", args.ID), nil

	case name == "mcp_list":
		if t.Plugins == nil {
			return `{"count":0,"plugins":[]}`, nil
		}
		plugins, err := t.Plugins.List()
		if err != nil {
			return "", fmt.Errorf("mcp_list: %w", err)
		}
		type srvInfo struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Command string `json:"command"`
			Running bool   `json:"running"`
			Tools   int    `json:"tools"`
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
		b, _ := json.Marshal(map[string]any{"count": len(out), "plugins": out})
		return string(b), nil

	case name == "tool_list":
		var args struct {
			Server string `json:"server"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		type toolEntry struct {
			Name        string         `json:"name"`
			Server      string         `json:"server"`
			Description string         `json:"description,omitempty"`
			InputSchema map[string]any `json:"input_schema,omitempty"`
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
		b, _ := json.Marshal(map[string]any{"count": len(entries), "tools": entries})
		return string(b), nil

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
			Name        string `json:"name"`
			Server      string `json:"server"`
			Description string `json:"description,omitempty"`
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
		b, _ := json.Marshal(map[string]any{
			"server": args.Server, "query": args.Query,
			"count": len(matches), "matches": matches,
		})
		return string(b), nil

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
				b, _ := json.Marshal(map[string]any{
					"server": args.Server, "tool": args.Tool,
					"input_schema": schema,
				})
				return string(b), nil
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
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Search: %s\n\n", resp.Query))
		for i, r := range resp.Results[:limit] {
			sb.WriteString(fmt.Sprintf("## %d. %s\n", i+1, r.Title))
			sb.WriteString(fmt.Sprintf("URL: %s\n", r.URL))
			if r.Snippet != "" {
				sb.WriteString(fmt.Sprintf("Snippet: %s\n", r.Snippet))
			}
			sb.WriteString(fmt.Sprintf("Sources: %s\n\n", strings.Join(r.Sources, ", ")))
		}
		if len(resp.Errors) > 0 {
			sb.WriteString("Source errors:\n")
			for _, e := range resp.Errors {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", e.Source, e.Error))
			}
		}
		if len(resp.Results) == 0 {
			sb.WriteString("No results found.\n")
		}
		return sb.String(), nil
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
		sb.WriteString(fmt.Sprintf("URL: %s\n", page.URL))
		if page.FinalURL != page.URL {
			sb.WriteString(fmt.Sprintf("Final URL: %s\n", page.FinalURL))
		}
		sb.WriteString(fmt.Sprintf("Status: %d | Content-Type: %s\n", page.StatusCode, page.ContentType))
		if page.Redirects > 0 {
			sb.WriteString(fmt.Sprintf("Redirects: %d\n", page.Redirects))
		}
		if len(page.Headers) > 0 {
			sb.WriteString("Headers:\n")
			for k, v := range page.Headers {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
			}
		}
		if page.Title != "" {
			sb.WriteString(fmt.Sprintf("Title: %s\n", page.Title))
		}
		if len(page.Links) > 0 {
			sb.WriteString(fmt.Sprintf("Links: %d\n", len(page.Links)))
			for i, l := range page.Links {
				if i >= 50 {
					sb.WriteString("... (more links omitted)\n")
					break
				}
				label := strings.TrimSpace(l.Text)
				if label == "" {
					sb.WriteString(fmt.Sprintf("- %s\n", l.Href))
				} else {
					sb.WriteString(fmt.Sprintf("- %s -> %s\n", label, l.Href))
				}
			}
		}
		if page.Truncated {
			sb.WriteString(fmt.Sprintf("[truncated at %d bytes]\n", page.Bytes))
		}
		sb.WriteString("\n")
		sb.WriteString(page.Text)
		return sb.String(), nil
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
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Provider: %s\n\n", answer.Provider))
		sb.WriteString(answer.Text)
		return sb.String(), nil
	}

	if out, handled, err := t.executeAutomation(ctx, name, argsJSON); handled {
		return out, err
	}

	// dynamic MCP tools: mcp__<server>__<tool>
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
// TodoWrite style). Empty items clears the list. Requires a conversation id
// in the context (set via WithConversationID by the turn runner).
const (
	todoMaxItems        = 50
	todoMaxContentChars = 500
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
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if len(args.Items) > todoMaxItems {
		return "", fmt.Errorf("items must have at most %d entries", todoMaxItems)
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
	current := t.Todos.Get(conversationID)
	summary := domain.SummarizeTodos(current)
	out := map[string]any{
		"ok":           true,
		"conversation": conversationID,
		"total":        summary.Total,
		"pending":      summary.Pending,
		"in_progress":  summary.InProgress,
		"completed":    summary.Completed,
		"items":        current,
	}
	b, _ := json.Marshal(out)
	return string(b), nil
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
		b, _ := json.Marshal(map[string]any{
			"ok":     true,
			"via":    result.Via,
			"answer": result.Answer,
		})
		return string(b), nil
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
		case "ci_pipeline_list", "ci_pipeline_read", "ci_pipeline_validate", "ci_run", "ci_run_status",
			"ci_logs", "ci_cancel", "automation_list", "automation_read", "automation_validate",
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
		b, _ := json.Marshal(v)
		return string(b), true, nil
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
		if id := str("workflow_id"); id != "" {
			run, err := a.RunWorkflow(ctx, id, "agent")
			return encode(run, err)
		}
		run, err := a.StartPipeline(ctx, str("workspace"), "agent")
		return encode(run, err)
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

// formatMemoryEntry renders one memory entry for agent tool output. It
// surfaces id, created_at (RFC3339), source, tags, and content so the
// agent can reason about recency, provenance, and clustering — not just
// the raw text. Entries are listed newest-first by the callers.
func formatMemoryEntry(e *domain.MemoryEntry) string {
	ts := e.CreatedAt.UTC().Format(time.RFC3339)
	source := e.Source
	if source == "" {
		source = "user"
	}
	tags := ""
	if len(e.Tags) > 0 {
		tags = " tags:" + strings.Join(e.Tags, ",")
	}
	return fmt.Sprintf("- [%s] (%s, %s%s) %s\n", e.ID, ts, source, tags, e.Content)
}
