package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"
	docsinfra "nusashell/infrastructure/docs"
	"nusashell/infrastructure/jsonstore"
	"nusashell/infrastructure/memorystore"
	"nusashell/infrastructure/pluginfs"
	"nusashell/resources"
)

// stubSkillStore is a minimal SkillStore for testing.
type stubSkillStore struct{ skills []*domain.Skill }

func (s *stubSkillStore) List() []*domain.Skill { return s.skills }
func (s *stubSkillStore) Get(id, ownedBy string) (*domain.Skill, error) {
	for _, sk := range s.skills {
		if sk.ID == id {
			return sk, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *stubSkillStore) Save(sk *domain.Skill) error     { return nil }
func (s *stubSkillStore) Delete(id, ownedBy string) error { return nil }
func (s *stubSkillStore) ReadFile(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSkillStore) Files(id, ownedBy string) ([]domain.SkillFileEntry, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSkillStore) WriteFile(id, ownedBy, path, content string) error {
	return fmt.Errorf("not implemented")
}
func (s *stubSkillStore) Install(zipData []byte) (string, error) {
	return "", fmt.Errorf("not supported")
}
func (s *stubSkillStore) MountPluginSkills(pluginID, dir string) error { return nil }
func (s *stubSkillStore) UnmountPluginSkills(pluginID string) error    { return nil }

// stubPluginStore is a minimal PluginStore for testing.
type stubPluginStore struct{ plugins []*domain.Plugin }

func (s *stubPluginStore) List() ([]*domain.Plugin, error) { return s.plugins, nil }
func (s *stubPluginStore) Get(id string) (*domain.Plugin, error) {
	for _, p := range s.plugins {
		if p.Manifest.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *stubPluginStore) Install(sourceDir string) (*domain.Plugin, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubPluginStore) Uninstall(id string) error   { return nil }
func (s *stubPluginStore) Save(p *domain.Plugin) error { return nil }
func (s *stubPluginStore) Delete(id string) error      { return nil }

// stubMCP is a minimal MCP manager stub for testing.
type stubMCP struct {
	tools        map[string][]contracts.MCPToolDTO // serverID -> tools
	lastServerID string
	lastTool     string
	lastArgs     map[string]any
	connectCount int             // incremented on each Connect call
	connected    map[string]bool // serverID -> connected (set by Connect)
}

func (m *stubMCP) Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	m.connectCount++
	if m.connected == nil {
		m.connected = map[string]bool{}
	}
	m.connected[p.Manifest.MCPServerID()] = true
	if tools, ok := m.tools[p.Manifest.MCPServerID()]; ok {
		return tools, nil
	}
	return nil, fmt.Errorf("not connected")
}
func (m *stubMCP) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) {
	// When connected-tracking is active (non-nil map), only return tools
	// for explicitly connected servers. When nil (legacy mode), any server
	// with tools in the map is treated as connected.
	if m.connected != nil {
		if m.connected[serverID] {
			tools, ok := m.tools[serverID]
			return tools, ok
		}
		return nil, false
	}
	tools, ok := m.tools[serverID]
	return tools, ok
}
func (m *stubMCP) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	m.lastServerID = serverID
	m.lastTool = toolName
	m.lastArgs = args
	return "ok", nil
}

func testToolbox(skills []*domain.Skill, plugins []*domain.Plugin, mcp *stubMCP) *Toolbox {
	return &Toolbox{
		Skills:    &stubSkillStore{skills: skills},
		Plugins:   &stubPluginStore{plugins: plugins},
		MCP:       mcp,
		Contracts: NewFileContractReader(),
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
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"search","query":"git"}`))
	if err != nil {
		t.Fatalf("skill search: %v", err)
	}
	if !strings.Contains(out, "git-helper") {
		t.Errorf("expected git-helper in results, got: %s", out)
	}
	if strings.Contains(out, "docker-pro") {
		t.Errorf("docker-pro should not match git query, got: %s", out)
	}
}

// stubSkillSearcher returns scripted ranked results for skill search tests.
type stubSkillSearcher struct {
	ids []string
}

func (s *stubSkillSearcher) SearchSkills(_ context.Context, _ string, topK int) ([]application.SearchResult, error) {
	out := make([]application.SearchResult, 0, topK)
	for _, id := range s.ids {
		out = append(out, application.SearchResult{ID: id})
	}
	return out, nil
}

func TestSkillSearchUsesRankedSearcher(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{
			{ID: "s1", Name: "git-helper", Description: "Help with git operations"},
			{ID: "s2", Name: "git-advanced", Description: "Advanced git workflows"},
			{ID: "docker-pro", Name: "docker-pro", Description: "Docker container management"},
		},
		nil, &stubMCP{},
	)
	tb.SkillSearcher = &stubSkillSearcher{ids: []string{"s2", "s1"}}
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"search","query":"git"}`))
	if err != nil {
		t.Fatalf("skill search: %v", err)
	}
	if strings.Index(out, "git-advanced") > strings.Index(out, "git-helper") {
		t.Errorf("searcher ranking should be preserved (s2 before s1), got: %s", out)
	}
	if strings.Contains(out, "docker-pro") {
		t.Errorf("docker-pro should not match git query, got: %s", out)
	}
}

func TestSkillSearchRecallFallback(t *testing.T) {
	// The ranker misses an inflected name; the substring fallback keeps it
	// so recall never regresses below the plain matcher.
	tb := testToolbox(
		[]*domain.Skill{
			{ID: "s1", Name: "review", Description: "Review code for bugs"},
			{ID: "s2", Name: "reviews", Description: "Handle review sessions"},
		},
		nil, &stubMCP{},
	)
	tb.SkillSearcher = &stubSkillSearcher{ids: []string{"s1"}}
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"search","query":"review"}`))
	if err != nil {
		t.Fatalf("skill search: %v", err)
	}
	if !strings.Contains(out, "reviews") {
		t.Errorf("recall fallback should include substring matches the ranker missed, got: %s", out)
	}
	if strings.Index(out, "review") > strings.Index(out, "reviews") {
		t.Errorf("ranked hit should come before the fallback hit, got: %s", out)
	}
}

func TestSkillSearchCaseInsensitive(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "Code-Review", Description: "Review code"}},
		nil, &stubMCP{},
	)
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"search","query":"CODE"}`))
	if err != nil {
		t.Fatalf("skill search: %v", err)
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
	_, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"search","query":""}`))
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestSkillSearchNoMatch(t *testing.T) {
	tb := testToolbox(
		[]*domain.Skill{{ID: "s1", Name: "git", Description: "git tool"}},
		nil, &stubMCP{},
	)
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"search","query":"nonexistent"}`))
	if err != nil {
		t.Fatalf("skill search: %v", err)
	}
	if !strings.Contains(out, "count: 0") {
		t.Errorf("expected count: 0 in YAML output, got: %s", out)
	}
}

func TestSkillListLimit(t *testing.T) {
	skills := []*domain.Skill{}
	for i := 0; i < 5; i++ {
		skills = append(skills, &domain.Skill{ID: "s", Name: "skill", Description: "desc"})
	}
	tb := testToolbox(skills, nil, &stubMCP{})
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"list","limit":2}`))
	if err != nil {
		t.Fatalf("skill list: %v", err)
	}
	// Should only contain 2 entries
	if !strings.Contains(out, "count: 2") {
		t.Errorf("expected count: 2 with limit=2, got: %s", out)
	}
}

func TestMcpList(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
			{Manifest: domain.PluginManifest{ID: "srv2", Name: "filesystem", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {{Name: "create_issue", Description: "Create a GitHub issue"}},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "mcp_list", []byte(`{}`))
	if err != nil {
		t.Fatalf("mcp_list: %v", err)
	}
	if !strings.Contains(out, "count: 2") {
		t.Errorf("expected 2 plugins, got: %s", out)
	}
	// github should be running with 1 tool — rendered as JSONL
	if !strings.Contains(out, `"name":"github"`) || !strings.Contains(out, `"running":true`) || !strings.Contains(out, `"tools":1`) {
		t.Errorf("expected github running with 1 tool in JSONL, got: %s", out)
	}
}

func TestToolListAll(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
			{Manifest: domain.PluginManifest{ID: "srv2", Name: "fs", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {{Name: "create_issue", Description: "Create issue", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}}}`)}},
				"plugin:srv2": {{Name: "read_file", Description: "Read a file"}},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "tool_list", []byte(`{}`))
	if err != nil {
		t.Fatalf("tool_list: %v", err)
	}
	if !strings.Contains(out, "count: 2") {
		t.Errorf("expected 2 tools, got: %s", out)
	}
	// tool_list stays schema-free so huge catalogs stay token-cheap;
	// full input schemas live behind tool_schema.
	if strings.Contains(out, "\"parameters\"") {
		t.Errorf("tool_list must not inline parameters; use tool_schema, got: %s", out)
	}
}

func TestToolListByServer(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
			{Manifest: domain.PluginManifest{ID: "srv2", Name: "fs", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {{Name: "create_issue", Description: "Create issue"}},
				"plugin:srv2": {{Name: "read_file", Description: "Read file"}},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "tool_list", []byte(`{"server":"srv1"}`))
	if err != nil {
		t.Fatalf("tool_list: %v", err)
	}
	if !strings.Contains(out, "count: 1") {
		t.Errorf("expected 1 tool from srv1, got: %s", out)
	}
	if !strings.Contains(out, `"server":"srv1"`) {
		t.Errorf("expected server=srv1 in JSONL, got: %s", out)
	}
}

func TestToolListByPluginID(t *testing.T) {
	// tool_list accepts the plugin id (e.g. "nusashell.terminal") — the
	// only accepted form. Refs use the plugin id as prefix.
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "nusashell.terminal", Name: "Terminal", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:nusashell.terminal": {{Name: "exec", Description: "Run command"}},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "tool_list", []byte(`{"server":"nusashell.terminal"}`))
	if err != nil {
		t.Fatalf("tool_list by plugin id: %v", err)
	}
	if !strings.Contains(out, "count: 1") {
		t.Errorf("expected 1 tool via plugin id, got: %s", out)
	}
	if !strings.Contains(out, `"ref":"nusashell.terminal:exec"`) || !strings.Contains(out, `"name":"exec"`) {
		t.Errorf("expected ref+name in JSONL, got: %s", out)
	}
}

func TestToolListNotRunning(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{tools: map[string][]contracts.MCPToolDTO{}}, // no running servers
	)
	out, err := tb.Execute(context.Background(), "tool_list", []byte(`{}`))
	if err != nil {
		t.Fatalf("tool_list: %v", err)
	}
	if !strings.Contains(out, "count: 0") {
		t.Errorf("expected 0 tools from non-running server, got: %s", out)
	}
}

func TestMcpSearch(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {
					{Name: "create_issue", Description: "Create a GitHub issue", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`)},
					{Name: "list_repos", Description: "List repositories"},
				},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "mcp_search", []byte(`{"server":"srv1","query":"issue"}`))
	if err != nil {
		t.Fatalf("mcp_search: %v", err)
	}
	if !strings.Contains(out, "count: 1") {
		t.Errorf("expected 1 match, got: %s", out)
	}
	if !strings.Contains(out, "create_issue") {
		t.Errorf("expected create_issue match, got: %s", out)
	}
	// mcp_search returns a compact discovery payload (ref/name/description).
	// Full input schemas live behind tool_schema so catalogs with hundreds
	// of tools stay token-cheap to search.
	if strings.Contains(out, "\"parameters\"") {
		t.Errorf("mcp_search must not inline parameters; call tool_schema instead, got: %s", out)
	}
}

func TestMcpSearchTokenMatch(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {
					{Name: "create_issue", Description: "Create a GitHub issue for tracking bugs"},
				},
			},
		},
	)
	// "issue bug" — any token matches
	out, err := tb.Execute(context.Background(), "mcp_search", []byte(`{"server":"srv1","query":"issue bug"}`))
	if err != nil {
		t.Fatalf("mcp_search: %v", err)
	}
	if !strings.Contains(out, "count: 1") {
		t.Errorf("expected 1 match for token match, got: %s", out)
	}
}

func TestMcpSearchRanksByRelevance(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv2", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv2": {
				{Name: "list_files", Description: "List files in a directory"},
				{Name: "read_file", Description: "Read a file from disk"},
			},
		}},
	)
	out, err := tb.Execute(context.Background(), "mcp_search", []byte(`{"query":"file"}`))
	if err != nil {
		t.Fatalf("mcp_search: %v", err)
	}
	// Both match by substring; BM25 ranks read_file (the query token occurs
	// in its text) above list_files (only the plural "files" occurs).
	if strings.Index(out, "read_file") > strings.Index(out, "list_files") {
		t.Errorf("read_file should rank above list_files, got: %s", out)
	}
}

func TestMcpSearchBoundedByLimit(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv2", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv2": {
				{Name: "read_file", Description: "Read a file"},
				{Name: "write_file", Description: "Write a file"},
				{Name: "list_files", Description: "List files"},
			},
		}},
	)
	out, err := tb.Execute(context.Background(), "mcp_search", []byte(`{"query":"file","limit":2}`))
	if err != nil {
		t.Fatalf("mcp_search: %v", err)
	}
	if !strings.Contains(out, "count: 2") {
		t.Errorf("mcp_search must honor the limit, got: %s", out)
	}
}

func TestToolSchema(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {
					{Name: "create_issue", Description: "Create issue", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`)},
				},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "tool_schema", []byte(`{"server":"srv1","tool":"create_issue"}`))
	if err != nil {
		t.Fatalf("tool_schema: %v", err)
	}
	// server/tool fields removed from meta (echo input args); the tool
	// name is still in the JSONL body as "name":"create_issue".
	// Full tool definition as a single JSONL line.
	if !strings.Contains(out, `"name":"create_issue"`) {
		t.Errorf("expected tool name in JSONL, got: %s", out)
	}
	if !strings.Contains(out, `"parameters":`) {
		t.Errorf("expected parameters field in JSONL, got: %s", out)
	}
	if !strings.Contains(out, `"required":["title"]`) {
		t.Errorf("expected required array in JSONL, got: %s", out)
	}
}

func TestMcpSearchAllServers(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
			{Manifest: domain.PluginManifest{ID: "srv2", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {
					{Name: "create_issue", Description: "Create a GitHub issue"},
				},
				"plugin:srv2": {
					{Name: "read_file", Description: "Read a file from disk", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
					{Name: "list_files", Description: "List files in a directory"},
				},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "mcp_search", []byte(`{"query":"file"}`))
	if err != nil {
		t.Fatalf("mcp_search: %v", err)
	}
	if !strings.Contains(out, "count: 2") {
		t.Errorf("expected 2 matches, got: %s", out)
	}
	// mcp_search must return a ref the model can pass to mcp_call.
	if !strings.Contains(out, `"ref":"srv2:read_file"`) {
		t.Errorf("expected ref srv2:read_file, got: %s", out)
	}
	if !strings.Contains(out, `"ref":"srv2:list_files"`) {
		t.Errorf("expected ref srv2:list_files, got: %s", out)
	}
	if strings.Contains(out, "create_issue") {
		t.Errorf("github tool should not match 'file' query, got: %s", out)
	}
	// Schemas are never inlined in search results — tool_schema owns them.
	if strings.Contains(out, `"parameters"`) {
		t.Errorf("mcp_search must not include parameters, got: %s", out)
	}
}

func TestMcpSearchWithServer(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
			{Manifest: domain.PluginManifest{ID: "srv2", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {{Name: "create_issue", Description: "Create a GitHub issue"}},
				"plugin:srv2": {{Name: "read_file", Description: "Read a file"}},
			},
		},
	)
	out, err := tb.Execute(context.Background(), "mcp_search", []byte(`{"server":"srv1","query":"issue"}`))
	if err != nil {
		t.Fatalf("mcp_search: %v", err)
	}
	if !strings.Contains(out, "count: 1") {
		t.Errorf("expected 1 match, got: %s", out)
	}
	if !strings.Contains(out, `"ref":"srv1:create_issue"`) {
		t.Errorf("expected ref srv1:create_issue, got: %s", out)
	}
}

func TestMcpSearchEmptyQuery(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	_, err := tb.Execute(context.Background(), "mcp_search", []byte(`{"query":""}`))
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestMcpCallExecutes(t *testing.T) {
	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv1": {{Name: "read_file", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
		},
	}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	out, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"ref":"srv1:read_file","arguments_json":"{\"path\":\"/etc/hosts\"}"}`))
	if err != nil {
		t.Fatalf("mcp_call: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("expected tool output, got: %s", out)
	}
	if mcp.lastServerID != "plugin:srv1" {
		t.Errorf("expected serverID plugin:srv1, got %s", mcp.lastServerID)
	}
	if mcp.lastTool != "read_file" {
		t.Errorf("expected tool read_file, got %s", mcp.lastTool)
	}
	if mcp.lastArgs["path"] != "/etc/hosts" {
		t.Errorf("expected path argument passed through, got %v", mcp.lastArgs)
	}
}

func TestMcpCallStaleRef(t *testing.T) {
	// Server was connected when searched, but disconnected before mcp_call.
	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv1": {{Name: "read_file", Description: "Read a file"}},
		},
		connected: map[string]bool{}, // strict mode: nothing connected
	}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	_, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"ref":"srv1:read_file","arguments_json":"{}"}`))
	if err == nil {
		t.Fatal("expected error for stale ref")
	}
	if !strings.Contains(err.Error(), "not running") && !strings.Contains(err.Error(), "STALE") {
		t.Errorf("expected stale/not-running error, got %v", err)
	}
}

func TestMcpCallMalformedRef(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	_, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"ref":"not_a_valid_ref","arguments_json":"{}"}`))
	if err == nil {
		t.Fatal("expected error for malformed ref")
	}
}

func TestMcpCallMissingRef(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	_, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"arguments_json":"{}"}`))
	if err == nil {
		t.Fatal("expected error for missing ref")
	}
}

func TestMcpCallMissingArgumentsJSONDefaultsToEmpty(t *testing.T) {
	// arguments_json is optional; omitting it defaults to {}.
	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv1": {{Name: "list", Description: "List things"}},
		},
	}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	out, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"ref":"srv1:list"}`))
	if err != nil {
		t.Fatalf("mcp_call: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("expected tool output, got: %s", out)
	}
	if mcp.lastArgs == nil {
		t.Error("expected empty args map, got nil")
	}
	if len(mcp.lastArgs) != 0 {
		t.Errorf("expected empty args, got %v", mcp.lastArgs)
	}
}

func TestMcpCallInvalidArgumentsJSON(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	_, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"ref":"srv1:read","arguments_json":"not json"}`))
	if err == nil {
		t.Fatal("expected error for invalid arguments_json")
	}
}

// TestMcpCallSchemaAdvertisesArgumentsJSON is a regression test for
// conv_f159e2e234a900e4 + conv_cefd2640b3b2f3a4 + conv_42ac5a9a2b274518:
// dynamic MCP tool arguments must travel in a statically representable
// string field. The original schema wrapped args in a required "additional"
// string; the open-object variant (additionalProperties:true, no fixed
// properties) made Luna emit arguments:{} every round because function-call
// generation has no affordance for properties absent from the schema. The
// schema keeps arguments_json as a string field (not an open object) so
// function-call generation always produces a predictable payload. It is no
// longer required — omitting it defaults to {} — but the property type is
// string to maintain provider compatibility (avoiding the open-object bug
// that made Luna emit arguments:{} every round).
func TestMcpCallSchemaAdvertisesArgumentsJSON(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	var def *application.ToolInfo
	for _, ti := range tb.ListTools() {
		if ti.Name == "mcp_call" {
			def = &ti
			break
		}
	}
	if def == nil {
		t.Fatal("mcp_call not in ListTools()")
	}
	props := def.InputSchema["properties"].(map[string]any)
	argsSchema, ok := props["arguments_json"].(map[string]any)
	if !ok {
		t.Fatalf("arguments_json schema missing or wrong type: %#v", props)
	}
	if argsSchema["type"] != "string" {
		t.Errorf("arguments_json.type = %v, want string", argsSchema["type"])
	}
	if _, hasObjectArgs := props["arguments"]; hasObjectArgs {
		t.Errorf("mcp_call must not advertise an object 'arguments' field, got %#v", props)
	}
	reqAny, ok := def.InputSchema["required"].([]any)
	if !ok {
		reqStrings, ok2 := def.InputSchema["required"].([]string)
		if !ok2 || len(reqStrings) == 0 {
			t.Fatal("mcp_call schema must declare required fields")
		}
		reqAny = make([]any, len(reqStrings))
		for i, r := range reqStrings {
			reqAny[i] = r
		}
	}
	if len(reqAny) == 0 {
		t.Fatal("mcp_call schema must declare required fields")
	}
	hasRef := false
	for _, r := range reqAny {
		if r == "ref" {
			hasRef = true
		}
	}
	if !hasRef {
		t.Errorf("required must include ref, got %v", reqAny)
	}
}

func TestMcpCallAcceptsObjectArgumentsJSON(t *testing.T) {
	// Canonical form: arguments_json is a JSON object directly (matching MCP spec).
	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv1": {{Name: "read", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
		},
	}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	_, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"ref":"srv1:read","arguments_json":{"path":"/etc/hosts","offset":0}}`))
	if err != nil {
		t.Fatalf("mcp_call: %v", err)
	}
	if mcp.lastArgs["path"] != "/etc/hosts" {
		t.Errorf("expected path=/etc/hosts passed through, got %v", mcp.lastArgs)
	}
	if mcp.lastArgs["offset"] != float64(0) {
		t.Errorf("expected offset=0 passed through, got %v", mcp.lastArgs)
	}
}

func TestMcpCallPassesArgumentsJSONThrough(t *testing.T) {
	// End-to-end: the arguments_json string is parsed and its keys reach
	// the MCP server unchanged — no additional wrapper, no empty object.
	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv1": {{Name: "read", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}},
		},
	}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	_, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"ref":"srv1:read","arguments_json":"{\"path\":\"/etc/hosts\",\"offset\":0}"}`))
	if err != nil {
		t.Fatalf("mcp_call: %v", err)
	}
	if mcp.lastArgs["path"] != "/etc/hosts" {
		t.Errorf("expected path=/etc/hosts passed through, got %v", mcp.lastArgs)
	}
	if mcp.lastArgs["offset"] != float64(0) {
		t.Errorf("expected offset=0 passed through, got %v", mcp.lastArgs)
	}
	if _, hasAdditional := mcp.lastArgs["additional"]; hasAdditional {
		t.Errorf("arguments.additional wrapper leaked to MCP server: %v", mcp.lastArgs)
	}
}

func TestMcpCallEmptyArgumentsJSON(t *testing.T) {
	// Tools that need no arguments still work with an empty object payload.
	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			"plugin:srv1": {{Name: "list", Description: "List things"}},
		},
	}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	out, err := tb.Execute(context.Background(), "mcp_call", []byte(`{"ref":"srv1:list","arguments_json":"{}"}`))
	if err != nil {
		t.Fatalf("mcp_call: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("expected tool output, got: %s", out)
	}
	if mcp.lastArgs == nil {
		t.Error("expected empty args map, got nil")
	}
}

func TestToolSchemaNotFound(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {{Name: "create_issue"}},
			},
		},
	)
	_, err := tb.Execute(context.Background(), "tool_schema", []byte(`{"server":"srv1","tool":"nonexistent"}`))
	if err == nil {
		t.Error("expected error for missing tool")
	}
}

func TestToolSchemaServerNotRunning(t *testing.T) {
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{tools: map[string][]contracts.MCPToolDTO{}}, // not running
	)
	_, err := tb.Execute(context.Background(), "tool_schema", []byte(`{"server":"srv1","tool":"create_issue"}`))
	if err == nil {
		t.Error("expected error for non-running server")
	}
}

func TestMCPDynamicToolsNotAdvertised(t *testing.T) {
	mcp := &stubMCP{tools: map[string][]contracts.MCPToolDTO{
		"plugin:files": {{Name: "read", Description: "read a file"}},
	}}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "files", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	for _, ti := range tb.ListTools() {
		if strings.HasPrefix(ti.Name, "mcp__") {
			t.Fatalf("dynamic MCP tool %q should not be advertised to the agent", ti.Name)
		}
	}
}

func TestMCPDynamicToolsNotCallableByName(t *testing.T) {
	mcp := &stubMCP{tools: map[string][]contracts.MCPToolDTO{
		"plugin:files": {{Name: "read", Description: "read a file"}},
	}}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "files", Name: "files", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	// Not in ListTools (cache stability) ...
	for _, ti := range tb.ListTools() {
		if strings.HasPrefix(ti.Name, "mcp__") {
			t.Fatalf("mcp__ tool %q should not be advertised", ti.Name)
		}
	}
	// ... and the legacy mcp__ name is NOT callable: the only execution
	// contract is mcp_call with a ref.
	if out, err := tb.Execute(context.Background(), "mcp__files__read", nil); err == nil {
		t.Fatalf("mcp__ tool must not be executable, got: %s", out)
	}
}

// --- mcp management tools ---

type recordedStubPluginStore struct {
	*stubPluginStore
	installedDirs []string
	deleted       []string
}

func newRecordedStub(plugins []*domain.Plugin) *recordedStubPluginStore {
	return &recordedStubPluginStore{stubPluginStore: &stubPluginStore{plugins: plugins}}
}

func (s *recordedStubPluginStore) Install(sourceDir string) (*domain.Plugin, error) {
	s.installedDirs = append(s.installedDirs, sourceDir)
	p := &domain.Plugin{Manifest: domain.PluginManifest{
		ID:      "plugin:fake",
		Name:    "Fake",
		Version: "1.0.0",
		Icon:    "🧪",
		MCP:     domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "nope"},
	}}
	if len(s.plugins) == 0 {
		s.plugins = []*domain.Plugin{p}
	}
	return p, nil
}

func (s *recordedStubPluginStore) Delete(id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

type stubDropper struct {
	droppedServers map[string]bool
}

func (d *stubDropper) Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	return nil, nil
}
func (d *stubDropper) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) { return nil, false }
func (d *stubDropper) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	return "ok", nil
}
func (d *stubDropper) Drop(serverID string) { d.droppedServers[serverID] = true }

func TestMcpRegister(t *testing.T) {
	store := newRecordedStub(nil)
	tb := &Toolbox{Plugins: store, MCP: &stubMCP{tools: map[string][]contracts.MCPToolDTO{}}}
	dir := t.TempDir()
	absPluginSource, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	payload, err := json.Marshal(map[string]string{"source": dir})
	if err != nil {
		t.Fatal(err)
	}
	out, err := tb.Execute(context.Background(), "mcp_register", payload)
	if err != nil {
		t.Fatalf("mcp_register: %v", err)
	}
	if !strings.Contains(out, "status: registered") {
		t.Fatalf("unexpected output %q", out)
	}
	if len(store.installedDirs) != 1 || store.installedDirs[0] != absPluginSource {
		t.Fatalf("expected install of %s, got %v", absPluginSource, store.installedDirs)
	}
}

func TestMcpRegisterMissingSource(t *testing.T) {
	tb := &Toolbox{Plugins: newRecordedStub(nil), MCP: &stubMCP{}}
	_, err := tb.Execute(context.Background(), "mcp_register", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "source is required") {
		t.Fatalf("expected source-required error, got %v", err)
	}
}

func TestMcpEnable(t *testing.T) {
	plugin := &domain.Plugin{Manifest: domain.PluginManifest{ID: "nusashell.demo", Name: "Demo", Version: "1.0.0", Icon: "🧩", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "nope"}}}
	serverID := plugin.Manifest.MCPServerID()
	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			serverID: {{Name: "demo_ping", Description: "ping", InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}`)}},
		},
		connected: map[string]bool{}, // strict mode: not connected until Connect
	}
	tb := &Toolbox{Plugins: &stubPluginStore{plugins: []*domain.Plugin{plugin}}, MCP: mcp}
	out, err := tb.Execute(context.Background(), "mcp_enable", []byte(`{"id":"nusashell.demo"}`))
	if err != nil {
		t.Fatalf("mcp_enable: %v", err)
	}
	if !strings.Contains(out, "status: enabled") || !strings.Contains(out, "tools: 1") {
		t.Fatalf("expected status: enabled and tools: 1 in output, got %q", out)
	}
	// mcp_enable returns only status + count — no tool dump.
	// The agent must use tool_list or mcp_search to discover tools.
	if strings.Contains(out, `"name":"mcp__Demo__demo_ping"`) {
		t.Errorf("mcp_enable must not dump tool definitions, got %q", out)
	}
}

func TestMcpEnableIdempotent(t *testing.T) {
	plugin := &domain.Plugin{Manifest: domain.PluginManifest{ID: "nusashell.demo", Name: "Demo", Version: "1.0.0", Icon: "🧩", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "nope"}}}
	serverID := plugin.Manifest.MCPServerID()
	mcp := &stubMCP{
		tools: map[string][]contracts.MCPToolDTO{
			serverID: {{Name: "demo_ping", Description: "ping", InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}`)}},
		},
		connected: map[string]bool{}, // strict mode: track Connect calls
	}
	tb := &Toolbox{Plugins: &stubPluginStore{plugins: []*domain.Plugin{plugin}}, MCP: mcp}

	// First enable: connects, returns status + count only.
	out1, err := tb.Execute(context.Background(), "mcp_enable", []byte(`{"id":"nusashell.demo"}`))
	if err != nil {
		t.Fatalf("first mcp_enable: %v", err)
	}
	if !strings.Contains(out1, "status: enabled") {
		t.Fatalf("first call: expected status: enabled, got %q", out1)
	}
	if strings.Contains(out1, `"name":"mcp__Demo__demo_ping"`) {
		t.Fatalf("first call: must not dump tools, got %q", out1)
	}
	if mcp.connectCount != 1 {
		t.Fatalf("first call: expected 1 Connect, got %d", mcp.connectCount)
	}

	// Second enable: already connected — must NOT reconnect and must NOT
	// dump tools. Return a short already_enabled signal so the agent
	// stops re-enabling and moves on to tool_list/mcp_search.
	out2, err := tb.Execute(context.Background(), "mcp_enable", []byte(`{"id":"nusashell.demo"}`))
	if err != nil {
		t.Fatalf("second mcp_enable: %v", err)
	}
	if !strings.Contains(out2, "already_enabled") {
		t.Fatalf("second call: expected already_enabled status, got %q", out2)
	}
	if strings.Contains(out2, `"name":"mcp__Demo__demo_ping"`) {
		t.Fatalf("second call: must not dump tools, got %q", out2)
	}
	if mcp.connectCount != 1 {
		t.Fatalf("second call: must not reconnect, expected 1 Connect, got %d", mcp.connectCount)
	}
}

func TestMcpEnableUnknown(t *testing.T) {
	tb := &Toolbox{Plugins: &stubPluginStore{}, MCP: &stubMCP{}}
	_, err := tb.Execute(context.Background(), "mcp_enable", []byte(`{"id":"nope"}`))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestMcpDisable(t *testing.T) {
	dropper := &stubDropper{droppedServers: map[string]bool{}}
	tb := &Toolbox{Plugins: &stubPluginStore{}, MCP: dropper}
	out, err := tb.Execute(context.Background(), "mcp_disable", []byte(`{"id":"nusashell.demo"}`))
	if err != nil {
		t.Fatalf("mcp_disable: %v", err)
	}
	if !strings.Contains(out, "disabled") {
		t.Fatalf("unexpected output %q", out)
	}
	if !dropper.droppedServers["plugin:nusashell.demo"] {
		t.Fatalf("expected drop of plugin:nusashell.demo")
	}
}

func TestMcpUnregister(t *testing.T) {
	store := newRecordedStub([]*domain.Plugin{{Manifest: domain.PluginManifest{ID: "demo", Name: "Demo", Version: "1.0.0", Icon: "🧩", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "nope"}}}})
	tb := &Toolbox{Plugins: store, MCP: &stubDropper{droppedServers: map[string]bool{}}}
	out, err := tb.Execute(context.Background(), "mcp_unregister", []byte(`{"id":"demo"}`))
	if err != nil {
		t.Fatalf("mcp_unregister: %v", err)
	}
	if !strings.Contains(out, "unregistered") {
		t.Fatalf("unexpected output %q", out)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "demo" {
		t.Fatalf("expected delete demo, got %v", store.deleted)
	}
}

// advertisedNames mirrors the provider-facing roster: non-family built-ins
// plus the dispatcher family roots (see tool_dispatch.go).
func advertisedNames(tb *Toolbox) map[string]bool {
	names := map[string]bool{}
	for _, ti := range append(tb.ListTools(), application.DispatcherToolInfos()...) {
		names[ti.Name] = true
	}
	return names
}

func TestListToolsIncludesAutomation(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	names := advertisedNames(tb)
	for _, want := range []string{
		"ci_run",
		"ci_run_status", "ci_logs", "ci_cancel",
		"automation_list", "automation_read", "automation_validate", "automation_create",
		"automation_enable", "automation_disable", "automation_status",
		"schedule_once", "schedule_every", "wait_until",
	} {
		if !names[want] {
			t.Fatalf("ListTools missing %q", want)
		}
	}
}

func TestListToolsIncludesMcpManagement(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	names := map[string]bool{}
	for _, ti := range tb.ListTools() {
		names[ti.Name] = true
	}
	for _, want := range []string{"mcp_register", "mcp_enable", "mcp_disable", "mcp_unregister"} {
		if !names[want] {
			t.Fatalf("ListTools missing %q", want)
		}
	}
}

func TestMemorySaveExactDuplicateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	fragments, err := memorystore.NewFragments(dir)
	if err != nil {
		t.Fatal(err)
	}
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.Fragments = fragments
	out, err := tb.Execute(context.Background(), "memory", []byte(`{"op":"save","content":"User prefers Indonesian\n","category":"user"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status: saved") {
		t.Fatalf("first save output = %s", out)
	}
	out, err = tb.Execute(context.Background(), "memory", []byte(`{"op":"save","content":"  User prefers Indonesian  \r\n","category":"user"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status: unchanged") || !strings.Contains(out, "reason: exact_duplicate") {
		t.Fatalf("duplicate save output = %s", out)
	}
	if got := len(fragments.List(domain.FragmentSearchFilter{})); got != 1 {
		t.Fatalf("fragment count = %d, want 1", got)
	}
}

func TestListToolsIncludesMemoryReplace(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	names := advertisedNames(tb)
	for _, want := range []string{"memory"} {
		if !names[want] {
			t.Fatalf("ListTools missing %q", want)
		}
	}
}

// --- mcp_install tests ---

type stubPluginInstaller struct {
	catalog []domain.PluginCatalogEntry
	lastReq domain.PluginInstallRequest
	plugin  *domain.Plugin
	err     error
}

func (s *stubPluginInstaller) Catalog(ctx context.Context) ([]domain.PluginCatalogEntry, error) {
	return s.catalog, nil
}
func (s *stubPluginInstaller) CheckUpdates(ctx context.Context, installed []*domain.Plugin) ([]domain.PluginCatalogEntry, error) {
	return nil, nil
}
func (s *stubPluginInstaller) Update(ctx context.Context, pluginID string) (*domain.Plugin, error) {
	return s.Install(ctx, domain.PluginInstallRequest{Source: domain.InstallSourceCatalog, ID: pluginID})
}
func (s *stubPluginInstaller) Install(ctx context.Context, req domain.PluginInstallRequest) (*domain.Plugin, error) {
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	if s.plugin != nil {
		return s.plugin, nil
	}
	return &domain.Plugin{Manifest: domain.PluginManifest{
		ID:      "nusashell.demo",
		Name:    "Demo",
		Version: "1.0.0",
		Icon:    "🧩",
		MCP:     domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "demo"},
	}}, nil
}

func TestMcpInstallCatalog(t *testing.T) {
	inst := &stubPluginInstaller{}
	tb := &Toolbox{PluginInstaller: inst, Plugins: &stubPluginStore{}, MCP: &stubDropper{droppedServers: map[string]bool{}}}
	out, err := tb.Execute(context.Background(), "mcp_install", []byte(`{"source":"catalog","id":"notes"}`))
	if err != nil {
		t.Fatalf("mcp_install catalog: %v", err)
	}
	if !strings.Contains(out, "status: installed") {
		t.Fatalf("unexpected output %q", out)
	}
	if inst.lastReq.Source != domain.InstallSourceCatalog || inst.lastReq.ID != "notes" {
		t.Fatalf("unexpected request: %+v", inst.lastReq)
	}
}

func TestMcpInstallGithub(t *testing.T) {
	inst := &stubPluginInstaller{}
	tb := &Toolbox{PluginInstaller: inst, Plugins: &stubPluginStore{}, MCP: &stubDropper{droppedServers: map[string]bool{}}}
	_, err := tb.Execute(context.Background(), "mcp_install", []byte(`{"source":"github","url":"jahrulnr/NusaShell-mcp","ref":"master"}`))
	if err != nil {
		t.Fatalf("mcp_install github: %v", err)
	}
	if inst.lastReq.Source != domain.InstallSourceGitHub || inst.lastReq.URL != "jahrulnr/NusaShell-mcp" || inst.lastReq.Ref != "master" {
		t.Fatalf("unexpected request: %+v", inst.lastReq)
	}
}

func TestMcpInstallMissingFields(t *testing.T) {
	tb := &Toolbox{PluginInstaller: &stubPluginInstaller{}, Plugins: &stubPluginStore{}}
	_, err := tb.Execute(context.Background(), "mcp_install", []byte(`{"source":"catalog"}`))
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("expected catalog id-required error, got %v", err)
	}
	_, err = tb.Execute(context.Background(), "mcp_install", []byte(`{"source":"github"}`))
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("expected github url-required error, got %v", err)
	}
	_, err = tb.Execute(context.Background(), "mcp_install", []byte(`{"source":"zip"}`))
	if err == nil || !strings.Contains(err.Error(), `source must be`) {
		t.Fatalf("expected unsupported source error, got %v", err)
	}
}

func TestListToolsIncludesMcpInstall(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	names := map[string]bool{}
	for _, ti := range tb.ListTools() {
		names[ti.Name] = true
	}
	if !names["mcp_install"] {
		t.Fatalf("ListTools missing mcp_install")
	}
}

// --- mcp_server_add tests ---

type savingStubPlugin struct {
	*stubPluginStore
	saved []*domain.Plugin
}

func newSavingStub(plugins []*domain.Plugin) *savingStubPlugin {
	return &savingStubPlugin{stubPluginStore: &stubPluginStore{plugins: plugins}}
}

func (s *savingStubPlugin) Save(p *domain.Plugin) error {
	s.saved = append(s.saved, p)
	return nil
}

func TestMcpServerAdd(t *testing.T) {
	store := newSavingStub(nil)
	tb := &Toolbox{Plugins: store, MCP: &stubDropper{droppedServers: map[string]bool{}}}
	out, err := tb.Execute(context.Background(), "mcp_server_add", []byte(`{"name":"GitHub MCP","command":"npx","args":["-y","@modelcontextprotocol/server-github"],"env":{"TOKEN":"x"}}`))
	if err != nil {
		t.Fatalf("mcp_server_add: %v", err)
	}
	if !strings.Contains(out, "status: added") {
		t.Fatalf("unexpected output %q", out)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected one saved plugin, got %d", len(store.saved))
	}
	got := store.saved[0]
	if got.Manifest.Name != "GitHub MCP" || got.Manifest.MCP.Command != "npx" {
		t.Fatalf("unexpected manifest: %+v", got.Manifest)
	}
	if len(got.Manifest.MCP.Args) != 2 || got.Manifest.MCP.Args[1] != "@modelcontextprotocol/server-github" {
		t.Fatalf("unexpected args: %v", got.Manifest.MCP.Args)
	}
	if got.Manifest.MCP.Env["TOKEN"] != "x" {
		t.Fatalf("env not saved: %v", got.Manifest.MCP.Env)
	}
	if got.Manifest.ID == "" || !domain.ValidatePluginID(got.Manifest.ID) {
		t.Fatalf("invalid generated id %q", got.Manifest.ID)
	}
}

func TestMcpServerAddValidation(t *testing.T) {
	tb := &Toolbox{Plugins: newSavingStub(nil), MCP: &stubDropper{droppedServers: map[string]bool{}}}
	_, err := tb.Execute(context.Background(), "mcp_server_add", []byte(`{"command":"npx"}`))
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("expected name-required, got %v", err)
	}
	_, err = tb.Execute(context.Background(), "mcp_server_add", []byte(`{"name":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected command-required, got %v", err)
	}
}

func TestMcpServerAddHTTPTransport(t *testing.T) {
	store := newSavingStub(nil)
	tb := &Toolbox{Plugins: store, MCP: &stubDropper{droppedServers: map[string]bool{}}}
	out, err := tb.Execute(context.Background(), "mcp_server_add", []byte(`{"name":"Remote","transport":"http","url":"https://mcp.example.com/mcp","headers":{"Authorization":"Bearer tok"}}`))
	if err != nil {
		t.Fatalf("mcp_server_add: %v", err)
	}
	if !strings.Contains(out, "status: added") {
		t.Fatalf("unexpected output %q", out)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected 1 saved plugin, got %d", len(store.saved))
	}
	got := store.saved[0]
	if got.Manifest.MCP.Transport != domain.PluginTransportHTTP {
		t.Fatalf("transport = %q, want http", got.Manifest.MCP.Transport)
	}
	if got.Manifest.MCP.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("url not saved: %q", got.Manifest.MCP.URL)
	}
	if got.Manifest.MCP.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("headers not saved: %v", got.Manifest.MCP.Headers)
	}
	if got.Manifest.MCP.Command != "" {
		t.Fatalf("command must be empty for http transport, got %q", got.Manifest.MCP.Command)
	}
}

func TestMcpServerAddRemoteRequiresURL(t *testing.T) {
	for _, transport := range []string{"sse", "http"} {
		tb := &Toolbox{Plugins: newSavingStub(nil), MCP: &stubDropper{droppedServers: map[string]bool{}}}
		payload := fmt.Sprintf(`{"name":"remote","transport":%q}`, transport)
		_, err := tb.Execute(context.Background(), "mcp_server_add", []byte(payload))
		if err == nil || !strings.Contains(err.Error(), "url is required") {
			t.Fatalf("transport %s without url: got %v, want url-required", transport, err)
		}
	}
}

func TestListToolsIncludesMcpServerAdd(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	names := map[string]bool{}
	for _, ti := range tb.ListTools() {
		names[ti.Name] = true
	}
	if !names["mcp_server_add"] {
		t.Fatalf("ListTools missing mcp_server_add")
	}
}

// --- enriched skill read (file + listing) ---

type skillFileStoreStub struct {
	*stubSkillStore
	files    map[string][]domain.SkillFileEntry
	readErr  error
	read     func(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error)
	writeErr error
	written  map[string]string // key "id|path" → content
	write    func(id, ownedBy, path, content string) error
}

func (s *skillFileStoreStub) ReadFile(id, ownedBy, path string, offset, maxChars int) (*domain.SkillFile, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	if s.read != nil {
		return s.read(id, ownedBy, path, offset, maxChars)
	}
	return nil, fmt.Errorf("not implemented")
}
func (s *skillFileStoreStub) Files(id, ownedBy string) ([]domain.SkillFileEntry, error) {
	return s.files[id], nil
}
func (s *skillFileStoreStub) WriteFile(id, ownedBy, path, content string) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	if s.write != nil {
		return s.write(id, "", path, content)
	}
	if s.written == nil {
		s.written = map[string]string{}
	}
	s.written[id+"|"+path] = content
	return nil
}

func TestSkillSaveWithPath_writesSupportFile(t *testing.T) {
	store := &skillFileStoreStub{stubSkillStore: &stubSkillStore{skills: []*domain.Skill{{ID: "my-skill", Name: "my-skill"}}}}
	tb := &Toolbox{Skills: store, Plugins: &stubPluginStore{}, MCP: &stubMCP{}}
	out, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"save","name":"my-skill","path":"references/errors.md","content":"# Error recipes\n"}`))
	if err != nil {
		t.Fatalf("skill save with path: %v", err)
	}
	if !strings.Contains(out, "saved") {
		t.Fatalf("unexpected output %q", out)
	}
	got, ok := store.written["my-skill|references/errors.md"]
	if !ok {
		t.Fatal("WriteFile was not called with the expected id+path")
	}
	if got != "# Error recipes\n" {
		t.Fatalf("written content = %q, want %q", got, "# Error recipes\n")
	}
}

func TestSkillSaveWithPath_emptyPathUsesSaveNotWriteFile(t *testing.T) {
	store := &skillFileStoreStub{stubSkillStore: &stubSkillStore{skills: []*domain.Skill{{ID: "my-skill", Name: "my-skill"}}}}
	tb := &Toolbox{Skills: store, Plugins: &stubPluginStore{}, MCP: &stubMCP{}}
	_, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"save","name":"my-skill","path":"","content":"# Updated\n"}`))
	if err != nil {
		t.Fatalf("skill save empty path: %v", err)
	}
	// Empty path must go through Save (metadata + SKILL.md), not WriteFile.
	if len(store.written) != 0 {
		t.Fatalf("WriteFile should not be called for empty path, got %v", store.written)
	}
}

// skillSaveRecordingStore verifies the contract between the toolbox and the
// filesystem skill store: for a new skill, Save receives an empty ID so the
// store can derive the lowercase folder ID from the validated name.
type skillSaveRecordingStore struct {
	*stubSkillStore
	saved *domain.Skill
}

func (s *skillSaveRecordingStore) Save(skill *domain.Skill) error {
	s.saved = skill
	return nil
}

func TestSkillSaveNewSkillLeavesIDForStoreToDerive(t *testing.T) {
	store := &skillSaveRecordingStore{stubSkillStore: &stubSkillStore{}}
	tb := &Toolbox{Skills: store, Plugins: &stubPluginStore{}, MCP: &stubMCP{}}
	_, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"save","name":"new-skill","description":"Reusable workflow","content":"# New skill\n"}`))
	if err != nil {
		t.Fatalf("skill save new skill: %v", err)
	}
	if store.saved == nil {
		t.Fatal("Save was not called")
	}
	if store.saved.ID != "" {
		t.Fatalf("new skill ID = %q, want empty so the store derives it from name", store.saved.ID)
	}
}

func TestSkillSaveWithPath_nonexistentSkill_rejected(t *testing.T) {
	store := &skillFileStoreStub{stubSkillStore: &stubSkillStore{}}
	store.writeErr = fmt.Errorf("skill %q not found", "no-such")
	tb := &Toolbox{Skills: store, Plugins: &stubPluginStore{}, MCP: &stubMCP{}}
	_, err := tb.Execute(context.Background(), "skill", []byte(`{"op":"save","name":"no-such","path":"references/x.md","content":"x"}`))
	if err == nil {
		t.Fatal("expected error for nonexistent skill, got nil")
	}
}

type stubAcp struct {
	agents []*domain.AcpAgent
}

func (s *stubAcp) SpawnSubagents(ctx context.Context, argsJSON []byte) (string, error) {
	return `{"runs":[]}`, nil
}
func (s *stubAcp) SteerAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	return "{}", nil
}
func (s *stubAcp) StopAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	return "{}", nil
}
func (s *stubAcp) WaitAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	return "{}", nil
}
func (s *stubAcp) EnabledAcpAgents() []*domain.AcpAgent { return s.agents }

func TestListToolsOmitsSubagentWhenNoAgents(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	for _, ti := range tb.ListTools() {
		if strings.HasPrefix(ti.Name, "subagent") {
			t.Fatalf("subagent tools must stay hidden without ACP agents, found %q", ti.Name)
		}
	}
}

func TestListToolsIncludesSubagentWhenAgentsEnabled(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.Acp = &stubAcp{agents: []*domain.AcpAgent{{ID: "acp_1", Name: "Cursor", Enabled: true}}}
	names := map[string]bool{}
	for _, ti := range tb.ListTools() {
		names[ti.Name] = true
	}
	for _, want := range []string{"subagent", "subagent_steer", "subagent_stop", "subagent_wait"} {
		if !names[want] {
			t.Fatalf("ListTools missing %q", want)
		}
	}
}

func TestPipelineAgentRunnerHidesEnabledAcpTools(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	tb.Acp = &stubAcp{agents: []*domain.AcpAgent{{ID: "acp_1", Name: "Cursor", Enabled: true}}}
	runner := application.NewPipelineAgentRunner(tb, nil)
	for _, ti := range runner.Tools.ListTools() {
		if strings.HasPrefix(ti.Name, "subagent") {
			t.Fatalf("pipeline agent listed %q", ti.Name)
		}
	}
	if _, err := runner.Tools.Execute(context.Background(), "subagent", []byte(`{"prompt":"x"}`)); err == nil {
		t.Fatal("pipeline agent must not execute subagent")
	}
}

func TestSleepTool(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	start := time.Now()
	out, err := tb.Execute(context.Background(), "sleep", []byte(`{"seconds":1}`))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("sleep failed: %v", err)
	}
	if !strings.Contains(out, "status: slept") {
		t.Fatalf("unexpected output: %s", out)
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("sleep returned too fast: %v", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("sleep took too long: %v", elapsed)
	}
}

func TestSleepToolRejectsZeroAndNegative(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	if _, err := tb.Execute(context.Background(), "sleep", []byte(`{"seconds":0}`)); err == nil {
		t.Fatal("seconds=0 should error")
	}
	if _, err := tb.Execute(context.Background(), "sleep", []byte(`{"seconds":-5}`)); err == nil {
		t.Fatal("seconds=-5 should error")
	}
}

func TestSleepToolDescriptionMentionsCap(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	for _, ti := range tb.ListTools() {
		if ti.Name == "sleep" {
			if !strings.Contains(ti.Description, "300") {
				t.Fatalf("sleep description should mention 300s cap: %s", ti.Description)
			}
			return
		}
	}
	t.Fatal("sleep tool not found")
}

func TestDocsReadAcceptsCanonicalIDAndMarkdownFilename(t *testing.T) {
	source, err := docsinfra.New("")
	if err != nil {
		t.Fatal(err)
	}
	tb := &Toolbox{Docs: source}
	for _, id := range []string{"automation", "automation.md"} {
		out, err := tb.Execute(context.Background(), "docs", []byte(fmt.Sprintf(`{"op":"read","id":%q}`, id)))
		if err != nil {
			t.Fatalf("docs read id %q: %v", id, err)
		}
		if !strings.Contains(out, "Automation and pipelines") {
			t.Fatalf("docs read id %q returned unexpected content: %s", id, out)
		}
		if !strings.Contains(out, "title: Automation") {
			t.Fatalf("docs read id %q did not return canonical metadata: %s", id, out)
		}
	}
}

func TestAgentToolsDocMatchesBuiltInRoster(t *testing.T) {
	content, err := resources.DocsFS.ReadFile("agent/docs/tools.md")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile("`([a-z][a-z0-9_]*)`")
	documented := map[string]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		for _, match := range pattern.FindAllStringSubmatch(cells[1], -1) {
			documented[match[1]] = true
		}
	}
	tb := testToolbox(nil, nil, &stubMCP{})
	// The docs document the ADVERTISED roster — non-family built-ins plus
	// the dispatcher family roots (docs/design/tool-dispatchers.md). There
	// are no per-verb names anywhere on the roster.
	actual := map[string]bool{}
	for _, tool := range append(tb.ListTools(), application.DispatcherToolInfos()...) {
		actual[tool.Name] = true
		if !documented[tool.Name] {
			t.Errorf("agent tools documentation missing %q", tool.Name)
		}
	}
	conditional := map[string]bool{
		"web_answer": true, "subagent": true, "subagent_steer": true,
		"subagent_stop": true, "subagent_wait": true,
		"generate_media": true,
	}
	for name := range documented {
		if !actual[name] && !conditional[name] {
			t.Errorf("agent tools documentation contains stale built-in %q", name)
		}
	}
}

func TestMcpCreatorSkillUsesRuntimeToolContract(t *testing.T) {
	foundRuntimeName := false
	err := fs.WalkDir(resources.BuiltinSkillsFS, "agent/skills/mcp-creator", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		content, err := resources.BuiltinSkillsFS.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(content)
		for _, stale := range []string{"mcp_context", "mcp_<pluginId>_<tool>", "capabilities.prompts", "prompts.js", "mcp__<server>__<tool>"} {
			if strings.Contains(text, stale) {
				t.Errorf("%s contains stale tool contract %q", path, stale)
			}
		}
		foundRuntimeName = foundRuntimeName || strings.Contains(text, "<server>:<tool>")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !foundRuntimeName {
		t.Error("mcp-creator missing runtime MCP ref naming contract")
	}
}

func TestTodoToolDescriptionMatchesSingleHydrationCheckpoint(t *testing.T) {
	for _, tool := range testToolbox(nil, nil, &stubMCP{}).ListTools() {
		if tool.Name != "todo" {
			continue
		}
		if strings.Contains(tool.Description, "every turn") || !strings.Contains(tool.Description, "reused until compaction") {
			t.Fatalf("todo description contradicts hydration lifecycle: %s", tool.Description)
		}
		return
	}
	t.Fatal("todo tool not found")
}

// TestTodoToolResultDoesNotEchoItemsOrBrief verifies the tool result is a
// compact acknowledgment (summary counts only), not a full echo of the
// items and brief the agent just sent. Echoing wastes tokens — the agent
// already knows what it set, and the UI gets the full list via the
// agent.todo.updated event.
func TestTodoToolResultDoesNotEchoItemsOrBrief(t *testing.T) {
	dir := t.TempDir()
	store := jsonstore.NewTodoStore(filepath.Join(dir, "todos.json"), dir, nil)
	tb := &Toolbox{Todos: store}
	ctx := application.WithConversationID(context.Background(), "conv_echo")
	args := `{"items":[{"id":"a","content":"do thing one","status":"pending"},{"id":"b","content":"do thing two with a long description that should not be echoed back","status":"in_progress"}],"brief":"## Objective\nBuild the feature\n## Done when\nTests pass"}`
	out, err := tb.Execute(ctx, "todo", []byte(args))
	if err != nil {
		t.Fatalf("todo execute: %v", err)
	}
	// Summary counts must be present.
	for _, want := range []string{"ok: true", "total: 2", "pending: 1", "in_progress: 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("result missing %q\ngot:\n%s", want, out)
		}
	}
	// Item contents must NOT be echoed back.
	for _, banned := range []string{"do thing one", "do thing two with a long description"} {
		if strings.Contains(out, banned) {
			t.Errorf("result must not echo item content %q\ngot:\n%s", banned, out)
		}
	}
	// Brief must NOT be echoed back.
	if strings.Contains(out, "## Objective") || strings.Contains(out, "Build the feature") {
		t.Errorf("result must not echo brief\ngot:\n%s", out)
	}
}

func TestMcpRegisterRejectsSourcesInsideInstalledRoot(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			root := t.TempDir()
			store, err := pluginfs.New(root)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(root, "same.plugin")
			source := destination
			if nested {
				source = filepath.Join(destination, "staging")
			}
			if err := os.MkdirAll(source, 0o755); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(source, "manifest.json")
			manifest := `{"id":"same.plugin","name":"Same Plugin","version":"1.0.0","icon":"S","mcp":{"transport":"stdio","command":"node"}}`
			if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			tb := &Toolbox{Plugins: store}
			_, err = tb.Execute(context.Background(), "mcp_register", []byte(fmt.Sprintf(`{"source":%q}`, source)))
			if err == nil || !strings.Contains(err.Error(), "source directory must be outside the installed plugins root") {
				t.Fatalf("expected installed-root rejection, got %v", err)
			}
			if _, err := os.Stat(manifestPath); err != nil {
				t.Fatalf("installed-root rejection must preserve source files: %v", err)
			}
		})
	}
}

func TestSleepToolRespectsContextCancellation(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := tb.Execute(ctx, "sleep", []byte(`{"seconds":5}`))
	if err == nil {
		t.Fatal("should error when context cancelled")
	}
}

func TestListToolsIncludesSleepAndWaitAndCiWait(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	names := map[string]bool{}
	for _, ti := range tb.ListTools() {
		names[ti.Name] = true
	}
	for _, want := range []string{"sleep", "ci_wait", "ci_run"} {
		if !names[want] {
			t.Fatalf("ListTools missing %q", want)
		}
	}
}

type memToolboxSettings struct {
	s domain.Settings
}

func (m *memToolboxSettings) Get() domain.Settings        { return m.s }
func (m *memToolboxSettings) Set(s domain.Settings) error { m.s = s; return nil }

func TestListToolsGenerateImageIsConditional(t *testing.T) {
	tb := testToolbox(nil, nil, &stubMCP{})
	for _, ti := range tb.ListTools() {
		if ti.Name == "generate_media" {
			t.Fatal("generate_media must be omitted when no media backend is configured")
		}
	}
	tb.Settings = &memToolboxSettings{s: domain.Settings{ImageProviderID: "or", ImageModelID: "openai/gpt-image-2"}}
	found := false
	for _, ti := range tb.ListTools() {
		if ti.Name == "generate_media" {
			found = true
			if !strings.Contains(ti.Description, "do not re-render") {
				t.Fatalf("description missing UI hint: %s", ti.Description)
			}
		}
	}
	if !found {
		t.Fatal("generate_media missing when image provider is configured")
	}
}
