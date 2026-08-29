// Package mcpclient manages stdio MCP server connections (mcp-go) and
// exposes their tools to the agent toolbox.
package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"nusashell/contracts"
	"nusashell/domain"
)

// Manager owns one stdio connection per server id, created lazily.
type Manager struct {
	mu    sync.Mutex
	conns map[string]*conn
}

type conn struct {
	plugin *domain.Plugin
	client *client.Client
	tools  []contracts.MCPToolDTO
}

func NewManager() *Manager {
	return &Manager{conns: map[string]*conn{}}
}

// Connect returns the plugin's MCP tools, connecting on first use. The
// returned connection is cached until Drop or the process exits.
func (m *Manager) Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conns[p.Manifest.MCPServerID()]; ok {
		return c.tools, nil
	}
	c, err := dial(ctx, p)
	if err != nil {
		return nil, err
	}
	m.conns[p.Manifest.MCPServerID()] = c
	return c.tools, nil
}

// ToolsFor returns cached tools without connecting.
func (m *Manager) ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[serverID]
	if !ok {
		return nil, false
	}
	return c.tools, true
}

// Drop closes and forgets a connection (used after config changes or delete).
func (m *Manager) Drop(serverID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.conns[serverID]; ok {
		_ = c.client.Close()
		delete(m.conns, serverID)
	}
}

// CallTool executes a tool on a connected server and returns the
// concatenated text content. Use CallToolRaw when the caller needs the
// full MCP result (structuredContent, isError, content parts).
func (m *Manager) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error) {
	result, err := m.CallToolRaw(ctx, serverID, toolName, args)
	if err != nil {
		return "", err
	}
	return contentText(result), nil
}

// CallToolRaw executes a tool on a connected server and returns the full
// MCP CallToolResult, including StructuredContent and IsError. The caller
// is responsible for inspecting IsError and extracting content.
func (m *Manager) CallToolRaw(ctx context.Context, serverID, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
	m.mu.Lock()
	c, ok := m.conns[serverID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("mcp server %q is not connected", serverID)
	}
	result, err := c.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: toolName, Arguments: args},
	})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return result, fmt.Errorf("tool %s failed: %s", toolName, contentText(result))
	}
	return result, nil
}

func contentText(result *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range result.Content {
		if t, ok := c.(mcp.TextContent); ok {
			sb.WriteString(t.Text)
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func dial(ctx context.Context, p *domain.Plugin) (*conn, error) {
	cfg := p.Manifest.MCP
	var mcpClient *client.Client
	switch cfg.Transport {
	case domain.PluginTransportSSE:
		c, err := client.NewSSEMCPClient(cfg.URL, transport.WithHeaders(cfg.Headers))
		if err != nil {
			return nil, fmt.Errorf("connect %s: %w", cfg.URL, err)
		}
		mcpClient = c
	case domain.PluginTransportHTTP:
		c, err := client.NewStreamableHttpClient(cfg.URL, transport.WithHTTPHeaders(cfg.Headers))
		if err != nil {
			return nil, fmt.Errorf("connect %s: %w", cfg.URL, err)
		}
		mcpClient = c
	case domain.PluginTransportStdio:
		env := append([]string{}, os.Environ()...)
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		var opts []transport.StdioOption
		if p.InstallPath != "" {
			opts = append(opts, transport.WithCommandFunc(func(_ context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
				cmd := exec.Command(command, args...)
				cmd.Env = env
				cmd.Dir = p.InstallPath
				return cmd, nil
			}))
		}
		c, err := client.NewStdioMCPClientWithOptions(cfg.Command, env, cfg.Args, opts...)
		if err != nil {
			return nil, fmt.Errorf("start %s: %w", cfg.Command, err)
		}
		mcpClient = c
	default:
		return nil, fmt.Errorf("unsupported mcp transport %q", cfg.Transport)
	}
	if cfg.Transport != domain.PluginTransportStdio {
		if err := mcpClient.Start(ctx); err != nil {
			_ = mcpClient.Close()
			return nil, fmt.Errorf("start %s: %w", cfg.URL, err)
		}
	}
	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err := mcpClient.Initialize(initCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "nusashell",
				Version: "0.1.0",
			},
		},
	})
	if err != nil {
		_ = mcpClient.Close()
		return nil, fmt.Errorf("initialize %s: %w", p.Manifest.Name, err)
	}
	toolsResult, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		_ = mcpClient.Close()
		return nil, fmt.Errorf("list tools %s: %w", p.Manifest.Name, err)
	}
	tools := make([]contracts.MCPToolDTO, 0, len(toolsResult.Tools))
	for _, t := range toolsResult.Tools {
		schema, _ := json.Marshal(t.InputSchema)
		tools = append(tools, contracts.MCPToolDTO{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return &conn{plugin: p, client: mcpClient, tools: tools}, nil
}
