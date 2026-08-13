package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

// stubSkillStore is a minimal SkillStore for testing.
type stubSkillStore struct{ skills []*domain.Skill }

func (s *stubSkillStore) List() []*domain.Skill { return s.skills }
func (s *stubSkillStore) Get(id string) (*domain.Skill, error) {
	for _, sk := range s.skills {
		if sk.ID == id {
			return sk, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *stubSkillStore) Save(sk *domain.Skill) error { return nil }
func (s *stubSkillStore) Delete(id string) error      { return nil }

// stubMCPStore is a minimal MCPServerStore for testing.
type stubMCPStore struct{ servers []*domain.MCPServer }

func (s *stubMCPStore) List() []*domain.MCPServer { return s.servers }
func (s *stubMCPStore) Get(id string) (*domain.MCPServer, error) {
	for _, srv := range s.servers {
		if srv.ID == id {
			return srv, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *stubMCPStore) Save(srv *domain.MCPServer) error { return nil }
func (s *stubMCPStore) Delete(id string) error           { return nil }

// stubMCP is a minimal MCP manager stub for testing.
type stubMCP struct {
	tools   map[string][]contracts.MCPToolDTO // serverID -> tools
	running map[string]bool                   // serverID -> running
}

func (m *stubMCP) Connect(ctx context.Context, s *domain.MCPServer) ([]contracts.MCPToolDTO, error) {
	if tools, ok := m.tools[s.ID]; ok {
		return tools, nil
	}
	return nil, fmt.Errorf("not connected")
}
func (m *stubMCP) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) {
	tools, ok := m.tools[serverID]
	return tools, ok
}
func (m *stubMCP) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	return "ok", nil
}

func testToolbox(skills []*domain.Skill, servers []*domain.MCPServer, mcp *stubMCP) *Toolbox {
	return &Toolbox{
		Skills:     &stubSkillStore{skills: skills},
		MCPServers: &stubMCPStore{servers: servers},
		MCP:        mcp,
	}
}

func TestSkillSearch(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{
			{ID: "s1", Name: "git-helper", Description: "Help with git operations"},
			{ID: "s2", Name: "docker-pro", Description: "Docker container management"},
			{ID: "s3", Name: "code-review", Description: "Review code for bugs"},
		},
		nil, &stubMCP{},
	)
	out, err := tb.Execute(context.Background(), "skill_search", []byte(`{"query":"git"}`))
	if err != nil {
		t.Fatalf("skill_search: %v", err)
	}
	if !strings.Contains(out, "git-helper") {
		t.Errorf("expected git-helper in results, got: %s", out)
	}
	if strings.Contains(out, "docker-pro") {
		t.Errorf("docker-pro should not match git query, got: %s", out)
	}
}

func TestSkillSearchCaseInsensitive(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "Code-Review", Description: "Review code"}},
		nil, &stubMCP{},
	)
	out, err := tb.Execute(context.Background(), "skill_search", []byte(`{"query":"CODE"}`))
	if err != nil {
		t.Fatalf("skill_search: %v", err)
	}
	if !strings.Contains(out, "Code-Review") {
		t.Errorf("expected case-insensitive match, got: %s", out)
	}
}

func TestSkillSearchEmptyQuery(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "test", Description: "test"}},
		nil, &stubMCP{},
	)
	_, err := tb.Execute(context.Background(), "skill_search", []byte(`{"query":""}`))
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestSkillSearchNoMatch(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git", Description: "git tool"}},
		nil, &stubMCP{},
	)
	out, err := tb.Execute(context.Background(), "skill_search", []byte(`{"query":"nonexistent"}`))
	if err != nil {
		t.Fatalf("skill_search: %v", err)
	}
	if out != "No skills matched." {
		t.Errorf("expected no match message, got: %s", out)
	}
}

func TestSkillRead(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git-helper", Description: "Git help", Content: "# Git Helper\nUse git status."}},
		nil, &stubMCP{},
	)
	out, err := tb.Execute(context.Background(), "skill_read", []byte(`{"name":"git-helper"}`))
	if err != nil {
		t.Fatalf("skill_read: %v", err)
	}
	if !strings.Contains(out, "# Git Helper") {
		t.Errorf("expected skill content, got: %s", out)
	}
}

func TestSkillReadNotFound(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git-helper", Content: "content"}},
		nil, &stubMCP{},
	)
	_, err := tb.Execute(context.Background(), "skill_read", []byte(`{"name":"nonexistent"}`))
	if err == nil {
		t.Error("expected error for missing skill")
	}
}

func TestSkillListLimit(t *testing.T) {
	skills := []*domain.Skill{}
	for i := 0; i < 5; i++ {
		skills = append(skills, &domain.Skill{ID: "s", Name: "skill", Description: "desc"})
	}
	tb := testToolbox(skills, nil, &stubMCP{})
	out, err := tb.Execute(context.Background(), "skill_list", []byte(`{"limit":2}`))
	if err != nil {
		t.Fatalf("skill_list: %v", err)
	}
	// Should only contain 2 entries
	lines := strings.Count(out, "\n") + 1
	if lines != 2 {
		t.Errorf("expected 2 entries with limit=2, got %d lines: %s", lines, out)
	}
}

func TestMcpList(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
			{ID: "srv2", Name: "filesystem", Command: "npx", Enabled: false},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"srv1": {{Name: "create_issue", Description: "Create a GitHub issue"}},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "mcp_list", []byte(`{}`))
	if err != nil {
		t.Fatalf("mcp_list: %v", err)
	}
	var res struct {
		Count   int `json:"count"`
		Servers []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
			Tools   int    `json:"tools"`
		} `json:"servers"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if res.Count != 2 {
		t.Errorf("expected 2 servers, got %d", res.Count)
	}
	// github should be running with 1 tool
	var github *struct {
		Name    string `json:"name"`
		Running bool   `json:"running"`
		Tools   int    `json:"tools"`
	}
	for i := range res.Servers {
		if res.Servers[i].Name == "github" {
			github = &res.Servers[i]
		}
	}
	if github == nil || !github.Running || github.Tools != 1 {
		t.Errorf("expected github running with 1 tool, got: %+v", github)
	}
}

func TestToolListAll(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
			{ID: "srv2", Name: "fs", Command: "npx", Enabled: true},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"srv1": {{Name: "create_issue", Description: "Create issue", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`)}},
				"srv2": {{Name: "read_file", Description: "Read a file"}},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "tool_list", []byte(`{}`))
	if err != nil {
		t.Fatalf("tool_list: %v", err)
	}
	var res struct {
		Count int `json:"count"`
		Tools []struct {
			Name   string `json:"name"`
			Server string `json:"server"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if res.Count != 2 {
		t.Errorf("expected 2 tools, got %d", res.Count)
	}
}

func TestToolListByServer(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
			{ID: "srv2", Name: "fs", Command: "npx", Enabled: true},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"srv1": {{Name: "create_issue", Description: "Create issue"}},
				"srv2": {{Name: "read_file", Description: "Read file"}},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "tool_list", []byte(`{"server":"github"}`))
	if err != nil {
		t.Fatalf("tool_list: %v", err)
	}
	var res struct {
		Count int `json:"count"`
		Tools []struct {
			Name   string `json:"name"`
			Server string `json:"server"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if res.Count != 1 {
		t.Errorf("expected 1 tool from github, got %d", res.Count)
	}
	if len(res.Tools) > 0 && res.Tools[0].Server != "github" {
		t.Errorf("expected server=github, got %s", res.Tools[0].Server)
	}
}

func TestToolListNotRunning(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
		},
		&stubMCP{tools: map[string][]contracts.MCPToolDTO{}}, // no running servers
	)
	out, err := tb.Execute(context.Background(), "tool_list", []byte(`{}`))
	if err != nil {
		t.Fatalf("tool_list: %v", err)
	}
	var res struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if res.Count != 0 {
		t.Errorf("expected 0 tools from non-running server, got %d", res.Count)
	}
}

func TestToolSearch(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"srv1": {
					{Name: "create_issue", Description: "Create a GitHub issue"},
					{Name: "list_repos", Description: "List repositories"},
				},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "tool_search", []byte(`{"server":"github","query":"issue"}`))
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	var res struct {
		Count   int `json:"count"`
		Matches []struct {
			Name string `json:"name"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if res.Count != 1 {
		t.Errorf("expected 1 match, got %d", res.Count)
	}
	if len(res.Matches) > 0 && !strings.Contains(res.Matches[0].Name, "create_issue") {
		t.Errorf("expected create_issue match, got %s", res.Matches[0].Name)
	}
}

func TestToolSearchTokenMatch(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"srv1": {
					{Name: "create_issue", Description: "Create a GitHub issue for tracking bugs"},
				},
			},
		},
	)
	// "issue bug" — any token matches
	out, err := tb.Execute(context.Background(), "tool_search", []byte(`{"server":"github","query":"issue bug"}`))
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	var res struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if res.Count != 1 {
		t.Errorf("expected 1 match for token match, got %d", res.Count)
	}
}

func TestToolSchema(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"srv1": {
					{Name: "create_issue", Description: "Create issue", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`)},
				},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "tool_schema", []byte(`{"server":"github","tool":"create_issue"}`))
	if err != nil {
		t.Fatalf("tool_schema: %v", err)
	}
	var res struct {
		Server      string         `json:"server"`
		Tool        string         `json:"tool"`
		InputSchema map[string]any `json:"input_schema"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if res.Server != "github" || res.Tool != "create_issue" {
		t.Errorf("expected github/create_issue, got %s/%s", res.Server, res.Tool)
	}
	if res.InputSchema["type"] != "object" {
		t.Errorf("expected object schema, got: %v", res.InputSchema["type"])
	}
}

func TestToolSchemaNotFound(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"srv1": {{Name: "create_issue"}},
			},
		},
	)
	_, err := tb.Execute(context.Background(), "tool_schema", []byte(`{"server":"github","tool":"nonexistent"}`))
	if err == nil {
		t.Error("expected error for missing tool")
	}
}

func TestToolSchemaServerNotRunning(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.MCPServer{
			{ID: "srv1", Name: "github", Command: "npx", Enabled: true},
		},
		&stubMCP{tools: map[string][]contracts.MCPToolDTO{}}, // not running
	)
	_, err := tb.Execute(context.Background(), "tool_schema", []byte(`{"server":"github","tool":"create_issue"}`))
	if err == nil {
		t.Error("expected error for non-running server")
	}
}
