// Package tools implements the agent's built-in toolbox (skill_*,
// memory_*, docs_*) plus dynamic mcp__<server>__<tool> tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"
)

type Toolbox struct {
	Skills     application.SkillStore
	Memory     application.MemoryStore
	Docs       application.DocsSource
	MCPServers application.MCPServerStore
	MCP        interface {
		Connect(ctx context.Context, s *domain.MCPServer) ([]contracts.MCPToolDTO, error)
		CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
	}
}

func (t *Toolbox) ListTools() []application.ToolInfo {
	tools := []application.ToolInfo{
		{Name: "skill_list", Description: "List available skills with their names and descriptions.", InputSchema: obj("object", nil)},
		{Name: "skill_run", Description: "Load a skill's markdown instructions by skill name so you can follow them.", InputSchema: obj("object", props("name", str("Skill name to load")))},
		{Name: "memory_save", Description: "Save a fact to long-term memory with optional tags.", InputSchema: obj("object", props("content", str("Fact to remember"), "tags", arr("Optional tags")))},
		{Name: "memory_search", Description: "Search memory entries by substring match over content and tags.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results, default 10")))},
		{Name: "memory_list", Description: "List all memory entries.", InputSchema: obj("object", nil)},
		{Name: "memory_delete", Description: "Delete a memory entry by id.", InputSchema: obj("object", props("id", str("Memory entry id")))},
		{Name: "docs_search", Description: "Search the NusaShell Light documentation corpus.", InputSchema: obj("object", props("query", str("Search query"), "limit", intSchema("Max results, default 10")))},
		{Name: "docs_read", Description: "Read a documentation page by id (see docs_search results).", InputSchema: obj("object", props("id", str("Documentation page id")))},
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
	return tools
}

func (t *Toolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	switch {
	case name == "skill_list":
		var sb strings.Builder
		for _, s := range t.Skills.List() {
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
	}

	// dynamic MCP tools: mcp__<server>__<tool>
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		serverName, toolName, ok := strings.Cut(rest, "__")
		if !ok {
			return "", fmt.Errorf("malformed mcp tool name: %s", name)
		}
		for _, s := range t.MCPServers.List() {
			if s.Name != serverName {
				continue
			}
			var args map[string]any
			if len(argsJSON) > 0 {
				if err := json.Unmarshal(argsJSON, &args); err != nil {
					return "", fmt.Errorf("invalid args: %w", err)
				}
			}
			return t.MCP.CallTool(ctx, s.ID, toolName, args)
		}
		return "", fmt.Errorf("mcp server %q not found", serverName)
	}

	return "", fmt.Errorf("unknown tool: %s", name)
}

// ---- json schema helpers ----

func obj(typ string, properties map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": typ}
	if len(properties) > 0 {
		m["properties"] = properties
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
