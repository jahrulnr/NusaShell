package application

import (
	"context"
	"fmt"

	"nusashell/application/tools"
	"nusashell/contracts"
	"nusashell/domain"
)

// FilteringMCP wraps an MCP surface so host-internal tools (internal_*,
// admin.*) disappear from ToolsFor/Connect listings. When FilterCalls is
// true, CallTool also rejects those names — use that for the agent toolbox.
// Host automation (notify sink, capability Caller) keeps the bare manager
// so it can still invoke internal tools.
type FilteringMCP struct {
	Inner interface {
		Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
		ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
		Drop(serverID string)
		CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
	}
	FilterCalls bool
}

// NewFilteringMCP returns an agent-facing MCP view (list filtered, calls blocked).
func NewFilteringMCP(inner interface {
	Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
	ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
	Drop(serverID string)
	CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
}) *FilteringMCP {
	return &FilteringMCP{Inner: inner, FilterCalls: true}
}

func (f *FilteringMCP) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) {
	if f == nil || f.Inner == nil {
		return nil, false
	}
	all, ok := f.Inner.ToolsFor(serverID)
	if !ok {
		return nil, false
	}
	return filterMCPToolDTOs(all), true
}

func (f *FilteringMCP) Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	if f == nil || f.Inner == nil {
		return nil, fmt.Errorf("mcp is not configured")
	}
	all, err := f.Inner.Connect(ctx, p)
	if err != nil {
		return nil, err
	}
	return filterMCPToolDTOs(all), nil
}

func (f *FilteringMCP) Drop(serverID string) {
	if f == nil || f.Inner == nil {
		return
	}
	f.Inner.Drop(serverID)
}

func (f *FilteringMCP) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	if f == nil || f.Inner == nil {
		return "", fmt.Errorf("mcp is not configured")
	}
	if f.FilterCalls && tools.IsInternalToolName(toolName) {
		return "", fmt.Errorf("tool %q is host-internal and not available to agents", toolName)
	}
	return f.Inner.CallTool(ctx, serverID, toolName, args)
}

func filterMCPToolDTOs(in []contracts.MCPToolDTO) []contracts.MCPToolDTO {
	if len(in) == 0 {
		return in
	}
	out := make([]contracts.MCPToolDTO, 0, len(in))
	for _, t := range in {
		if tools.IsInternalToolName(t.Name) {
			continue
		}
		out = append(out, t)
	}
	return out
}
