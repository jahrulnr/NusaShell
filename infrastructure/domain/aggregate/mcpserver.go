package aggregate

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ErrMCPServerChanged rejects a write based on an out-of-date read.
var ErrMCPServerChanged = errors.New("MCP server was changed elsewhere")

// ReservedMCPNames are the built-in servers every room gets.
var ReservedMCPNames = []string{"conversation", "browser"}

var (
	mcpName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// MCPServer is a tool server the owner adds. Every agent session and the
// model runtime get the enabled ones. A stdio server is a command with
// arguments and environment; an http or sse server is a URL with headers.
// Environment and header values may hold credentials.
type MCPServer struct {
	id        string
	name      string
	transport enum.MCPTransport
	command   string
	args      []string
	env       map[string]string
	url       string
	headers   map[string]string
	enabled   bool
	createdAt time.Time
	updatedAt time.Time
}

// NewMCPServer builds an enabled server; an empty id mints one. Set the
// launch with SetStdio or SetRemote before saving.
func NewMCPServer(id, name string, now time.Time) (*MCPServer, error) {
	vo, err := valueobject.NewID("mcp", id)
	if err != nil {
		return nil, err
	}
	s := &MCPServer{id: vo.String(), transport: enum.MCPStdio, enabled: true, env: map[string]string{}, headers: map[string]string{}, createdAt: now, updatedAt: now}
	if err := s.Rename(name); err != nil {
		return nil, err
	}
	return s, nil
}

// Rename sets the name tools are grouped under: lowercase letters, digits,
// dashes, and underscores, not one of the built-in names.
func (s *MCPServer) Rename(name string) error {
	name = strings.TrimSpace(name)
	if !mcpName.MatchString(name) {
		return fmt.Errorf("MCP server name %q: use 1-32 lowercase letters, digits, - or _", name)
	}
	if slices.Contains(ReservedMCPNames, name) {
		return fmt.Errorf("MCP server name %q is taken by a built-in server", name)
	}
	s.name = name
	return nil
}

// SetStdio makes the server a command NusaShell or the agent starts.
func (s *MCPServer) SetStdio(command string, args []string, env map[string]string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("a stdio MCP server needs a command")
	}
	if err := checkNames("environment variable", env); err != nil {
		return err
	}
	s.transport, s.command, s.args, s.env = enum.MCPStdio, command, slices.Clone(args), maps.Clone(env)
	s.url, s.headers = "", map[string]string{}
	if s.env == nil {
		s.env = map[string]string{}
	}
	return nil
}

// SetRemote makes the server a URL reached over http or sse.
func (s *MCPServer) SetRemote(t enum.MCPTransport, rawURL string, headers map[string]string) error {
	if !t.Remote() {
		return fmt.Errorf("transport %q is not a URL transport", t)
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("an http or sse MCP server needs an http(s) URL")
	}
	for k := range headers {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k, " :\r\n") {
			return fmt.Errorf("header name %q is not valid", k)
		}
	}
	s.transport, s.url, s.headers = t, u.String(), maps.Clone(headers)
	s.command, s.args, s.env = "", nil, map[string]string{}
	if s.headers == nil {
		s.headers = map[string]string{}
	}
	return nil
}

func checkNames(what string, m map[string]string) error {
	for k := range m {
		if !envName.MatchString(k) {
			return fmt.Errorf("%s name %q is not valid", what, k)
		}
	}
	return nil
}

// SetEnabled turns the server on or off for new sessions.
func (s *MCPServer) SetEnabled(on bool) { s.enabled = on }

// Touch stamps an owner edit.
func (s *MCPServer) Touch(now time.Time) { s.updatedAt = now }

// Restore sets the stored timestamps.
func (s *MCPServer) Restore(createdAt, updatedAt time.Time) {
	s.createdAt, s.updatedAt = createdAt, updatedAt
}

func (s MCPServer) ID() string                   { return s.id }
func (s MCPServer) Name() string                 { return s.name }
func (s MCPServer) Transport() enum.MCPTransport { return s.transport }
func (s MCPServer) Command() string              { return s.command }
func (s MCPServer) Args() []string               { return slices.Clone(s.args) }
func (s MCPServer) Env() map[string]string       { return maps.Clone(s.env) }
func (s MCPServer) URL() string                  { return s.url }
func (s MCPServer) Headers() map[string]string   { return maps.Clone(s.headers) }
func (s MCPServer) Enabled() bool                { return s.enabled }
func (s MCPServer) CreatedAt() time.Time         { return s.createdAt }
func (s MCPServer) UpdatedAt() time.Time         { return s.updatedAt }
