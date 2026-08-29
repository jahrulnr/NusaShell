// Package tools implements the agent's built-in toolbox (skill_*,
// memory_*, docs_*, mcp_* meta-tools). MCP plugin tools are executed via
// mcp_call with a ref (<plugin-id>:<tool>, e.g. nusashell.files:read);
// mcp__<server>__<tool> names are not callable.
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
	"sync"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"

	"github.com/jahrulnr/searchwire"
)

func ptrBool(v bool) *bool { return &v }

type Toolbox struct {
	Skills          application.SkillStore
	SkillSearcher   application.SkillSearcher // optional; nil = substring fallback
	Memory          application.MemoryStore   // legacy — used by lifecycle/learning subsystems
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
	// SpeechOfflineAvailable flips the generate_speech tool on when the local
	// piper engine is wired (set by the composition root at startup).
	SpeechOfflineAvailable bool
	// Contracts reads plugin usage contracts declared via manifest
	// contract.entry. nil disables contract_read and the gate.
	Contracts ContractSource
	MCP       interface {
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
	// contractsGate tracks per-conversation contract reads for the gate.
	// Initialized lazily via gate(); safe on the zero value.
	contractsGateOnce sync.Once
	contractsGate     *contractGate
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
		{Name: "todo", Description: "Manage the conversation task checklist. Two modes: `replace` (default, full-replace Claude TodoWrite style — empty items clears the list) and `patch` (merge by ID — update status/content of existing items, add new items, keep untouched items unchanged). Use `patch` to update a single item's status without re-emitting the full list (saves tokens). In patch mode, `content` may be empty (meaning \"don't change content, only update status\"). The user can delete items from the UI — treat deleted items as gone and do not re-add them. The optional `brief` argument is a living planning document that survives compaction and is re-injected via hydration. Format: markdown with required sections — `## Objective` (what the user asked for, in their words), `## Done when` (acceptance criteria — what the finished result looks like) — and optional sections that grow as the task progresses: `## Findings` (what you discovered during exploration: paths, line numbers, relevant files), `## Approach` (key steps/strategy). Set the brief at the start of a task; update it as findings emerge and the approach solidifies (e.g. after exploration, add concrete paths/lines to Findings; before execution, refine Approach). The brief is mirrored to a plan file on disk; the result returns `plan_path` (absolute) — `file_read` that path to re-read the latest brief, and pass it to ACP subagents that need the plan. Set `clear_brief: true` to delete the brief and its plan file (items are untouched unless you also clear them); an empty `brief` string alone never clears. The current hydration checkpoint is reused until compaction; the brief remains in tool history and is included in the fresh post-compaction checkpoint. Legacy `goal` arg is accepted for backward compat and mapped to `brief`.", InputSchema: obj("object", props("items", arrObj("Todo items (max 50). In replace mode: full list. In patch mode: only items to update/add.", props("id", str("Stable item id (unique within the list)"), "content", str("Short task description (max 500 chars). Required in replace mode; optional in patch mode (empty = keep existing)."), "status", strEnum("Item status; prefer exactly one in_progress at a time", "pending", "in_progress", "completed")), "id", "content", "status"), "mode", strEnum("Update mode: replace (default, full-replace) or patch (merge by ID)", "replace", "patch"), "brief", str("Living planning document. Required markdown sections: `## Objective` (user intent in their words), `## Done when` (acceptance criteria). Optional, grows over time: `## Findings` (paths, line numbers, relevant files discovered), `## Approach` (key steps/strategy). Set at task start; update as findings emerge. Survives compaction. Max ~10000 tokens."), "clear_brief", obj("boolean", nil)), "items")},
		{Name: "ask_question", Description: "Pause and ask the user a structured clarifying question before continuing. Use only for genuine decisions the user must make — not for things you can figure out yourself. Set multi_select=true whenever more than one option could fit (preferences, scope, priorities) so the user can pick several. The user can also add free text as a note/suggestion alongside the chosen options (when allow_free_text=true) or answer purely with text. The turn blocks until the user answers or cancels.", InputSchema: obj("object", props("question", str("The question to show the user"), "options", arrObj("Selectable choices (1-8). Mark one default when possible.", props("id", str("Stable option id"), "label", str("Short option label"), "description", str("Optional one-line explanation"), "default", obj("boolean", nil), "icon", str("Optional emoji or short icon glyph"), "image", str("Optional image URL or compact data URI")), "id", "label"), "allow_free_text", obj("boolean", nil), "multi_select", obj("boolean", nil)), "question", "options")},
		{Name: "ci_run", Description: "Start a saved automation by workflow_id. Set async=true to return immediately with a run_id while the pipeline runs in the background; then use ci_wait or ci_run_status to check on it. Without async, the call blocks until the pipeline finishes.", InputSchema: obj("object", props("workflow_id", str("Automation id from automation_list"), "async", obj("boolean", nil)), "workflow_id")},
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
		{Name: "tool_list", Description: "List tools from a running MCP server. Accepts the plugin id (e.g. \"nusashell.terminal\"). When omitted, lists tools across all running MCP servers. Returns compact entries (ref, name, server, description) without parameter schemas — load the exact schema with tool_schema before first use of an unfamiliar tool.", InputSchema: obj("object", props("server", str("Plugin id; when omitted, lists all running servers")))},
		{Name: "tool_schema", Description: "Load one MCP tool's input schema by plugin id and tool name. The tool name is the bare tool name (e.g. \"exec\"). Returns the schema as readable JSON — the only place schemas are served; mcp_search and tool_list stay schema-free so large catalogs remain token-cheap.", InputSchema: obj("object", props("server", str("Plugin id (e.g. nusashell.terminal)"), "tool", str("Bare tool name within the server (e.g. \"exec\")")), "server", "tool")},
		{Name: "mcp_search", Description: "Search running MCP servers' tools by name or description (case-insensitive token match — any term matches). When server is omitted, searches across ALL running MCP servers. Returns compact matches (ref, name, description, ranked) without inlining parameter schemas — call tool_schema for exact argument fields when needed. This is the universal MCP discovery path that works on every provider — always use mcp_search + mcp_call instead of guessing tool names.", InputSchema: obj("object", props("server", str("Optional: plugin id; when omitted, searches all running servers"), "query", str("Search query"), "limit", intSchema("Max results, default 20")), "query")},
		{Name: "mcp_call", Description: "Execute an MCP tool by ref. Get the ref from mcp_search or tool_list (format <plugin-id>:<tool> e.g. nusashell.files:read). Pass `arguments_json` as a JSON-encoded string of the arguments matching the tool's parameters schema — the exact object that the tool expects as its input, e.g. {\"path\":\"/etc/hosts\"}. Omit `arguments_json` entirely for parameterless tools (defaults to {}). The ref binds to a specific running MCP server + tool; if the server has been disabled or restarted since discovery, you get a STALE_TOOL_REF error and must search again. Plugins that declare a usage contract (contract flag in mcp_list) must be read first via contract_read when required by the plugin_contract_mode setting. This is the only MCP execution path — mcp__<server>__<tool> names are not callable.", InputSchema: obj("object", props("ref", str("Tool ref from mcp_search / tool_list results (e.g. nusashell.files:read)"), "arguments_json", str("JSON-encoded tool arguments matching the parameters schema (e.g. {\"path\":\"/etc/hosts\"}). Optional; defaults to {} — omit entirely for parameterless tools. Load the exact schema with tool_schema if unsure.")), "ref")},
		{Name: "contract_read", Description: "Read a plugin's usage contract (best-practice rules plus state & side-effect disclosure) declared in its manifest before working with that plugin's tools. Pass id=<plugin-id>, or id=all to read every contract-declaring plugin at once. Advisory by default (plugin_contract_mode defaults to hint); enforcement is only active when the setting is set to require.", InputSchema: obj("object", props("id", str("Plugin id (e.g. nusashell.files) or 'all'")), "id")},
		{Name: "mcp_register", Description: "Copy a new MCP plugin from an absolute staging folder into the installed plugin store, or replace an existing plugin with the same id. The source must contain manifest.json and must stay outside the installed plugins root. Check mcp_list and ask the user before replacing an existing id; then call mcp_enable.", InputSchema: obj("object", props("source", str("Absolute staging path to the plugin folder containing manifest.json")), "source")},
		{Name: "mcp_enable", Description: "Start/connect an MCP plugin so its tools become available. Returns only status + tool count — use tool_list or mcp_search to discover the tools. If already connected, returns already_enabled without reconnecting. The plugin must be registered first (mcp_register or the Plugins view).", InputSchema: obj("object", props("id", str("Plugin id (e.g. nusashell.files)")), "id")},
		{Name: "mcp_disable", Description: "Stop/disconnect an MCP plugin. The definition stays installed; only the MCP subprocess is stopped. Tools from this server are no longer listed.", InputSchema: obj("object", props("id", str("Plugin id")), "id")},
		{Name: "mcp_unregister", Description: "Remove an MCP plugin entirely and delete its installed folder. Ask the user for confirmation first. Use mcp_disable when the plugin only needs to stop.", InputSchema: obj("object", props("id", str("Plugin id")), "id")},
		{Name: "mcp_install", Description: "Install an MCP plugin from the curated catalog or a GitHub repository (owner/repo or URL). After install, call mcp_enable with the resulting plugin id to connect and load its tools.", InputSchema: obj("object", props("source", strEnum("Install source", "catalog", "github"), "id", str("Catalog plugin id (required when source=catalog)"), "url", str("GitHub repo URL or owner/repo shorthand (required when source=github)"), "subdir", str("Optional subdirectory inside a monorepo (github)"), "ref", str("Optional branch or tag to pin (github)")), "source")},
		{Name: "mcp_server_add", Description: "Register a manual MCP server (no manifest needed). Transports: stdio (command/args/env, e.g. npx servers), sse, or http (Streamable HTTP) with url and optional headers for remote servers. Use for generic MCP servers; use mcp_register for NusaShell plugin folders. After adding, call mcp_enable with the server id to connect and load its tools.", InputSchema: obj("object", props("name", str("Human-readable server name"), "transport", strEnum("Transport kind", "stdio", "sse", "http"), "command", str("Command to launch the server (stdio transport, e.g. npx, node, python)"), "url", str("Server URL (required for sse/http transports, e.g. https://host/mcp)"), "args", arr("Arguments for the stdio command (e.g. -y @modelcontextprotocol/server-github)"), "env", obj("object", props("additional", str("KEY=VALUE entries for the stdio process")), "additional"), "headers", obj("object", props("additional", str("HTTP headers for sse/http transports, e.g. Authorization: Bearer <token>")), "additional"), "id", str("Optional stable id (default auto-generated)")), "name")},
		{Name: "read_media", Description: "Load a media file (image, audio, video, or PDF document) from disk into your context. The media type is auto-detected from the file's binary magic bytes — no need to specify whether it's an image, audio, video, or PDF. When your active model supports the detected media kind natively, the file is attached to your context directly. For non-capable models, a fallback model transcribes/describes the content and returns the text, or a placeholder note with the file path is returned for documents.", InputSchema: obj("object", props("file_path", str("Absolute path of the media file on disk"), "question", str("Optional question about the media content")), "file_path")},
		{Name: "web_search", Description: "Search the web for fresh information. Returns ranked results with title, URL, and snippet from multiple sources (Brave, Startpage, Wikipedia, GitHub). Use this when you need current information, documentation, or research. Follow up with web_fetch on promising URLs for full page content. Oversized result lists are truncated in-band (~32KiB) with overflow_path pointing at the full JSONL in the platform temp dir; continue with file_read.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results (default 10)")), "query")},
		{Name: "web_fetch", Description: "Fetch a URL and return readable text (HTML stripped to title + visible text). Use after web_search to read full page content from a result URL. Accepts http/https only. Extraction may read up to max_bytes (default 2MB); the in-band tool result is capped at ~32KiB. When truncated, overflow_path is an absolute temp file — page with file_read using next_offset_bytes.", InputSchema: obj("object", props("url", str("URL to fetch"), "max_bytes", intSchema("Optional max bytes of extracted text (default 2MB)")), "url")},
	}
	if t.Acp != nil && len(t.Acp.EnabledAcpAgents()) > 0 {
		subagentDesc := "Delegate a self-contained task to a configured ACP coding agent (Cursor, Claude Code, Codex CLI, etc). The ACP agent does not receive this conversation, MCP plugins, or NusaShell tools. Pass a compact brief with absolute paths. Always async: returns immediately with run ids (status \"starting\"); the tool call stays \"running\" until the subagent finishes, then a synthetic `subagent_result` tool call delivers the full result and a new turn is triggered so you process it. count (1-6) fans the same brief out to parallel sessions."
		if delegation := application.AcpDelegationDescription(t.Acp.EnabledAcpAgents()); delegation != "" {
			subagentDesc += "\n\n" + delegation
		}
		tools = append(tools,
			application.ToolInfo{Name: "subagent", Description: subagentDesc, InputSchema: obj("object", props("prompt", str("Self-contained task brief"), "agent_id", str("Optional ACP agent id from Providers; omit to use the default enabled agent"), "workspace", str("Optional absolute workspace path (defaults to the conversation workspace)"), "mode_id", str("Optional ACP session mode id advertised by the agent"), "model_id", str("Optional ACP model id advertised by the agent"), "count", intSchema("Number of parallel spawns of the same brief (1-6, default 1)")), "prompt")},
			application.ToolInfo{Name: "subagent_steer", Description: "Send an additional instruction to a live ACP subagent without cancelling it. Applied at the next prompt boundary.", InputSchema: obj("object", props("id", str("ACP run id from subagent"), "text", str("Steer instruction")), "id", "text")},
			application.ToolInfo{Name: "subagent_stop", Description: "Cancel a live ACP subagent run.", InputSchema: obj("object", props("id", str("ACP run id")), "id")},
			application.ToolInfo{Name: "subagent_wait", Description: "Wait for an async ACP subagent run to finish.", InputSchema: obj("object", props("id", str("ACP run id"), "timeout_ms", intSchema("Optional wait timeout in milliseconds")), "id")},
		)
	}
	// MCP plugin tools are NOT advertised to the agent. The tool list must
	// stay stable for the lifetime of a conversation so the provider prompt
	// cache (OpenAI / Claude) is not invalidated. The agent discovers MCP
	// tools via mcp_list, tool_list, and tool_schema, and
	// executes them only through mcp_call with a ref (<plugin-id>:<tool>) —
	// mcp__<server>__<tool> names are not callable. MCP tools are available
	// to pipeline workflow steps (capability resolution) and the Plugins UI.
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
	if t.mediaGenerationAnyConfigured() {
		tools = append(tools, application.ToolInfo{
			Name:        "generate_media",
			Description: "Generate media from a prompt and save it for the user to view/play. media_type selects the generator: \"image\" (text\u2192PNG/JPEG/WebP; referenced images enable editing), \"speech\" (text\u2192spoken audio), \"video\" (text\u2192short mp4 clip; async upstream \u2014 tens of seconds to minutes is expected; referenced images enable image-to-video). Only modes configured in Settings can serve a request. The result is already delivered to the user \u2014 do not re-render it as Markdown.",
			InputSchema: obj("object", props(
				"media_type", strEnum("Which generator to use", "image", "speech", "video"),
				"prompt", str("Text input for the chosen generator: scene description (image/video; max 4000 chars for video) or the text to speak aloud (max 20000 chars)."),
				"size", strEnum("Image only: output size", "auto", "1024x1024", "1536x1024", "1024x1536"),
				"quality", strEnum("Image only: rendering quality", "auto", "low", "medium", "high"),
				"background", strEnum("Image only: background treatment", "auto", "transparent", "opaque"),
				"n", map[string]any{"type": "integer", "description": "Image only: number of images to generate (1-4, default 1).", "minimum": 1, "maximum": 4},
				"referenced_image_paths", map[string]any{
					"type":        "array",
					"description": "Image or video: absolute paths of source images. For image media_type, enables image-to-image editing. For video media_type, the first image becomes the first frame (image-to-video); additional images are style references. Models without i2i/i2v support will reject upstream — check the model picker badges. Max 5.",
					"items":       map[string]any{"type": "string"},
					"maxItems":    5,
				},
				"voice", str("Speech only: voice id (provider-specific, e.g. alloy). Omit for default."),
				"format", strEnum("Speech only: audio format", "mp3", "wav", "opus"),
				"speed", map[string]any{"type": "number", "description": "Speech only: speed 0.25-4.0 (default 1.0)."},
				"duration_seconds", map[string]any{"type": "integer", "description": "Video only: clip length in seconds. Provider minimums apply and are reported verbatim on rejection (e.g. 'Supported durations: 4, 6, 8s')."},
				"resolution", strEnum("Video only: output resolution", "480p", "720p", "1080p"),
			), "media_type", "prompt"),
		})
	}
	// Native file CRUD + exec built-ins.
	tools = append(tools, fileToolInfos()...)
	tools = append(tools, execToolInfos()...)
	return tools
}

// mediaGenerationAnyConfigured reports whether at least one generate_media
// mode has a configured backend (the unified tool is advertised once for all
// modes; unconfigured modes are rejected at execution time with guidance).
func (t *Toolbox) mediaGenerationAnyConfigured() bool {
	imageConfigured := false
	if t.Settings != nil {
		s := t.Settings.Get()
		imageConfigured = mediaGenerationConfigured(s.ImageProviderID, s.ImageModelID)
	}
	return imageConfigured || t.speechGenerationConfigured() || t.videoGenerationConfigured()
}

// mediaGenerationConfigured is the shared gate for all generate_* tools:
// both provider and model must be set (non-empty).
func mediaGenerationConfigured(providerID, modelID string) bool {
	return strings.TrimSpace(providerID) != "" && strings.TrimSpace(modelID) != ""
}

// speechGenerationConfigured reports whether generate_speech can serve:
// an online TTS model is picked, OR offline piper is wired (the toolbox
// learns this via the SpeechGenerationAvailable flag set at composition).
func (t *Toolbox) speechGenerationConfigured() bool {
	if t.Settings == nil {
		return false
	}
	s := t.Settings.Get()
	if mediaGenerationConfigured(s.TTSProviderID, s.TTSModelID) {
		return true
	}
	return t.SpeechOfflineAvailable
}

// videoGenerationConfigured reports whether generate_video can serve:
// an online video model must be picked in Settings (no offline fallback).
func (t *Toolbox) videoGenerationConfigured() bool {
	if t.Settings == nil {
		return false
	}
	s := t.Settings.Get()
	return mediaGenerationConfigured(s.VideoGenProviderID, s.VideoGenModelID)
}

// executeFamily routes an ADVERTISED dispatcher-root call (skill, memory,
// docs, ci_pipeline) to its op handler. This is the ONLY door to the family
// handlers: the root+"_"+op strings below are private routing keys and are
// not reachable as tool names, so retired per-op calls fail loud upstream.
func (t *Toolbox) executeFamily(ctx context.Context, name string, argsJSON []byte) (string, error) {
	op, err := application.DispatchOp(name, argsJSON)
	if err != nil {
		return "", err
	}
	name = name + "_" + op // private routing key; never escapes this method
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
		items := make([]any, 0, len(skills))
		for _, s := range skills {
			items = append(items, map[string]any{"name": s.Name, "description": s.Description, "owned_by": s.EffectiveOwnedBy()})
		}
		return yamlJSONL(map[string]any{"count": len(skills)}, items), nil

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
		if t.SkillSearcher != nil {
			return t.searchSkillsRanked(ctx, args.Query, limit)
		}
		q := strings.ToLower(args.Query)
		var items []any
		for _, s := range t.Skills.List() {
			if !strings.Contains(strings.ToLower(s.Name+" "+s.Description), q) {
				continue
			}
			items = append(items, map[string]any{"name": s.Name, "description": s.Description})
			if len(items) >= limit {
				break
			}
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

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
		// scripts/) inside an existing skill — mirrors skill read's name+path
		// pattern. The skill must already exist; creation is not supported
		// in this mode.
		if path := strings.TrimSpace(args.Path); path != "" {
			if err := t.Skills.WriteFile(name, "", path, args.Content); err != nil {
				return "", fmt.Errorf("skill save: %w", err)
			}
			return yamlBlock(map[string]any{"status": "saved"}), nil
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
		return yamlBlock(map[string]any{"status": "saved", "id": s.ID}), nil

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
			Content:  domain.NormalizeMemoryContent(args.Content),
			Source:   "agent",
		}
		// Prefer the store's atomic capability. The fallback keeps custom/test
		// stores compatible while still making memory save idempotent.
		if idempotent, ok := t.Fragments.(application.FragmentSaveIfAbsent); ok {
			existing, saved, err := idempotent.SaveIfAbsent(frag)
			if err != nil {
				return "", err
			}
			if !saved {
				return yamlBlock(map[string]any{"status": "unchanged", "reason": "exact_duplicate", "fragment_id": existing.ID, "category": existing.Category}), nil
			}
		} else {
			want := domain.NormalizeMemoryContent(frag.Content)
			for _, existing := range t.Fragments.List(domain.FragmentSearchFilter{}) {
				if domain.NormalizeMemoryContent(existing.Content) == want {
					return yamlBlock(map[string]any{"status": "unchanged", "reason": "exact_duplicate", "fragment_id": existing.ID, "category": existing.Category}), nil
				}
			}
			if err := t.Fragments.Save(frag); err != nil {
				return "", err
			}
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
				// No old_text: rewrite the entire primary document body.
				if err := t.Primary.Update([]domain.PrimaryEntry{{Content: args.Content, Source: "agent"}}); err != nil {
					return "", err
				}
				return yamlBlock(map[string]any{"status": "rewritten", "target": "primary"}), nil
			}
			if err := t.Primary.Replace(args.OldText, args.Content); err != nil {
				return "", err
			}
			return yamlBlock(map[string]any{"status": "replaced", "target": "primary"}), nil
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
			return yamlBlock(map[string]any{"status": "updated", "target": "fragment"}), nil
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
		items := make([]any, 0, len(hits))
		for _, h := range hits {
			items = append(items, formatFragmentJSON(h.Fragment, h.Score))
		}
		return yamlJSONL(map[string]any{"count": len(hits)}, items), nil

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
			// Primary is a single document, not a list. Reading it via
			// list was a design wart; file_read is the honest path.
			if t.Primary == nil {
				return "", fmt.Errorf("primary memory is a single document, not a list — and no primary store is configured")
			}
			return "", fmt.Errorf("primary memory is a single document, not a list — read it with file_read(path=%q)", t.Primary.Path())
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
		items := make([]any, 0, len(frags))
		for _, f := range frags {
			items = append(items, formatFragmentJSON(f, 0))
		}
		return yamlJSONL(map[string]any{"target": "fragments", "count": len(frags)}, items), nil

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
		return yamlBlock(map[string]any{"status": "deleted"}), nil

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
		items := make([]any, 0, len(hits))
		for _, h := range hits {
			items = append(items, map[string]any{"id": h.ID, "title": h.Title, "path": h.Path})
		}
		return capJSONL("docs", map[string]any{"count": len(hits)}, items), nil

	case name == "docs_read":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		doc, err := t.Docs.Read(args.ID)
		if err != nil {
			return "", fmt.Errorf("document %q not found; use docs with op=search first", args.ID)
		}
		return capToolOutput("docs", map[string]any{"title": doc.Title, "path": doc.Path}, doc.Content), nil

	default:
		return "", fmt.Errorf("unknown %s op %q", name, op)
	}
}

// ExecuteStreamed runs a tool while forwarding live output chunks to onChunk.
// Only tools that produce streaming output (exec) honor the callback today;
// every other tool executes exactly like Execute and ignores it. Returns the
// final combined output string and error, same contract as Execute.
func (t *Toolbox) ExecuteStreamed(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (string, error) {
	if name == "exec" {
		_, out, err := executeExecToolChunks(ctx, name, argsJSON, onChunk)
		return out, err
	}
	return t.Execute(ctx, name, argsJSON)
}

// executePipelineOp runs one validated ci_pipeline op (list|read|validate).
// It lives outside executeAutomation so the automation switch never answers
// to a resolved per-op name.
func (t *Toolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	// Native built-ins first: file CRUD and the exec island.
	if name == "exec" {
		_, out, err := executeExecTool(ctx, name, argsJSON)
		return out, err
	}
	if strings.HasPrefix(name, "file_") || name == "grep" || name == "find_file" || name == "show" {
		ok, out, err := executeFileTool(name, argsJSON)
		if !ok {
			return "", fmt.Errorf("unknown tool %q", name)
		}
		return out, err
	}
	// Dispatcher families (advertised roots: skill, memory, docs,
	// ci_pipeline) are routed exclusively through executeFamily: resolving
	// the required `op` and reaching a family handler is impossible without
	// going through it. Retired per-op names fall through to the plain
	// switch below and end up as the honest "unknown tool" error.
	if application.IsDispatchRoot(name) {
		return t.executeFamily(ctx, name, argsJSON)
	}
	switch {
	case name == "todo":
		return t.execTodo(ctx, argsJSON)

	case name == "ask_question":
		return t.execAskQuestion(ctx, argsJSON)

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
		return yamlMD(map[string]any{"status": "installed", "name": p.Manifest.Name, "id": p.Manifest.ID, "version": p.Manifest.Version}, "Plugin installed. Call `mcp_enable id="+p.Manifest.ID+"` to connect and load its tools."), nil

	case name == "mcp_server_add":
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
			return "", fmt.Errorf("plugin store not available")
		}
		transport := domain.PluginTransport(strings.TrimSpace(args.Transport))
		if transport == "" {
			transport = domain.PluginTransportStdio
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
		return yamlMD(map[string]any{"status": "registered", "name": p.Manifest.Name, "id": p.Manifest.ID, "version": p.Manifest.Version}, "Plugin registered. Call `mcp_enable id="+p.Manifest.ID+"` to connect and load its tools."), nil

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
		return yamlBlock(map[string]any{"status": "disabled"}), nil

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
		return yamlBlock(map[string]any{"status": "unregistered"}), nil

	case name == "mcp_list":
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

	case name == "tool_list":
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
				// The full input schema is available via tool_schema.
				items = append(items, map[string]any{
					"ref":         p.Manifest.ID + ":" + tool.Name,
					"name":        tool.Name,
					"server":      p.Manifest.ID,
					"description": tool.Description,
				})
			}
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

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
				items := []any{map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  schema,
				}}
				return yamlJSONL(map[string]any{}, items), nil
			}
			return "", fmt.Errorf("tool %q not found on plugin %q; use tool_list to see available tools", args.Tool, args.Server)
		}
		return "", fmt.Errorf("plugin id %q not found; use mcp_list to see configured servers", args.Server)

	case name == "mcp_search":
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
		if len(items) > limit {
			items = items[:limit]
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

	case name == "mcp_call":
		var args struct {
			Ref           string          `json:"ref"`
			ArgumentsJSON json.RawMessage `json:"arguments_json"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Ref) == "" {
			return "", fmt.Errorf("ref is required (use mcp_search to find tools and their refs)")
		}
		// Tolerant parsing: accept arguments_json as a JSON string (legacy)
		// or as a JSON object directly (canonical form matching MCP spec).
		// Omitting arguments_json entirely defaults to {}.
		var toolArgs map[string]any
		if len(args.ArgumentsJSON) == 0 || string(args.ArgumentsJSON) == "null" {
			toolArgs = map[string]any{}
		} else if len(args.ArgumentsJSON) > 0 && args.ArgumentsJSON[0] == '"' {
			// Legacy form: arguments_json is a JSON string containing escaped JSON.
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
		toolExists := false
		for _, tool := range tools {
			if tool.Name == toolName {
				toolExists = true
				break
			}
		}
		if !toolExists {
			return "", fmt.Errorf("STALE_TOOL_REF: tool %q not found on plugin %q; call mcp_search again", toolName, plugin.Manifest.ID)
		}
		// Usage-contract gate: plugins declaring contract.entry are subject
		// to plugin_contract_mode (off/hint/require). Runs last so ref and
		// staleness errors always win over gate errors.
		advisory, err := t.contractCheck(ctx, plugin)
		if err != nil {
			return "", err
		}
		result, err := t.MCP.CallTool(ctx, plugin.Manifest.MCPServerID(), toolName, toolArgs)
		if err != nil {
			return "", err
		}
		if advisory != "" {
			return result + "\n\n[contract notice] " + advisory, nil
		}
		return result, nil

	case name == "contract_read":
		return t.execContractRead(ctx, argsJSON)

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
		resp, err := t.Searcher.SearchWithOptions(ctx, args.Query, searchwire.SearchOptions{Limit: args.Limit})
		if err != nil {
			return "", fmt.Errorf("search failed: %w", err)
		}
		if limit > len(resp.Results) {
			limit = len(resp.Results)
		}
		items := make([]any, 0, limit)
		for _, r := range resp.Results[:limit] {
			items = append(items, map[string]any{"title": r.Title, "url": r.URL, "snippet": r.Snippet, "sources": r.Sources})
		}
		meta := map[string]any{"count": len(items)}
		if len(resp.Errors) > 0 {
			var errs []any
			for _, e := range resp.Errors {
				errs = append(errs, map[string]any{"source": e.Source, "error": e.Error})
			}
			meta["errors"] = errs
		}
		return capJSONL("web_search", meta, items), nil
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
		var linkItems []any
		if len(page.Links) > 0 {
			for _, l := range page.Links {
				if len(linkItems) >= 50 {
					break
				}
				linkItems = append(linkItems, map[string]any{"text": strings.TrimSpace(l.Text), "href": l.Href})
			}
			meta["links_count"] = len(page.Links)
		}
		if page.Truncated {
			meta["bytes"] = page.Bytes
		}
		sb.WriteString(page.Text)
		// Page text is prose body; links are structured JSONL appended
		// after the text so the agent can parse them independently.
		body := sb.String()
		if len(linkItems) > 0 {
			var linkLines []string
			for _, li := range linkItems {
				b, _ := json.Marshal(li)
				linkLines = append(linkLines, string(b))
			}
			body = body + "\n\n" + strings.Join(linkLines, "\n")
		}
		return capToolOutput("web_fetch", meta, body), nil
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
		return yamlBlock(map[string]any{"status": "slept"}), nil
	}

	if out, handled, err := t.executeAutomation(ctx, name, argsJSON); handled {
		return out, err
	}

	// MCP plugin tools are NOT callable by name. There is exactly one
	// execution contract: mcp_call with a ref (<server>:<tool>) obtained from
	// mcp_search / tool_list. The legacy mcp__<server>__<tool>
	// dispatch was removed — those names are never advertised in tools[] and
	// gave the model a second, ambiguous way to reach the same tool.
	return "", fmt.Errorf("unknown tool: %s", name)
}

// searchSkillsRanked runs the ranked skill search (BM25 + graph + recency,
// no embedding) and appends substring matches the ranker missed (plural or
// inflected forms) so recall never regresses below the plain matcher.
func (t *Toolbox) searchSkillsRanked(ctx context.Context, query string, limit int) (string, error) {
	results, err := t.SkillSearcher.SearchSkills(ctx, query, limit)
	if err != nil {
		return "", fmt.Errorf("skill search: %w", err)
	}
	skills := t.Skills.List()
	byKey := make(map[string]*domain.Skill, len(skills))
	for _, sk := range skills {
		byKey[sk.ID] = sk
		if _, ok := byKey[sk.Name]; !ok {
			byKey[sk.Name] = sk
		}
	}
	seen := make(map[string]bool, len(results))
	items := make([]any, 0, limit)
	for _, r := range results {
		sk := byKey[r.ID]
		if sk == nil || seen[sk.ID] {
			continue
		}
		seen[sk.ID] = true
		items = append(items, map[string]any{"name": sk.Name, "description": sk.Description})
		if len(items) >= limit {
			break
		}
	}
	// Recall fallback: substring matches the ranker missed, in store order.
	if len(items) < limit {
		q := strings.ToLower(query)
		for _, sk := range skills {
			if len(items) >= limit {
				break
			}
			if seen[sk.ID] {
				continue
			}
			if strings.Contains(strings.ToLower(sk.Name+" "+sk.Description), q) {
				seen[sk.ID] = true
				items = append(items, map[string]any{"name": sk.Name, "description": sk.Description})
			}
		}
	}
	return yamlJSONL(map[string]any{"count": len(items)}, items), nil
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

// execTodo replaces the conversation todo checklist (full-replace, Claude
// TodoWrite style). Empty items clears the list. The optional `brief` argument
// is a living planning document that stays visible in tool history while the
// hydration checkpoint is reused, then is included in the fresh checkpoint
// after compaction. Requires a conversation id in the context (set via
// WithConversationID by the turn runner). Legacy `goal` arg is accepted for
// backward compat and mapped to `brief`.
const (
	todoMaxItems        = 50
	todoMaxContentChars = 500
	todoMaxBriefChars   = 40000 // ~10k tokens (4 chars/token average)
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
		Items      json.RawMessage `json:"items"`
		Mode       string          `json:"mode"`
		Brief      string          `json:"brief"`
		Goal       string          `json:"goal"` // legacy, mapped to brief
		ClearBrief bool            `json:"clear_brief"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	// itemsPresent distinguishes an omitted `items` key (leave the list
	// alone) from an explicit `items: []` (clear the list). This matters for
	// clear_brief: `{"clear_brief": true}` must remove only the brief, never
	// the checklist.
	itemsPresent := args.Items != nil
	var rawItems []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Status  string `json:"status"`
	}
	if itemsPresent {
		if err := json.Unmarshal(args.Items, &rawItems); err != nil {
			return "", fmt.Errorf("invalid items: %w", err)
		}
	}
	if len(rawItems) > todoMaxItems {
		return "", fmt.Errorf("items must have at most %d entries", todoMaxItems)
	}
	brief := strings.TrimSpace(args.Brief)
	if brief == "" && args.Goal != "" {
		// Backward compat: legacy `goal` arg maps to `brief`.
		brief = strings.TrimSpace(args.Goal)
	}
	if args.ClearBrief && brief != "" {
		return "", fmt.Errorf("clear_brief and brief are mutually exclusive")
	}
	if len(brief) > todoMaxBriefChars {
		return "", fmt.Errorf("brief exceeds %d chars (~10k tokens)", todoMaxBriefChars)
	}
	if err := domain.ValidateBrief(brief); err != nil {
		return "", err
	}
	mode := strings.TrimSpace(args.Mode)
	patchMode := mode == "patch"
	items := make([]domain.TodoItem, 0, len(rawItems))
	seenIDs := make(map[string]bool, len(rawItems))
	for _, raw := range rawItems {
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
		// In patch mode, content may be empty (meaning "don't change
		// content, only update status"). In replace mode, content is
		// required for every item.
		if !patchMode && content == "" {
			return "", fmt.Errorf("each item requires non-empty content")
		}
		if content != "" && len(content) > todoMaxContentChars {
			return "", fmt.Errorf("item content exceeds %d chars", todoMaxContentChars)
		}
		if !domain.IsValidTodoStatus(status) {
			return "", fmt.Errorf("item status must be pending, in_progress, or completed")
		}
		items = append(items, domain.TodoItem{ID: id, Content: content, Status: status})
	}
	if patchMode {
		t.Todos.Patch(conversationID, items)
	} else if itemsPresent {
		// Replace mode only rewrites the list when the `items` key is
		// present. An omitted `items` key leaves the existing list alone
		// (so `{"clear_brief": true}` never wipes the checklist); an
		// explicit `items: []` clears it.
		t.Todos.Set(conversationID, items)
	}
	if args.ClearBrief {
		// Explicit clear: remove the brief and its mirrored plan file.
		// Items are untouched (clear them separately with items: [] in
		// replace mode). An empty `brief` arg alone never clears — it
		// means "don't change the brief" so status patches are safe.
		if err := t.Todos.ClearBrief(conversationID); err != nil {
			return "", fmt.Errorf("clear brief: %w", err)
		}
	} else if brief != "" {
		t.Todos.SetBrief(conversationID, brief)
	}
	// Return a compact acknowledgment only: summary counts. The full item
	// list and brief are NOT echoed back — the agent just sent them, and
	// the UI receives the complete list via the agent.todo.updated event.
	// Echoing wastes tokens (the brief alone can be ~10k tokens). The
	// plan_path points at the mirrored plan file (always current) so the
	// agent or an ACP subagent can file_read it later.
	var summary domain.TodoSummary
	if patchMode || !itemsPresent {
		// Patch merged into the stored list, or replace mode left the
		// list untouched (items key omitted) — summarize what's stored.
		summary = domain.SummarizeTodos(t.Todos.Get(conversationID))
	} else {
		summary = domain.SummarizeTodos(items)
	}
	meta := map[string]any{
		"ok":           true,
		"conversation": conversationID,
		"mode":         mode,
		"total":        summary.Total,
		"pending":      summary.Pending,
		"in_progress":  summary.InProgress,
		"completed":    summary.Completed,
	}
	if planPath := t.Todos.PlanPath(conversationID); planPath != "" {
		meta["plan_path"] = planPath
	}
	return yamlJSONL(meta, nil), nil
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

// matchMCPTool was removed: mcp__<server>__<tool> names are no longer
// callable. The single MCP execution contract is mcp_call with a ref
// (<server>:<tool>) from mcp_search / tool_list.

func (t *Toolbox) executeAutomation(ctx context.Context, name string, argsJSON []byte) (string, bool, error) {
	if t.Automation == nil {
		switch name {
		case "ci_run", "ci_wait", "ci_run_status",
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
	case "automation_validate":
		raw := []byte(str("yaml"))
		r, _ := a.ValidateYAML(raw)
		return encode(r, nil)
	case "ci_run":
		async, _ := args["async"].(bool)
		id := str("workflow_id")
		if id == "" {
			return "", true, fmt.Errorf("workflow_id is required (use automation_list to see available automations)")
		}
		var run *domain.WorkflowRun
		var err error
		if async {
			run, err = a.RunWorkflowAsync(ctx, id, "agent")
		} else {
			run, err = a.RunWorkflow(ctx, id, "agent")
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

// formatFragmentJSON renders a fragment as a map for JSONL output.
// Score is included when non-zero (BM25 search results).
func formatFragmentJSON(f *domain.MemoryFragment, score float64) map[string]any {
	m := map[string]any{
		"id":         f.ID,
		"category":   string(f.Category),
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
