package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/contracts"
	"nusashell/domain"
)

type stubFilterMCP struct {
	tools map[string][]contracts.MCPToolDTO
	calls []string
}

func (s *stubFilterMCP) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) {
	t, ok := s.tools[serverID]
	return t, ok
}

func (s *stubFilterMCP) Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	return s.tools[p.Manifest.MCPServerID()], nil
}

func (s *stubFilterMCP) Drop(serverID string) {}

func (s *stubFilterMCP) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	s.calls = append(s.calls, toolName)
	return "ok:" + toolName, nil
}

func TestFilteringMCPHidesInternalTools(t *testing.T) {
	inner := &stubFilterMCP{tools: map[string][]contracts.MCPToolDTO{
		"plugin:tg": {
			{Name: "send_message"},
			{Name: "internal_send_progress"},
			{Name: "admin.send_progress"},
		},
	}}
	view := NewFilteringMCP(inner)
	got, ok := view.ToolsFor("plugin:tg")
	if !ok {
		t.Fatal("expected tools")
	}
	if len(got) != 1 || got[0].Name != "send_message" {
		t.Fatalf("got %+v, want only send_message", got)
	}
	if _, err := view.CallTool(context.Background(), "plugin:tg", "internal_send_progress", nil); err == nil || !strings.Contains(err.Error(), "host-internal") {
		t.Fatalf("expected host-internal deny, got %v", err)
	}
	if len(inner.calls) != 0 {
		t.Fatalf("inner must not be called for internal tools, calls=%v", inner.calls)
	}
	out, err := view.CallTool(context.Background(), "plugin:tg", "send_message", nil)
	if err != nil || out != "ok:send_message" {
		t.Fatalf("public tool: out=%q err=%v", out, err)
	}
}
