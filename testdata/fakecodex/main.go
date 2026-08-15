// Command fakecodex is a minimal Codex app-server over NDJSON JSON-RPC
// used by CompactServer subprocess tests. It implements initialize,
// thread/start, turn/start, and thread/compact/start.
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

func reply(id json.RawMessage, result any) {
	b, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result})
	fmt.Println(string(b))
}

func notify(method string, params any) {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	fmt.Println(string(b))
}

func handle(req request) {
	switch req.Method {
	case "initialize":
		reply(req.ID, map[string]any{
			"userAgent":      "test",
			"codexHome":      "/tmp",
			"platformFamily": "unix",
			"platformOs":     "linux",
		})
	case "initialized":
		// notification — no reply
	case "thread/start":
		reply(req.ID, map[string]any{
			"thread": map[string]any{"id": "test-thread-001"},
		})
	case "turn/start":
		reply(req.ID, map[string]any{
			"turn": map[string]any{
				"id":     "turn-1",
				"items":  []any{},
				"status": "inProgress",
			},
		})
		notify("turn/completed", map[string]any{
			"threadId": "test-thread-001",
			"turn":     map[string]any{"id": "turn-1", "status": "completed"},
		})
	case "thread/compact/start":
		reply(req.ID, map[string]any{})
		notify("item/started", map[string]any{
			"item":     map[string]any{"type": "contextCompaction", "id": "cc-1"},
			"threadId": "test-thread-001",
			"turnId":   "compact-turn",
		})
		notify("item/completed", map[string]any{
			"item":     map[string]any{"type": "agentMessage", "text": "Compaction summary: user discussed testing."},
			"threadId": "test-thread-001",
			"turnId":   "compact-turn",
		})
		notify("turn/completed", map[string]any{
			"threadId": "test-thread-001",
			"turn":     map[string]any{"id": "compact-turn", "status": "completed"},
		})
	}
}
