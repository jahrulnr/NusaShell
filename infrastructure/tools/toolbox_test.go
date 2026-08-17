package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
func (s *stubSkillStore) ReadFile(id, path string, offset, maxChars int) (*domain.SkillFile, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSkillStore) Files(id string) ([]domain.SkillFileEntry, error) {
	return nil, fmt.Errorf("not implemented")
}

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
	running      map[string]bool                   // serverID -> running
	lastServerID string
	lastTool     string
}

func (m *stubMCP) Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	if tools, ok := m.tools[p.Manifest.MCPServerID()]; ok {
		return tools, nil
	}
	return nil, fmt.Errorf("not connected")
}
func (m *stubMCP) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) {
	tools, ok := m.tools[serverID]
	return tools, ok
}
func (m *stubMCP) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	m.lastServerID = serverID
	m.lastTool = toolName
	return "ok", nil
}

func testToolbox(skills []*domain.Skill, plugins []*domain.Plugin, mcp *stubMCP) *Toolbox {
	return &Toolbox{
		Skills:  &stubSkillStore{skills: skills},
		Plugins: &stubPluginStore{plugins: plugins},
		MCP:     mcp,
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

type stubSkillStoreNoFiles struct{ skills []*domain.Skill }

func (s *stubSkillStoreNoFiles) List() []*domain.Skill { return s.skills }
func (s *stubSkillStoreNoFiles) Get(id string) (*domain.Skill, error) {
	for _, sk := range s.skills {
		if sk.ID == id {
			return sk, nil
		}
	}
	return nil, fmt.Errorf("not found")
}
func (s *stubSkillStoreNoFiles) Save(sk *domain.Skill) error { return nil }
func (s *stubSkillStoreNoFiles) Delete(id string) error      { return nil }
func (s *stubSkillStoreNoFiles) ReadFile(id, path string, offset, maxChars int) (*domain.SkillFile, error) {
	return nil, errReadFileUnsupported
}
func (s *stubSkillStoreNoFiles) Files(id string) ([]domain.SkillFileEntry, error) {
	return nil, errReadFileUnsupported
}

var errReadFileUnsupported = fmt.Errorf("file reads unsupported by this store")

func TestSkillRead(t *testing.T) {
	tb := &Toolbox{
		Skills:  &stubSkillStoreNoFiles{skills: []*domain.Skill{{ID: "s1", Name: "git-helper", Description: "Git help", Content: "# Git Helper\nUse git status."}}},
		Plugins: &stubPluginStore{},
		MCP:     &stubMCP{},
	}
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
	var res struct {
		Count   int `json:"count"`
		Plugins []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
			Tools   int    `json:"tools"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if res.Count != 2 {
		t.Errorf("expected 2 plugins, got %d", res.Count)
	}
	// github should be running with 1 tool
	var github *struct {
		Name    string `json:"name"`
		Running bool   `json:"running"`
		Tools   int    `json:"tools"`
	}
	for i := range res.Plugins {
		if res.Plugins[i].Name == "github" {
			github = &res.Plugins[i]
		}
	}
	if github == nil || !github.Running || github.Tools != 1 {
		t.Errorf("expected github running with 1 tool, got: %+v", github)
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
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
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
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {
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
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{
			tools: map[string][]contracts.MCPToolDTO{
				"plugin:srv1": {{Name: "create_issue"}},
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
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "srv1", Name: "github", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		&stubMCP{tools: map[string][]contracts.MCPToolDTO{}}, // not running
	)
	_, err := tb.Execute(context.Background(), "tool_schema", []byte(`{"server":"github","tool":"create_issue"}`))
	if err == nil {
		t.Error("expected error for non-running server")
	}
}

func TestMCPToolNameMatchesLongestServerPrefix(t *testing.T) {
	mcp := &stubMCP{}
	tb := testToolbox(nil,
		[]*domain.Plugin{
			{Manifest: domain.PluginManifest{ID: "short", Name: "foo", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
			{Manifest: domain.PluginManifest{ID: "long", Name: "foo__bar", MCP: domain.PluginMCPConfig{Transport: domain.PluginTransportStdio, Command: "npx"}}},
		},
		mcp,
	)
	if _, err := tb.Execute(context.Background(), "mcp__foo__bar__read", nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if mcp.lastServerID != "plugin:long" || mcp.lastTool != "read" {
		t.Fatalf("routed to server %q tool %q, want plugin:long/read", mcp.lastServerID, mcp.lastTool)
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
	absPluginSource, err := filepath.Abs("/tmp/fake-plugin")
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	out, err := tb.Execute(context.Background(), "mcp_register", []byte(`{"source":"/tmp/fake-plugin"}`))
	if err != nil {
		t.Fatalf("mcp_register: %v", err)
	}
	if !strings.Contains(out, "registered plugin") {
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
	mcp := &stubMCP{tools: map[string][]contracts.MCPToolDTO{
		serverID: {{Name: "demo_ping", Description: "ping"}},
	}}
	tb := &Toolbox{Plugins: &stubPluginStore{plugins: []*domain.Plugin{plugin}}, MCP: mcp}
	out, err := tb.Execute(context.Background(), "mcp_enable", []byte(`{"id":"nusashell.demo"}`))
	if err != nil {
		t.Fatalf("mcp_enable: %v", err)
	}
	if !strings.Contains(out, "1 tool") {
		t.Fatalf("expected 1 tool in output, got %q", out)
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
	if !strings.Contains(out, "installed plugin") {
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
	if !strings.Contains(out, "added MCP server") {
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

// --- enriched skill_read (file + listing) ---

type skillFileStoreStub struct {
	*stubSkillStore
	files   map[string][]domain.SkillFileEntry
	readErr error
	read    func(id, path string, offset, maxChars int) (*domain.SkillFile, error)
}

func (s *skillFileStoreStub) ReadFile(id, path string, offset, maxChars int) (*domain.SkillFile, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	if s.read != nil {
		return s.read(id, path, offset, maxChars)
	}
	return nil, fmt.Errorf("not implemented")
}
func (s *skillFileStoreStub) Files(id string) ([]domain.SkillFileEntry, error) {
	return s.files[id], nil
}

func TestSkillReadFile(t *testing.T) {
	store := &skillFileStoreStub{stubSkillStore: &stubSkillStore{skills: []*domain.Skill{{ID: "mcp-creator", Name: "mcp-creator"}}}}
	store.read = func(id, path string, offset, maxChars int) (*domain.SkillFile, error) {
		if path == "" {
			path = "SKILL.md"
		}
		return &domain.SkillFile{SkillID: id, Path: path, Content: "hello from " + path, Editable: true, SizeBytes: 20}, nil
	}
	tb := &Toolbox{Skills: store, Plugins: &stubPluginStore{}, MCP: &stubMCP{}}
	out, err := tb.Execute(context.Background(), "skill_read", []byte(`{"name":"mcp-creator","path":"references/prerequisites.md"}`))
	if err != nil {
		t.Fatalf("skill_read file: %v", err)
	}
	if !strings.Contains(out, "references/prerequisites.md") {
		t.Fatalf("unexpected content %q", out)
	}
}

func TestSkillFiles(t *testing.T) {
	store := &skillFileStoreStub{stubSkillStore: &stubSkillStore{skills: []*domain.Skill{{ID: "mcp-creator", Name: "mcp-creator"}}}}
	store.files = map[string][]domain.SkillFileEntry{
		"mcp-creator": {
			{Path: "SKILL.md", Type: "file", SizeBytes: 10, Editable: true},
			{Path: "references", Type: "directory"},
			{Path: "references/prerequisites.md", Type: "file", SizeBytes: 5, Editable: true},
		},
	}
	tb := &Toolbox{Skills: store, Plugins: &stubPluginStore{}, MCP: &stubMCP{}}
	out, err := tb.Execute(context.Background(), "skill_files", []byte(`{"name":"mcp-creator"}`))
	if err != nil {
		t.Fatalf("skill_files: %v", err)
	}
	if !strings.Contains(out, "references/prerequisites.md") || !strings.Contains(out, "SKILL.md") {
		t.Fatalf("unexpected listing %q", out)
	}
}

func TestSkillFilesUnsupported(t *testing.T) {
	tb := &Toolbox{Skills: &stubSkillStoreNoFiles{}, Plugins: &stubPluginStore{}, MCP: &stubMCP{}}
	_, err := tb.Execute(context.Background(), "skill_files", []byte(`{"name":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "does not support file listing") {
		t.Fatalf("expected unsupported error, got %v", err)
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
