package domain

import (
	"fmt"
	"strings"
	"time"
)

// ValidatePluginID reports whether id is a safe plugin identifier: non-empty,
// no path separators (POSIX or Windows), no parent-traversal segments, and
// not absolute. It guards pluginfs from path-traversal via a malicious
// manifest ID (e.g. id: ".." or "/etc/passwd").
func ValidatePluginID(id string) bool {
	t := strings.TrimSpace(id)
	if t == "" || t == "." || t == ".." {
		return false
	}
	// No path separators (POSIX or Windows).
	if strings.ContainsAny(t, `/\`) {
		return false
	}
	// Reject embedded parent-traversal segments (e.g. "foo/..") and
	// Windows drive-prefixed ids like "C:".
	if strings.Contains(t, "..") {
		return false
	}
	if len(t) >= 2 && t[1] == ':' {
		return false
	}
	return true
}

// PluginTransport is the MCP transport type for a plugin.
type PluginTransport string

const (
	PluginTransportStdio PluginTransport = "stdio"
	PluginTransportSSE   PluginTransport = "sse"
	PluginTransportHTTP  PluginTransport = "http"
)

// PluginContractConfig declares the plugin's agent-facing usage contract.
// Entry is a plugin-relative markdown file (e.g. CONTRACT.md) the agent is
// expected to read before working with the plugin's tools.
type PluginContractConfig struct {
	Entry string `json:"entry,omitempty"`
}

// Plugin window mode controls how the plugin UI window is displayed.
type PluginWindowMode string

const (
	PluginWindowFullscreen PluginWindowMode = "fullscreen"
	PluginWindowPanel      PluginWindowMode = "panel"
	PluginWindowWidget     PluginWindowMode = "widget"
)

// PluginWindowConfig describes the plugin UI window.
type PluginWindowConfig struct {
	Mode        PluginWindowMode `json:"mode,omitempty"`
	DefaultSize PluginWindowSize `json:"defaultSize,omitempty"`
	Resizable   bool             `json:"resizable,omitempty"`
}

// PluginWindowSize is the default window dimensions.
type PluginWindowSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// PluginUIConfig describes the plugin UI surface. Omit for a headless
// MCP-only plugin.
type PluginUIConfig struct {
	Entry  string             `json:"entry"`
	Window PluginWindowConfig `json:"window,omitempty"`
}

// PluginMCPConfig describes the plugin's MCP server connection.
type PluginMCPConfig struct {
	Transport        PluginTransport   `json:"transport"`
	Command          string            `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	URL              string            `json:"url,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	Autostart        bool              `json:"autostart,omitempty"`
	KeepAliveOnClose bool              `json:"keepAliveOnClose,omitempty"`
}

// PluginManifest is the parsed manifest.json from a plugin folder.
type PluginManifest struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Icon         string            `json:"icon"`
	Description  string            `json:"description,omitempty"`
	Category     string            `json:"category,omitempty"`
	AutoUpdate   bool              `json:"autoUpdate,omitempty"`
	UI           *PluginUIConfig   `json:"ui,omitempty"`
	MCP          PluginMCPConfig   `json:"mcp"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
	// Contract optionally declares an agent-facing usage contract file.
	Contract *PluginContractConfig `json:"contract,omitempty"`
}

// Plugin is an installed plugin: manifest + install path + runtime state.
type Plugin struct {
	Manifest    PluginManifest
	InstallPath string
	HasUI       bool
	InstalledAt time.Time
}

// Validate checks the manifest has the required fields and that the
// MCP transport is supported.
func (m *PluginManifest) Validate() error {
	if !ValidatePluginID(m.ID) {
		return fmt.Errorf("manifest: id %q is not a valid plugin identifier (no path separators, parent traversal, or absolute paths)", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("manifest: version is required")
	}
	if strings.TrimSpace(m.Icon) == "" {
		return fmt.Errorf("manifest: icon is required")
	}
	switch m.MCP.Transport {
	case PluginTransportStdio, PluginTransportSSE, PluginTransportHTTP:
	default:
		return fmt.Errorf("manifest: unsupported mcp.transport %q", m.MCP.Transport)
	}
	if m.MCP.Transport == PluginTransportStdio && strings.TrimSpace(m.MCP.Command) == "" {
		return fmt.Errorf("manifest: mcp.command is required for stdio transport")
	}
	if m.MCP.Transport != PluginTransportStdio && strings.TrimSpace(m.MCP.URL) == "" {
		return fmt.Errorf("manifest: mcp.url is required for %s transport", m.MCP.Transport)
	}
	if m.UI != nil {
		if strings.TrimSpace(m.UI.Entry) == "" {
			return fmt.Errorf("manifest: ui.entry is required when ui is present")
		}
		if strings.Contains(m.UI.Entry, "..") || hostRootedPath(m.UI.Entry) {
			return fmt.Errorf("manifest: ui.entry must be a relative path within the plugin directory")
		}
	}
	if m.Contract != nil {
		if e := strings.TrimSpace(m.Contract.Entry); e == "" {
			return fmt.Errorf("manifest: contract.entry is required when contract is present")
		} else if strings.Contains(e, "..") || hostRootedPath(e) {
			return fmt.Errorf("manifest: contract.entry must be a relative path within the plugin directory")
		}
	}
	return nil
}

// Plugin contract enforcement modes (Settings.PluginContractMode).
const (
	PluginContractOff     = "off"     // never gate mcp_call
	PluginContractHint    = "hint"    // advisory note on first call per conversation
	PluginContractRequire = "require" // reject mcp_call until contract_read ran
)

// MCPServerID returns the server ID used by the MCP manager for this
// plugin. It is the plugin ID prefixed with "plugin:".
func (m *PluginManifest) MCPServerID() string {
	return "plugin:" + m.ID
}

// ContractEntry returns the declared contract file path (plugin-relative),
// or "" when the plugin declares no contract.
func (m *PluginManifest) ContractEntry() string {
	if m.Contract == nil {
		return ""
	}
	return strings.TrimSpace(m.Contract.Entry)
}

// HasWindow returns true when the plugin declares a UI entry.
func (m *PluginManifest) HasWindow() bool {
	return m.UI != nil && m.UI.Entry != ""
}
