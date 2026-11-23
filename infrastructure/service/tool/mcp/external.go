package toolmcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
)

const connectWait = 30 * time.Second

// External is an MCP server the owner added.
type External struct {
	Name      string
	Transport enum.MCPTransport
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
}

// ExternalStatus is how one server is doing.
type ExternalStatus struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Tools     int    `json:"tools"`
	Error     string `json:"error,omitempty"`
}

// Registry holds the tools of the owner's MCP servers for the model
// runtime. Each server has one connection shared by every thread — an
// outside server knows nothing of threads — reopened when it drops. Load
// replaces the set when the owner changes it.
type Registry struct {
	outputs    drepo.ToolOutput
	log        *slog.Logger
	clientName string
	version    string

	mu      sync.RWMutex
	servers map[string]*external
	tools   map[string]dservice.Tool
}

type external struct {
	cfg    External
	mu     sync.Mutex // one connect at a time
	client *mcp.Client
	tools  int
	err    string
}

// NewRegistry returns an empty registry.
func NewRegistry(outputs drepo.ToolOutput, clientName, version string, log *slog.Logger) *Registry {
	if log == nil {
		log = slog.Default()
	}
	return &Registry{outputs: outputs, clientName: clientName, version: version, log: log.With("component", "mcp"),
		servers: map[string]*external{}, tools: map[string]dservice.Tool{}}
}

// Load connects to the servers and offers their tools, replacing the
// previous set. A server that fails to connect offers no tools and reports
// why in Status.
func (r *Registry) Load(ctx context.Context, list []External) {
	next := map[string]*external{}
	var wg sync.WaitGroup
	for _, cfg := range list {
		e := &external{cfg: cfg}
		next[cfg.Name] = e
		wg.Go(func() { ; _ = e.connect(ctx, r) })
	}
	wg.Wait()
	tools := map[string]dservice.Tool{}
	// Sorted server names: two servers whose names sanitize to the same
	// wire name must resolve the collision deterministically — map order
	// would let the plain name rebind to a different server's tool on
	// every reload.
	for _, server := range slices.Sorted(maps.Keys(next)) {
		e := next[server]
		if e.client == nil {
			continue
		}
		listCtx, cancel := context.WithTimeout(ctx, connectWait)
		listed, err := e.client.Tools(listCtx)
		cancel()
		if err != nil {
			e.err = err.Error()
			continue
		}
		e.tools = 0
		for _, t := range listed {
			name := toolName(e.cfg.Name, t.Name)
			if _, taken := tools[name]; taken {
				// Sanitize+truncate can map two declared names onto one
				// wire name — a deterministic suffix keeps both reachable
				// and stable across restarts instead of one silently
				// overwriting the other in the map.
				sum := sha256.Sum256([]byte(e.cfg.Name + "\x00" + t.Name))
				suffix := "_" + hex.EncodeToString(sum[:])[:6]
				if len(name)+len(suffix) > 64 {
					name = name[:64-len(suffix)]
				}
				name += suffix
			}
			if _, taken := tools[name]; taken {
				r.log.Warn("MCP tool name collision", "server", e.cfg.Name, "tool", t.Name, "name", name)
				continue
			}
			desc := strings.TrimSpace(t.Description)
			if desc == "" {
				desc = t.Name
			}
			sch, err := valueobject.NewToolSchema(name, "["+e.cfg.Name+"] "+desc, t.InputSchema, true, enum.ToolPermissionAgent)
			if err != nil {
				r.log.Warn("MCP tool skipped", "server", e.cfg.Name, "tool", t.Name, "error", err)
				continue
			}
			tools[name] = &externalTool{reg: r, server: e.cfg.Name, name: t.Name, schema: sch}
			e.tools++
		}
	}
	r.mu.Lock()
	old := r.servers
	r.servers, r.tools = next, tools
	r.mu.Unlock()
	for _, e := range old {
		e.close()
	}
}

// Tool finds an offered tool.
func (r *Registry) Tool(name string) (dservice.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Tools lists the offered tools.
func (r *Registry) Tools() []dservice.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]dservice.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

// Status reports each server, by name.
func (r *Registry) Status() []ExternalStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ExternalStatus, 0, len(r.servers))
	for _, e := range r.servers {
		e.mu.Lock()
		st := ExternalStatus{Name: e.cfg.Name, Tools: e.tools, Error: e.err}
		if e.client != nil {
			select {
			case <-e.client.Done():
			default:
				st.Connected = true
			}
		}
		e.mu.Unlock()
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b ExternalStatus) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Close drops every connection.
func (r *Registry) Close() {
	r.mu.Lock()
	old := r.servers
	r.servers, r.tools = map[string]*external{}, map[string]dservice.Tool{}
	r.mu.Unlock()
	for _, e := range old {
		e.close()
	}
}

func (e *external) connect(ctx context.Context, r *Registry) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client != nil {
		select {
		case <-e.client.Done():
		default:
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, connectWait)
	defer cancel()
	var conn interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
	}
	var err error
	switch e.cfg.Transport {
	case enum.MCPHTTP, enum.MCPSSE:
		conn, err = mcp.Dial(e.cfg.URL, e.cfg.Headers, e.cfg.Transport == enum.MCPSSE)
	default:
		conn, err = mcp.Spawn(e.cfg.Command, e.cfg.Args, e.cfg.Env)
	}
	if err == nil {
		e.client, err = mcp.Connect(ctx, conn, r.clientName, r.version)
	}
	if err != nil {
		e.client, e.err = nil, err.Error()
		r.log.Warn("MCP server unavailable", "server", e.cfg.Name, "error", err)
		return err
	}
	e.err = ""
	return nil
}

func (e *external) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client != nil {
		_ = e.client.Close()
		e.client = nil
	}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// toolName is the name the model sees: the sanitized server name, then
// the tool's, within the 64 characters providers allow. A result that
// starts with a digit gains an "mcp" prefix — providers like Gemini
// reject names that cannot start an identifier.
func toolName(server, tool string) string {
	name := unsafeName.ReplaceAllString(server, "_") + "_" + unsafeName.ReplaceAllString(tool, "_")
	if name[0] >= '0' && name[0] <= '9' {
		name = "mcp" + name
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

type externalTool struct {
	reg    *Registry
	server string
	name   string
	schema valueobject.ToolSchema
}

func (t *externalTool) Schema() valueobject.ToolSchema { return t.schema }

func (t *externalTool) Execute(ctx context.Context, call entity.ToolCall, _ dservice.ToolEmitter) (dservice.ToolResult, error) {
	t.reg.mu.RLock()
	e := t.reg.servers[t.server]
	t.reg.mu.RUnlock()
	if e == nil {
		return dservice.ToolResult{}, fmt.Errorf("the MCP server %s was removed", t.server)
	}
	if err := e.connect(ctx, t.reg); err != nil {
		return dservice.ToolResult{}, fmt.Errorf("the MCP server %s is unavailable: %w", t.server, err)
	}
	e.mu.Lock()
	client := e.client
	e.mu.Unlock()
	if client == nil {
		return dservice.ToolResult{}, errors.New("the MCP server " + t.server + " is not connected")
	}
	args := strings.TrimSpace(call.Input())
	if args == "" {
		args = "{}"
	}
	res, err := client.Call(ctx, t.name, []byte(args))
	if err != nil {
		return dservice.ToolResult{}, err
	}
	return toolResult(ctx, t.reg.outputs, dservice.ConversationID(ctx), call.ID(), res)
}
