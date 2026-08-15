// Package tools implements the agent's built-in toolbox (skill_*,
// memory_*, docs_*) plus dynamic mcp__<server>__<tool> tools.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"

	"github.com/jahrulnr/searchwire"
)

func ptrBool(v bool) *bool { return &v }

type Toolbox struct {
	Skills      application.SkillStore
	Memory      application.MemoryStore
	Docs        application.DocsSource
	MCPServers  application.MCPServerStore
	Todos       application.ConversationTodoPort
	Searcher    *searchwire.Searcher // zero-config searcher for web_search + web_fetch
	Settings    application.SettingsStore
	Credentials application.CredentialStore
	MCP         interface {
		Connect(ctx context.Context, s *domain.MCPServer) ([]contracts.MCPToolDTO, error)
		ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
		CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
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
		{Name: "skill_read", Description: "Read a skill's full markdown content by name so you can follow its instructions.", InputSchema: obj("object", props("name", str("Skill name (from skill_list or skill_search)")), "name")},
		{Name: "skill_run", Description: "Load a skill's markdown instructions by skill name so you can follow them.", InputSchema: obj("object", props("name", str("Skill name to load")), "name")},
		{Name: "memory_save", Description: "Save a fact to long-term memory with optional tags.", InputSchema: obj("object", props("content", str("Fact to remember"), "tags", arr("Optional tags")), "content")},
		{Name: "memory_search", Description: "Search memory entries by substring match over content and tags.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results, default 10")), "query")},
		{Name: "memory_list", Description: "List all memory entries.", InputSchema: obj("object", nil)},
		{Name: "memory_delete", Description: "Delete a memory entry by id.", InputSchema: obj("object", props("id", str("Memory entry id")), "id")},
		{Name: "todo", Description: "Replace the conversation task checklist (full replace, Claude TodoWrite style). Empty items clears the list. The user can delete items from the UI — treat deleted items as gone and do not re-add them.", InputSchema: obj("object", props("items", arrObj("Full replacement list of todo items (max 50)", props("id", str("Stable item id (unique within the list)"), "content", str("Short task description (max 500 chars)"), "status", strEnum("Item status; prefer exactly one in_progress at a time", "pending", "in_progress", "completed")), "id", "content", "status")), "items")},
		{Name: "docs_search", Description: "Search the NusaShell Light documentation corpus.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results, default 10")), "query")},
		{Name: "docs_read", Description: "Read a documentation page by id (see docs_search results).", InputSchema: obj("object", props("id", str("Documentation page id")), "id")},
		{Name: "mcp_list", Description: "List configured MCP servers with their enabled status and runtime state (running/stopped).", InputSchema: obj("object", nil)},
		{Name: "tool_list", Description: "List tools from a running MCP server by name (names and descriptions, plus optional input schemas). When the server is omitted, lists tools across all running MCP servers.", InputSchema: obj("object", props("server", str("Optional MCP server name; when omitted, lists tools across all running servers")))},
		{Name: "tool_search", Description: "Search a running MCP server's tools by name or description (case-insensitive token match — any term matches). Returns matching tool names and descriptions.", InputSchema: obj("object", props("server", str("MCP server name"), "query", str("Search query")), "server", "query")},
		{Name: "tool_schema", Description: "Load one MCP tool's input schema by server and tool name. Useful when you need the exact argument shape before calling an mcp__<server>__<tool> tool.", InputSchema: obj("object", props("server", str("MCP server name"), "tool", str("Tool name within the server")), "server", "tool")},
		{Name: "read_image", Description: "Load an image from the conversation into your context so you can see it. Pass the attachment name (from a user message) to view that image. When your active model supports vision, the image is attached to your context directly. For non-vision models, the image is described using a vision fallback model and the text description is returned.", InputSchema: obj("object", props("attachment_name", str("Name of the image attachment from a user message in this conversation"), "question", str("Optional question about the image")), "attachment_name")},
		{Name: "web_search", Description: "Search the web for fresh information. Returns ranked results with title, URL, and snippet from multiple sources (Brave, Startpage, Wikipedia, GitHub). Use this when you need current information, documentation, or research. Follow up with web_fetch on promising URLs for full page content.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results (default 10)")), "query")},
		{Name: "web_fetch", Description: "Fetch a URL and return readable text (HTML stripped to title + visible text). Use after web_search to read full page content from a result URL. Accepts http/https only.", InputSchema: obj("object", props("url", str("URL to fetch"), "max_bytes", intSchema("Optional max bytes of extracted text (default 2MB)")), "url")},
	}
	for _, s := range t.MCPServers.List() {
		if !s.Enabled {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		dt, err := t.MCP.Connect(ctx, s)
		cancel()
		if err != nil {
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
				Name:        "mcp__" + s.Name + "__" + tool.Name,
				Description: tool.Description,
				InputSchema: schema,
			})
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
			Name string `json:"name"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		for _, s := range t.Skills.List() {
			if s.Name == args.Name {
				return s.Content, nil
			}
		}
		return "", fmt.Errorf("skill %q not found; use skill_list or skill_search to see available skills", args.Name)

	case name == "skill_run":
		var args struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		for _, s := range t.Skills.List() {
			if s.Name == args.Name {
				return fmt.Sprintf("Skill %q loaded. Follow these instructions:\n\n%s", s.Name, s.Content), nil
			}
		}
		return "", fmt.Errorf("skill %q not found; use skill_list to see available skills", args.Name)

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
		e := &domain.MemoryEntry{ID: domain.NewID("mem"), Content: args.Content, Tags: args.Tags, CreatedAt: time.Now().UTC()}
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
		var sb strings.Builder
		q := strings.ToLower(args.Query)
		found := 0
		for _, e := range t.Memory.List() {
			if !strings.Contains(strings.ToLower(e.Content+" "+strings.Join(e.Tags, " ")), q) {
				continue
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", e.ID, e.Content))
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
		var sb strings.Builder
		for _, e := range t.Memory.List() {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", e.ID, e.Content))
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

	case name == "mcp_list":
		servers := t.MCPServers.List()
		type srvInfo struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Command string `json:"command"`
			Enabled bool   `json:"enabled"`
			Running bool   `json:"running"`
			Tools   int    `json:"tools"`
		}
		out := make([]srvInfo, 0, len(servers))
		for _, s := range servers {
			toolCount := 0
			running := false
			if tools, ok := t.MCP.ToolsFor(s.ID); ok {
				running = true
				toolCount = len(tools)
			}
			out = append(out, srvInfo{
				ID: s.ID, Name: s.Name, Command: s.Command,
				Enabled: s.Enabled, Running: running, Tools: toolCount,
			})
		}
		b, _ := json.Marshal(map[string]any{"count": len(out), "servers": out})
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
		for _, s := range t.MCPServers.List() {
			if args.Server != "" && s.Name != args.Server {
				continue
			}
			tools, ok := t.MCP.ToolsFor(s.ID)
			if !ok {
				continue
			}
			for _, tool := range tools {
				var schema map[string]any
				if len(tool.InputSchema) > 0 {
					_ = json.Unmarshal(tool.InputSchema, &schema)
				}
				entries = append(entries, toolEntry{
					Name:   "mcp__" + s.Name + "__" + tool.Name,
					Server: s.Name, Description: tool.Description, InputSchema: schema,
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
		for _, s := range t.MCPServers.List() {
			if args.Server != "" && s.Name != args.Server {
				continue
			}
			tools, ok := t.MCP.ToolsFor(s.ID)
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
					Name:   "mcp__" + s.Name + "__" + tool.Name,
					Server: s.Name, Description: tool.Description,
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
		for _, s := range t.MCPServers.List() {
			if s.Name != args.Server {
				continue
			}
			tools, ok := t.MCP.ToolsFor(s.ID)
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

	// dynamic MCP tools: mcp__<server>__<tool>
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		server, toolName, ok := matchMCPTool(rest, t.MCPServers.List())
		if !ok {
			return "", fmt.Errorf("malformed mcp tool name: %s", name)
		}
		var args map[string]any
		if len(argsJSON) > 0 {
			if err := json.Unmarshal(argsJSON, &args); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}
		}
		return t.MCP.CallTool(ctx, server.ID, toolName, args)
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

// matchMCPTool resolves mcp__<server>__<tool> against configured servers,
// preferring the longest server-name prefix so names that contain "__" still
// route to the right server.
func matchMCPTool(rest string, servers []*domain.MCPServer) (*domain.MCPServer, string, bool) {
	var best *domain.MCPServer
	bestTool := ""
	for _, server := range servers {
		prefix := server.Name + "__"
		toolName, ok := strings.CutPrefix(rest, prefix)
		if !ok || toolName == "" {
			continue
		}
		if best == nil || len(server.Name) > len(best.Name) {
			best = server
			bestTool = toolName
		}
	}
	if best == nil {
		return nil, "", false
	}
	return best, bestTool, true
}
