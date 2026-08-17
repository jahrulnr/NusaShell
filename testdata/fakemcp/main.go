// Command fakemcp is a minimal JSON-RPC MCP server over stdio used by
// handler-level tests. It implements initialize, tools/list and tools/call.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		handle(req)
	}
}

func reply(id json.RawMessage, result any, err *rpcError) {
	b, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result, Error: err})
	fmt.Println(string(b))
}

func handle(req request) {
	switch req.Method {
	case "initialize":
		reply(req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "fakemcp", "version": "1.0.0"},
		}, nil)
	case "notifications/initialized":
		// no reply
	case "tools/list":
		reply(req.ID, map[string]any{"tools": []tool{
			{
				Name:        "echo",
				Description: "Echo back the given text",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{"type": "string"},
					},
					"required": []string{"text"},
				},
			},
			{
				Name:        "add",
				Description: "Add two integers",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a": map[string]any{"type": "integer"},
						"b": map[string]any{"type": "integer"},
					},
					"required": []string{"a", "b"},
				},
			},
			{
				Name:        "structured",
				Description: "Return a structured content payload with a human-readable text summary",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"label": map[string]any{"type": "string"},
					},
					"required": []string{"label"},
				},
			},
		}}, nil)
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		switch params.Name {
		case "echo":
			text, _ := params.Arguments["text"].(string)
			reply(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo: " + text}},
			}, nil)
		case "add":
			a, _ := params.Arguments["a"].(float64)
			b, _ := params.Arguments["b"].(float64)
			sum := int(a) + int(b)
			reply(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("%d", sum)}},
			}, nil)
		case "structured":
			label, _ := params.Arguments["label"].(string)
			// Mimic the NusaShell-mcp plugins: human-readable text + a
			// structuredContent payload. The host bridge must forward
			// structuredContent so plugin UIs can render JSON without
			// parsing the text.
			reply(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "label=" + label}},
				"structuredContent": map[string]any{
					"label": label,
					"ok":    true,
				},
			}, nil)
		default:
			reply(req.ID, nil, &rpcError{Code: -32602, Message: "unknown tool: " + params.Name})
		}
	case "ping":
		reply(req.ID, map[string]any{}, nil)
	default:
		reply(req.ID, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
	}
}
