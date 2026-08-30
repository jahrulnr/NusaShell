package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"nusashell/contracts"
	"nusashell/domain"
)

const (
	toolPresentationRequestLimit = 8 << 10
	toolPresentationTextLimit    = 12 << 10
)

// buildToolPresentation creates the browser-facing view without changing the
// raw result that is persisted or passed back to a provider. Built-in tools
// already use YAML front matter and JSONL bodies, so the presentation can
// expose those parts directly instead of making the browser parse an LLM
// envelope on every render.
func buildToolPresentation(name, args string, status domain.ToolCallStatus, rawOutput string) *contracts.ToolPresentationDTO {
	presentation := &contracts.ToolPresentationDTO{
		Variant: toolPresentationVariant(name, args),
		Action:  toolPresentationAction(name, status, rawOutput),
		Request: limitToolPresentation(formatToolPresentationRequest(name, args), toolPresentationRequestLimit),
		Result: contracts.ToolPresentationResultDTO{
			Format: toolPresentationFormat(name, args),
		},
	}

	if presentation.Variant == "terminal" {
		presentation.Result.Text = limitToolPresentation(rawOutput, toolPresentationTextLimit)
		presentation.Result.Summary = toolPresentationSummary(name, status, nil, nil, rawOutput)
		return presentation
	}

	parsed := parseToolPresentationOutput(rawOutput)
	presentation.Result.Meta = parsed.meta
	presentation.Result.Text = limitToolPresentation(parsed.body, toolPresentationTextLimit)
	if presentation.Result.Format == "list" {
		var items []map[string]any
		switch presentation.Variant {
		case "file-list":
			items = parseFileListPresentationItems(parsed.body)
		case "search-results":
			items = parseSearchPresentationItems(name, args, parsed.body)
		default:
			items = parseToolPresentationListItems(parsed.body)
		}
		if len(items) == 0 {
			items = parseToolPresentationMetaItems(parsed.meta, name)
		}
		if len(items) > 0 {
			presentation.Result.Items = items
			presentation.Result.Text = ""
		}
	}
	if presentation.Variant == "file-content" {
		presentation.Result.Language = toolPresentationLanguage(toolPresentationArg(args, "path"))
	}
	presentation.Result.Summary = toolPresentationSummary(name, status, parsed.meta, presentation.Result.Items, parsed.body)
	return presentation
}

// toolPresentationDTO is the shared adapter for persisted DTOs and live
// events. Keeping it in application means the domain ToolCall remains free of
// browser-specific fields and provider-facing ChatMessage stays unchanged.
func toolPresentationDTO(tc domain.ToolCall) *contracts.ToolPresentationDTO {
	return buildToolPresentation(tc.Name, tc.Args, tc.Status, tc.Output)
}

func toolResultPresentationStatus(output string) domain.ToolCallStatus {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(output)), "error:") {
		return domain.ToolFailed
	}
	meta := parseToolPresentationOutput(output).meta
	if value, ok := meta["status"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "failed", "failure", "error":
			return domain.ToolFailed
		case "cancelled", "canceled", "interrupted":
			return domain.ToolInterrupted
		}
	}
	return domain.ToolOK
}

type parsedToolPresentationOutput struct {
	meta map[string]any
	body string
}

func parseToolPresentationOutput(raw string) parsedToolPresentationOutput {
	parsed := parsedToolPresentationOutput{body: raw}
	text := strings.TrimPrefix(raw, "\ufeff")
	if !strings.HasPrefix(text, "---\n") {
		return parsed
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return parsed
	}
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(rest[:end]), &meta); err != nil {
		return parsed
	}
	if meta == nil {
		meta = map[string]any{}
	}
	parsed.meta = meta
	parsed.body = strings.TrimLeft(rest[end+len("\n---"):], "\r\n")
	return parsed
}

func parseToolPresentationItems(body string) []map[string]any {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	items := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil || item == nil {
			return nil
		}
		items = append(items, item)
	}
	return items
}

func parseToolPresentationListItems(body string) []map[string]any {
	if items := parseToolPresentationItems(body); len(items) > 0 {
		return items
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	var items []map[string]any
	if err := yaml.Unmarshal([]byte(trimmed), &items); err != nil || len(items) == 0 {
		return nil
	}
	return items
}

// parseToolPresentationMetaItems handles built-ins whose compact result is
// entirely in YAML front matter rather than a JSONL body (for example
// find_file's `files: [...]` result). It intentionally accepts only arrays
// and turns scalar paths into a stable {path: ...} row for the browser.
func parseToolPresentationMetaItems(meta map[string]any, name string) []map[string]any {
	if len(meta) == 0 {
		return nil
	}
	keys := []string{"items", "files", "plugins", "tools"}
	for _, key := range keys {
		values, ok := meta[key].([]any)
		if !ok || len(values) == 0 {
			continue
		}
		items := make([]map[string]any, 0, len(values))
		for _, value := range values {
			switch item := value.(type) {
			case map[string]any:
				items = append(items, item)
			case string:
				field := "path"
				if name == "file_list" {
					field = "name"
				}
				items = append(items, map[string]any{field: item})
			}
		}
		if len(items) > 0 {
			return items
		}
	}
	return nil
}

// parseFileListPresentationItems turns the compact ls-style body emitted by
// file_list into rows that the browser can style without parsing columns.
// Names are allowed to contain spaces; the first five fields are the stable
// mode, size, month, day, and time/year columns.
func parseFileListPresentationItems(body string) []map[string]any {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	items := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 6 {
			return nil
		}
		items = append(items, map[string]any{
			"mode":     fields[0],
			"size":     fields[1],
			"modified": strings.Join(fields[2:5], " "),
			"name":     strings.Join(fields[5:], " "),
		})
	}
	return items
}

func parseSearchPresentationItems(name, args, body string) []map[string]any {
	switch name {
	case "find_file", "file_search":
		return parsePathPresentationItems(body)
	case "grep":
		return parseGrepPresentationItems(body, toolPresentationArg(args, "output_mode"))
	default:
		return parseToolPresentationListItems(body)
	}
}

func parsePathPresentationItems(body string) []map[string]any {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	items := make([]map[string]any, 0)
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		items = append(items, map[string]any{"path": line})
	}
	return items
}

func parseGrepPresentationItems(body, mode string) []map[string]any {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}
	if mode == "files_with_matches" {
		return parsePathPresentationItems(trimmed)
	}
	items := make([]map[string]any, 0)
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path, lineNumber, content, matched, ok := splitGrepPresentationLine(line, mode == "count")
		if !ok {
			return nil
		}
		item := map[string]any{"path": path, "line": lineNumber}
		if mode == "count" {
			item["matches"] = lineNumber
			delete(item, "line")
		} else {
			item["content"] = content
			if matched {
				item["match"] = true
			}
		}
		items = append(items, item)
	}
	return items
}

func splitGrepPresentationLine(line string, allowTrailingNumber bool) (path string, lineNumber int, content string, matched bool, ok bool) {
	for i := 0; i < len(line); i++ {
		separator := line[i]
		if separator != ':' && separator != '-' {
			continue
		}
		start := i + 1
		end := start
		for end < len(line) && line[end] >= '0' && line[end] <= '9' {
			end++
		}
		if end == start {
			continue
		}
		if end == len(line) {
			if allowTrailingNumber && separator == ':' {
				n, err := strconv.Atoi(line[start:end])
				if err == nil {
					return line[:i], n, "", false, true
				}
			}
			continue
		}
		if line[end] != separator {
			continue
		}
		n, err := strconv.Atoi(line[start:end])
		if err != nil {
			continue
		}
		return line[:i], n, line[end+1:], separator == ':', true
	}
	return "", 0, "", false, false
}

func toolPresentationVariant(name, args string) string {
	switch name {
	case "exec", "mcp_call":
		return "terminal"
	case "file_list":
		return "file-list"
	case "file_read":
		return "file-content"
	case "grep", "find_file", "file_search", "web_search", "mcp_search", "docs_search":
		return "search-results"
	case "docs_read", "web_fetch", "web_answer", "contract_read":
		return "document"
	case "automation_read":
		return "document"
	case "automation_list", "ci_logs", "mcp_list", "tool_list", "tool_schema",
		"skill_list", "skill_search", "memory_search", "memory_list":
		return "collection"
	case "read_media", "generate_media", "generate_image", "generate_speech", "generate_video", "show":
		return "media"
	case "skill", "memory", "docs", "memory_project":
		switch toolPresentationArg(args, "op") {
		case "list", "search", "query":
			return "collection"
		case "read":
			return "document"
		default:
			return "status"
		}
	default:
		return "status"
	}
}

func toolPresentationFormat(name, args string) string {
	switch toolPresentationVariant(name, args) {
	case "terminal":
		return "terminal"
	case "file-list", "search-results", "collection":
		return "list"
	case "file-content":
		return "code"
	case "document":
		return "document"
	case "media":
		return "media"
	default:
		return "status"
	}
}

func toolPresentationAction(name string, status domain.ToolCallStatus, rawOutput string) string {
	if status == "" {
		if strings.TrimSpace(rawOutput) == "" {
			status = domain.ToolRunning
		} else {
			status = domain.ToolOK
		}
	}
	labels := map[string][3]string{
		"exec":                {"Running command", "Command completed", "Command failed"},
		"file_list":           {"Listing files", "Files listed", "File listing failed"},
		"file_read":           {"Reading file", "File read", "File read failed"},
		"file_write":          {"Writing file", "File written", "File write failed"},
		"file_patch":          {"Patching file", "File patched", "File patch failed"},
		"file_mkdir":          {"Creating directory", "Directory created", "Directory creation failed"},
		"file_delete":         {"Deleting path", "Path deleted", "Path deletion failed"},
		"file_move":           {"Moving path", "Path moved", "Path move failed"},
		"file_copy":           {"Copying path", "Path copied", "Path copy failed"},
		"file_info":           {"Inspecting path", "Path inspected", "Path inspection failed"},
		"grep":                {"Searching", "Search completed", "Search failed"},
		"file_search":         {"Searching files", "Search completed", "Search failed"},
		"find_file":           {"Finding files", "Files found", "File search failed"},
		"memory":              {"Updating memory", "Memory updated", "Memory update failed"},
		"skill":               {"Loading skill", "Skill loaded", "Skill load failed"},
		"docs":                {"Reading docs", "Docs loaded", "Docs load failed"},
		"mcp_call":            {"Calling MCP tool", "MCP call completed", "MCP call failed"},
		"mcp_list":            {"Listing MCP servers", "MCP servers listed", "MCP list failed"},
		"tool_list":           {"Listing MCP tools", "MCP tools listed", "Tool list failed"},
		"tool_schema":         {"Reading tool schema", "Tool schema loaded", "Tool schema failed"},
		"mcp_search":          {"Searching MCP tools", "MCP search completed", "MCP search failed"},
		"contract_read":       {"Reading plugin contract", "Plugin contract loaded", "Contract read failed"},
		"mcp_install":         {"Installing plugin", "Plugin installed", "Plugin install failed"},
		"mcp_register":        {"Registering plugin", "Plugin registered", "Plugin registration failed"},
		"mcp_server_add":      {"Adding MCP server", "MCP server added", "MCP server add failed"},
		"mcp_enable":          {"Connecting plugin", "Plugin connected", "Plugin connection failed"},
		"mcp_disable":         {"Disconnecting plugin", "Plugin disconnected", "Plugin disconnect failed"},
		"mcp_unregister":      {"Removing plugin", "Plugin removed", "Plugin removal failed"},
		"web_search":          {"Searching the web", "Web search completed", "Web search failed"},
		"web_fetch":           {"Fetching page", "Page fetched", "Page fetch failed"},
		"web_answer":          {"Preparing web answer", "Web answer ready", "Web answer failed"},
		"generate_image":      {"Generating image", "Image generated", "Image generation failed"},
		"generate_speech":     {"Generating speech", "Speech generated", "Speech generation failed"},
		"generate_video":      {"Generating video", "Video generated", "Video generation failed"},
		"generate_media":      {"Generating media", "Media generated", "Media generation failed"},
		"read_media":          {"Reading media", "Media read", "Media read failed"},
		"show":                {"Preparing preview", "Preview ready", "Preview failed"},
		"todo":                {"Updating tasks", "Tasks updated", "Task update failed"},
		"ask_question":        {"Waiting for answer", "Answer received", "Question failed"},
		"subagent":            {"Starting subagent", "Subagent completed", "Subagent failed"},
		"subagent_steer":      {"Steering subagent", "Subagent steered", "Subagent steer failed"},
		"subagent_stop":       {"Stopping subagent", "Subagent stopped", "Subagent stop failed"},
		"subagent_wait":       {"Waiting for subagent", "Subagent result ready", "Subagent wait failed"},
		"automation_list":     {"Listing automations", "Automations listed", "Automation list failed"},
		"automation_read":     {"Reading automation", "Automation loaded", "Automation read failed"},
		"automation_validate": {"Validating automation", "Automation validated", "Automation validation failed"},
		"automation_create":   {"Creating automation", "Automation created", "Automation creation failed"},
		"automation_enable":   {"Enabling automation", "Automation enabled", "Automation enable failed"},
		"automation_disable":  {"Disabling automation", "Automation disabled", "Automation disable failed"},
		"automation_status":   {"Checking automation", "Automation status ready", "Automation status failed"},
		"schedule_once":       {"Scheduling automation", "Automation scheduled", "Scheduling failed"},
		"schedule_every":      {"Scheduling automation", "Automation scheduled", "Scheduling failed"},
		"wait_until":          {"Preparing wait", "Wait prepared", "Wait preparation failed"},
		"sleep":               {"Pausing", "Pause finished", "Pause failed"},
		"ci_run":              {"Starting pipeline", "Pipeline started", "Pipeline failed"},
		"ci_wait":             {"Waiting for pipeline", "Pipeline finished", "Pipeline wait failed"},
		"ci_run_status":       {"Checking pipeline", "Pipeline status ready", "Pipeline status failed"},
		"ci_logs":             {"Reading pipeline logs", "Pipeline logs ready", "Pipeline logs failed"},
		"ci_cancel":           {"Cancelling pipeline", "Pipeline cancelled", "Pipeline cancel failed"},
		"ci_steer":            {"Steering pipeline", "Pipeline steered", "Pipeline steer failed"},
	}
	if known, ok := labels[name]; ok {
		switch {
		case status == domain.ToolRunning:
			return known[0]
		case status == domain.ToolFailed || status == domain.ToolInterrupted:
			return known[2]
		default:
			return known[1]
		}
	}
	readable := readableToolName(name)
	switch {
	case status == domain.ToolRunning:
		return "Running " + readable
	case status == domain.ToolFailed || status == domain.ToolInterrupted:
		return strings.Title(readable) + " failed"
	default:
		return strings.Title(readable) + " completed"
	}
}

func toolPresentationSummary(name string, status domain.ToolCallStatus, meta map[string]any, items []map[string]any, body string) string {
	if status == domain.ToolRunning && strings.TrimSpace(body) == "" {
		return "Running"
	}
	if status == domain.ToolFailed || status == domain.ToolInterrupted {
		if status == domain.ToolInterrupted {
			return "Interrupted"
		}
		return "Failed"
	}
	if name == "file_read" {
		if binary, ok := meta["binary"].(bool); ok && binary {
			if size, ok := toolPresentationNumber(meta, "size"); ok {
				return fmt.Sprintf("Binary file · %d bytes", size)
			}
			return "Binary file"
		}
		if bytes, ok := toolPresentationNumber(meta, "bytes"); ok {
			summary := fmt.Sprintf("Read %d bytes", bytes)
			if truncated, _ := meta["truncated"].(bool); truncated {
				summary += " · more available"
			}
			return summary
		}
	}
	if name == "grep" {
		if total, ok := toolPresentationNumber(meta, "total_line_matches"); ok {
			if files, filesOK := toolPresentationNumber(meta, "files"); filesOK {
				return fmt.Sprintf("%d matches · %d files", total, files)
			}
			return fmt.Sprintf("%d matches", total)
		}
		if matches, ok := toolPresentationNumber(meta, "line_matches"); ok {
			return fmt.Sprintf("%d line matches", matches)
		}
		if files, ok := toolPresentationNumber(meta, "files"); ok {
			return fmt.Sprintf("%d files", files)
		}
	}
	if count, ok := toolPresentationNumber(meta, "count"); ok {
		noun := toolPresentationCountNoun(name)
		summary := fmt.Sprintf("%d %s", count, noun)
		if total, ok := meta["total"].(string); ok && strings.TrimSpace(total) != "" {
			summary += " · " + total
		}
		return summary
	}
	if name == "todo" {
		if total, ok := toolPresentationNumber(meta, "total"); ok {
			summary := fmt.Sprintf("%d tasks", total)
			if open, pendingOK := toolPresentationNumber(meta, "pending"); pendingOK {
				if inProgress, inProgressOK := toolPresentationNumber(meta, "in_progress"); inProgressOK {
					open += inProgress
				}
				if open > 0 {
					summary += fmt.Sprintf(" · %d open", open)
				}
			}
			return summary
		}
	}
	if name == "file_info" {
		if exists, ok := meta["exists"].(bool); ok {
			if exists {
				return "Path exists"
			}
			return "Path not found"
		}
	}
	switch name {
	case "file_write":
		return "Written"
	case "file_patch":
		return "Patched"
	case "file_mkdir":
		return "Directory created"
	case "file_delete":
		return "Deleted"
	case "file_move":
		return "Moved"
	case "file_copy":
		return "Copied"
	case "automation_validate":
		if providers, ok := meta["providers"].(string); ok && strings.EqualFold(providers, "blocked") {
			return "Blocked"
		}
		if syntax, ok := meta["syntax"].(string); ok && strings.EqualFold(syntax, "invalid") {
			return "Invalid"
		}
		if capabilities, ok := meta["capabilities"].(string); ok && strings.EqualFold(capabilities, "invalid") {
			return "Invalid"
		}
		return "Valid"
	case "automation_enable":
		return "Enabled"
	case "automation_disable":
		return "Disabled"
	case "schedule_once", "schedule_every":
		return "Scheduled"
	case "web_answer":
		return "Answer ready"
	case "docs_read", "web_fetch":
		if title, ok := meta["title"].(string); ok && strings.TrimSpace(title) != "" {
			return "Loaded " + limitToolPresentation(title, 72)
		}
	}
	for _, key := range []string{"written", "created", "deleted", "moved", "copied"} {
		if done, ok := meta[key].(bool); ok && done {
			return strings.ToUpper(key[:1]) + key[1:]
		}
	}
	if len(items) > 0 {
		return fmt.Sprintf("%d %s", len(items), toolPresentationCountNoun(name))
	}
	if value, ok := meta["status"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.ToUpper(value[:1]) + value[1:]
	}
	if strings.TrimSpace(body) == "" {
		return "Completed"
	}
	return firstToolPresentationLine(body, 96)
}

func toolPresentationCountNoun(name string) string {
	switch name {
	case "file_list":
		return "entries"
	case "find_file", "file_search":
		return "files"
	case "skill_list", "skill_search":
		return "skills"
	case "memory_search", "memory_list":
		return "memories"
	case "docs_search":
		return "docs"
	case "mcp_list":
		return "plugins"
	case "tool_list":
		return "tools"
	case "mcp_search":
		return "matches"
	case "web_search":
		return "results"
	case "automation_list":
		return "automations"
	case "ci_logs":
		return "log entries"
	default:
		return "results"
	}
}

func toolPresentationNumber(meta map[string]any, key string) (int, bool) {
	if meta == nil {
		return 0, false
	}
	switch value := meta[key].(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case uint64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func toolPresentationArg(args, key string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(args), &payload); err != nil || payload == nil {
		return ""
	}
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func formatToolPresentationRequest(name, args string) string {
	tool := name
	if tool == "" {
		tool = "tool"
	}
	if tool == "mcp_call" {
		var payload map[string]json.RawMessage
		if json.Unmarshal([]byte(args), &payload) == nil && payload != nil {
			var ref string
			_ = json.Unmarshal(payload["ref"], &ref)
			if inner := payload["arguments_json"]; len(inner) > 0 && string(inner) != "null" {
				var encoded string
				if json.Unmarshal(inner, &encoded) == nil {
					inner = json.RawMessage(encoded)
				}
				if formatted := formatJSONForPresentation(string(inner)); formatted != "" {
					return fmt.Sprintf("mcp_call(%s) %s", refOrUnknown(ref), formatted)
				}
			}
			return fmt.Sprintf("mcp_call(%s) {}", refOrUnknown(ref))
		}
	}
	if formatted := formatJSONForPresentation(args); formatted != "" {
		return tool + "(" + formatted + ")"
	}
	args = strings.TrimSpace(args)
	if args == "" {
		return tool + "()"
	}
	return tool + "(" + args + ")"
}

func formatJSONForPresentation(raw string) string {
	var pretty bytes.Buffer
	if json.Indent(&pretty, []byte(strings.TrimSpace(raw)), "", "  ") != nil {
		return ""
	}
	return pretty.String()
}

func refOrUnknown(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return "?"
	}
	return ref
}

func readableToolName(name string) string {
	readable := strings.NewReplacer("mcp__", "", "__", " ", "_", " ", "-", " ").Replace(name)
	return strings.Join(strings.Fields(readable), " ")
}

func toolPresentationLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return ""
	}
	if ext == ".md" || ext == ".markdown" {
		return "markdown"
	}
	if ext == ".yml" || ext == ".yaml" {
		return "yaml"
	}
	if ext == ".js" || ext == ".mjs" || ext == ".cjs" {
		return "javascript"
	}
	if ext == ".ts" || ext == ".tsx" {
		return "typescript"
	}
	if ext == ".go" {
		return "go"
	}
	if ext == ".css" {
		return "css"
	}
	if ext == ".html" || ext == ".htm" {
		return "html"
	}
	if ext == ".json" {
		return "json"
	}
	return strings.TrimPrefix(ext, ".")
}

func firstToolPresentationLine(text string, limit int) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return limitToolPresentation(line, limit)
	}
	return ""
}

func limitToolPresentation(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n… (truncated)"
}
