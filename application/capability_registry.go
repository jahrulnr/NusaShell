package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

// CapabilityRegistry resolves logical capability names to builtin or MCP
// providers. The CI Engine consumes this interface and does not
// care which implementation sits underneath.
type CapabilityRegistry struct {
	Plugins   PluginStore
	MCP       MCPToolbox
	Caller    MCPToolCaller
	State     ProviderStateStore
	Workflows WorkflowStore
	Builtin   map[string]BuiltinCapability
}

// BuiltinCapability is a deterministic in-process action.
type BuiltinCapability struct {
	Name        string
	Kind        domain.CapabilityKind
	Description string
	Execute     func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

func NewCapabilityRegistry() *CapabilityRegistry {
	r := &CapabilityRegistry{Builtin: map[string]BuiltinCapability{}}
	r.RegisterBuiltin(BuiltinCapability{
		Name: "filesystem.read", Kind: domain.CapabilityAction,
		Execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			var req struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(input, &req)
			if req.Path == "" {
				return nil, fmt.Errorf("path is required")
			}
			b, err := os.ReadFile(req.Path)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"path": req.Path, "content": string(b)})
		},
	})
	r.RegisterBuiltin(BuiltinCapability{
		Name: "filesystem.write", Kind: domain.CapabilityAction,
		Execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			var req struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			_ = json.Unmarshal(input, &req)
			if req.Path == "" {
				return nil, fmt.Errorf("path is required")
			}
			if err := os.MkdirAll(filepath.Dir(req.Path), 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(req.Path, []byte(req.Content), 0o600); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"path": req.Path, "ok": true})
		},
	})
	r.RegisterBuiltin(BuiltinCapability{
		Name: "filesystem.changed", Kind: domain.CapabilityEvent,
		Execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(map[string]any{"ok": true})
		},
	})
	r.RegisterBuiltin(BuiltinCapability{
		Name: "http.request", Kind: domain.CapabilityAction,
		Execute: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("http.request is not enabled in this build")
		},
	})
	r.RegisterBuiltin(BuiltinCapability{
		Name: "workflow.wait_until", Kind: domain.CapabilityAction,
		Execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return input, nil
		},
	})
	return r
}

func (r *CapabilityRegistry) RegisterBuiltin(c BuiltinCapability) {
	if r.Builtin == nil {
		r.Builtin = map[string]BuiltinCapability{}
	}
	r.Builtin[c.Name] = c
}

func (r *CapabilityRegistry) List(ctx context.Context) []domain.CapabilityBinding {
	var out []domain.CapabilityBinding
	for name := range r.Builtin {
		out = append(out, domain.CapabilityBinding{
			Capability: name, ProviderID: "builtin", Kind: domain.CapabilityBuiltin, Status: domain.CapAvailable,
		})
	}
	if r.Plugins == nil || r.MCP == nil {
		return out
	}
	plugins, _ := r.Plugins.List()
	for _, p := range plugins {
		tools, active := r.MCP.ToolsFor(p.Manifest.MCPServerID())
		status := domain.CapNotRunning
		if active {
			status = domain.CapAvailable
		}
		if r.disabled(ctx, p.Manifest.ID) {
			status = domain.CapDisabled
		}
		for _, t := range tools {
			name := logicalCapability(p.Manifest.Name, t.Name)
			out = append(out, domain.CapabilityBinding{
				Capability: name, ProviderID: p.Manifest.ID, Kind: domain.CapabilityMCP, Status: status,
			})
		}
		if !active {
			out = append(out, domain.CapabilityBinding{
				Capability: p.Manifest.Name, ProviderID: p.Manifest.ID, Kind: domain.CapabilityMCP, Status: status,
			})
		}
	}
	return out
}

func (r *CapabilityRegistry) Resolve(ctx context.Context, name string, policy domain.AutoStartPolicy) (domain.CapabilityBinding, error) {
	if c, ok := r.Builtin[name]; ok {
		return domain.CapabilityBinding{
			Capability: c.Name, ProviderID: "builtin", Kind: domain.CapabilityBuiltin, Status: domain.CapAvailable,
		}, nil
	}
	if r.Plugins == nil {
		return domain.CapabilityBinding{Capability: name, Status: domain.CapMissing, Reason: "unknown_capability"}, fmt.Errorf("capability %q does not exist", name)
	}
	plugins, err := r.Plugins.List()
	if err != nil {
		return domain.CapabilityBinding{}, err
	}
	var missing bool
	var match *domain.Plugin
	var toolName string
	for _, p := range plugins {
		var tools []contracts.MCPToolDTO
		ok := r.MCP != nil
		if ok {
			tools, ok = r.MCP.ToolsFor(p.Manifest.MCPServerID())
		}
		for _, t := range tools {
			if logicalCapability(p.Manifest.Name, t.Name) == name || t.Name == name {
				match = p
				toolName = t.Name
				_ = ok
				break
			}
		}
		if match != nil {
			break
		}
		// Allow workflows to name capabilities after the plugin when the
		// event/action is advertised only as a logical name.
		if p.Manifest.Name == name || p.Manifest.ID == name {
			match = p
		}
	}
	if match == nil {
		missing = true
	}
	if missing {
		return domain.CapabilityBinding{Capability: name, Status: domain.CapMissing, Reason: "unknown_capability"}, fmt.Errorf("capability %q does not exist", name)
	}
	status := domain.CapNotRunning
	if r.MCP != nil {
		if _, ok := r.MCP.ToolsFor(match.Manifest.MCPServerID()); ok {
			status = domain.CapAvailable
		}
	}
	if r.disabled(ctx, match.Manifest.ID) {
		status = domain.CapDisabled
		return domain.CapabilityBinding{
			Capability: name, ProviderID: match.Manifest.ID, Kind: domain.CapabilityMCP,
			Status: status, Reason: "provider_disabled",
		}, nil
	}
	_ = toolName
	_ = policy
	return domain.CapabilityBinding{
		Capability: name, ProviderID: match.Manifest.ID, Kind: domain.CapabilityMCP, Status: status,
	}, nil
}

func (r *CapabilityRegistry) EnsureAvailable(ctx context.Context, binding domain.CapabilityBinding, policy domain.AutoStartPolicy) (domain.CapabilityBinding, error) {
	if binding.Kind != domain.CapabilityMCP {
		return binding, nil
	}
	if binding.Status == domain.CapAvailable {
		return binding, nil
	}
	if binding.Status == domain.CapDisabled || binding.Status == domain.CapMissing {
		return binding, nil
	}
	serverAuto := false
	var plugin *domain.Plugin
	if r.Plugins != nil {
		p, err := r.Plugins.Get(binding.ProviderID)
		if err == nil {
			plugin = p
			serverAuto = p.Manifest.MCP.Autostart
		}
	}
	if !domain.AllowsAutoStart(binding.Status, policy, serverAuto) {
		return binding, nil
	}
	if plugin == nil || r.MCP == nil {
		binding.Status = domain.CapError
		binding.Reason = "provider_error"
		return binding, nil
	}
	binding.Status = domain.CapStarting
	if _, err := r.MCP.Connect(ctx, plugin); err != nil {
		binding.Status = domain.CapError
		binding.Reason = err.Error()
		return binding, err
	}
	binding.Status = domain.CapAvailable
	binding.Reason = ""
	return binding, nil
}

func (r *CapabilityRegistry) Execute(ctx context.Context, binding domain.CapabilityBinding, input json.RawMessage) (json.RawMessage, error) {
	if binding.Kind == domain.CapabilityBuiltin {
		c, ok := r.Builtin[binding.Capability]
		if !ok || c.Execute == nil {
			return nil, fmt.Errorf("builtin %s not executable", binding.Capability)
		}
		return c.Execute(ctx, input)
	}
	if r.Caller == nil {
		return nil, fmt.Errorf("mcp caller not configured")
	}
	if r.Plugins == nil {
		return nil, fmt.Errorf("plugin store not configured")
	}
	p, err := r.Plugins.Get(binding.ProviderID)
	if err != nil {
		return nil, err
	}
	args := map[string]any{}
	_ = json.Unmarshal(input, &args)
	tool := binding.Capability
	if i := strings.LastIndexByte(tool, '.'); i >= 0 {
		tool = tool[i+1:]
	}
	tool = strings.ReplaceAll(tool, ".", "_")
	out, err := r.Caller.CallTool(ctx, p.Manifest.MCPServerID(), tool, args)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

func (r *CapabilityRegistry) Dependents(ctx context.Context, providerID string) ([]*domain.WorkflowDefinition, error) {
	if r.Workflows == nil {
		return nil, nil
	}
	all, err := r.Workflows.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*domain.WorkflowDefinition
	for _, w := range all {
		for _, name := range w.ReferencedCapabilities() {
			b, err := r.Resolve(ctx, name, domain.DefaultAutoStart)
			if err != nil {
				continue
			}
			if b.ProviderID == providerID {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}

func (r *CapabilityRegistry) SetDisabled(ctx context.Context, providerID string, disabled bool) error {
	if r.State == nil {
		return fmt.Errorf("provider state store not configured")
	}
	return r.State.SetDisabled(ctx, providerID, disabled)
}

func (r *CapabilityRegistry) disabled(ctx context.Context, providerID string) bool {
	if r.State == nil {
		return false
	}
	d, ok, err := r.State.Get(ctx, providerID)
	return err == nil && ok && d
}

func logicalCapability(server, tool string) string {
	t := strings.ReplaceAll(tool, "_", ".")
	if strings.Contains(t, ".") {
		return t
	}
	if server == "" {
		return t
	}
	return server + "." + t
}
