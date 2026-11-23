// Package deliverymcp serves NusaShell's own use cases as MCP tool servers:
// "conversation" (work with other threads), "browser" (drive this
// thread's browser), and "memory" (remember the owner and their work
// across threads). A Catalog is a server's tools plus a call bound to the
// thread it serves. The same catalogs reach ACP agents through a relay
// child process and the model runtime through an in-process connection.
package deliverymcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
)

// Catalog is one built-in tool server.
type Catalog struct {
	Name string
	// Purpose is a one-line description for whoever lists the servers.
	Purpose string
	Tools   []mcp.Tool
	// Call runs a tool for the thread convID.
	Call func(ctx context.Context, convID, tool string, args json.RawMessage) (mcp.Result, error)
}

// Server binds the catalog to one thread.
func (c Catalog) Server(convID string) mcp.Server {
	return mcp.Server{
		Name: c.Name, Version: "1", Tools: c.Tools,
		Call: func(ctx context.Context, name string, args json.RawMessage) (mcp.Result, error) {
			return c.Call(ctx, convID, name, args)
		},
	}
}

// Has reports whether the catalog offers a tool.
func (c Catalog) Has(tool string) bool {
	for _, t := range c.Tools {
		if t.Name == tool {
			return true
		}
	}
	return false
}

// decode reads tool arguments into v; absent arguments are an empty object.
func decode(args json.RawMessage, v any) error {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("arguments: %v", err)
	}
	return nil
}

// jsonResult answers with v as indented JSON text.
func jsonResult(v any, err error) (mcp.Result, error) {
	if err != nil {
		return mcp.Result{}, err
	}
	raw, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return mcp.Result{}, err
	}
	return mcp.TextResult(string(raw)), nil
}

// schema builds an object input schema. props maps a name to its JSON
// schema; required names the mandatory ones.
func schema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	raw, _ := json.Marshal(s)
	return raw
}

func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any  { return map[string]any{"type": "number", "description": desc} }
func flag(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
