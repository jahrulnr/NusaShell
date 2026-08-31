// Command fakeacp is a scripted Agent Client Protocol server over stdio
// used by handler-level tests. It speaks JSON-RPC 2.0 with newline-delimited
// frames (ACP stdio). It still accepts Content-Length on stdin.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	clock "nusashell/pkg/time"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      any     `json:"id,omitempty"`
	Result  any     `json:"result,omitempty"`
	Error   *rpcErr `json:"error,omitempty"`
	Method  string  `json:"method,omitempty"`
	Params  any     `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var (
	mu         sync.Mutex
	authed     bool
	reAuthed   bool
	sessions   = map[string]string{}
	cancels    = map[string]chan struct{}{}
	rpcWait    = map[any]chan request{}
	permSeq    int
	sessionSeq atomic.Uint64
)

func main() {
	if os.Getenv("FAKEACP_CRASH_BOOT") == "1" {
		os.Exit(2)
	}
	r := bufio.NewReader(os.Stdin)
	for {
		body, err := readFrame(r)
		if err != nil {
			return
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}
		go handle(req)
	}
}

func handle(req request) {
	if req.Method == "" {
		mu.Lock()
		ch, ok := rpcWait[req.ID]
		if ok {
			delete(rpcWait, req.ID)
		}
		mu.Unlock()
		if ok {
			ch <- req
		}
		return
	}
	switch req.Method {
	case "initialize":
		auth := []any{}
		if os.Getenv("FAKEACP_AUTH") == "1" || os.Getenv("FAKEACP_SOFT_AUTH") == "1" {
			auth = []any{map[string]any{"id": "cursor_login", "name": "Cursor login", "description": "Sign in via Cursor"}}
		}
		reply(req.ID, map[string]any{
			"protocolVersion": 1,
			"agentCapabilities": map[string]any{
				"loadSession":        false,
				"promptCapabilities": map[string]any{"image": false},
			},
			"agentInfo":   map[string]any{"name": "fakeacp", "title": "Fake ACP", "version": "1.0.0"},
			"authMethods": auth,
		})
	case "authenticate":
		authed = true
		reAuthed = true
		reply(req.ID, map[string]any{})
	case "session/new":
		if os.Getenv("FAKEACP_SOFT_AUTH") == "1" && reAuthed {
			fail(req.ID, "re-authentication triggered unnecessarily")
			return
		}
		if os.Getenv("FAKEACP_AUTH") == "1" && !authed {
			fail(req.ID, "authentication required")
			return
		}
		id := fmt.Sprintf("sess_%d_%d", clock.NewTime().EpochNano(), sessionSeq.Add(1))
		mu.Lock()
		sessions[id] = "plan"
		cancels[id] = make(chan struct{}, 1)
		mu.Unlock()
		// FAKEACP_CONFIG_OPTIONS simulates OpenCode-generation agents whose
		// session/new returns v1 configOptions instead of legacy modes/models.
		if os.Getenv("FAKEACP_CONFIG_OPTIONS") == "1" {
			reply(req.ID, map[string]any{
				"sessionId": id,
				"configOptions": []any{
					map[string]any{
						"id": "model", "name": "Model", "category": "model", "type": "select",
						"currentValue": "prov/alpha",
						"options": []any{
							map[string]any{"value": "prov/alpha", "name": "Prov/Alpha"},
							map[string]any{"value": "prov/beta", "name": "Prov/Beta"},
						},
					},
					map[string]any{
						"id": "mode", "name": "Session Mode", "category": "mode", "type": "select",
						"currentValue": "build",
						"options": []any{
							map[string]any{"value": "build", "name": "Build"},
							map[string]any{"value": "plan", "name": "Plan", "description": "Read-only planning"},
						},
					},
					// Boolean options exercise the raw-JSON current value path.
					map[string]any{
						"id": "flag", "name": "Flag", "category": "_custom", "type": "boolean",
						"currentValue": false,
					},
				},
			})
			return
		}
		modes := map[string]any{}
		if os.Getenv("FAKEACP_NO_MODES") != "1" {
			modes = map[string]any{
				"currentModeId": "plan",
				"availableModes": []any{
					map[string]any{"id": "plan", "name": "Plan", "description": "Read-only planning"},
					map[string]any{"id": "code", "name": "Code", "description": "Edit with confirmation"},
					map[string]any{"id": "bypassPermissions", "name": "Bypass", "description": "Skip prompts"},
				},
			}
		}
		reply(req.ID, map[string]any{
			"sessionId": id,
			"modes":     modes,
			"models": map[string]any{
				"currentModelId": "auto",
				"availableModels": []any{
					map[string]any{"modelId": "auto", "name": "auto", "description": "Default"},
					map[string]any{"modelId": "test-model", "name": "test-model", "description": "Fixture model"},
				},
			},
		})
	case "session/prompt":
		handlePrompt(req)
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		mu.Lock()
		ch := cancels[p.SessionID]
		mu.Unlock()
		if ch != nil {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	case "session/set_mode":
		var p struct {
			SessionID string `json:"sessionId"`
			ModeID    string `json:"modeId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		mu.Lock()
		sessions[p.SessionID] = p.ModeID
		mu.Unlock()
		notify("session/update", map[string]any{
			"sessionId": p.SessionID,
			"update":    map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": p.ModeID},
		})
		reply(req.ID, map[string]any{})
	case "session/set_model":
		var p struct {
			ModelID string `json:"modelId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		reply(req.ID, map[string]any{
			"models": map[string]any{"currentModelId": p.ModelID, "availableModels": []any{}},
		})
	default:
		if req.ID != nil {
			fail(req.ID, "method not found")
		}
	}
}

func handlePrompt(req request) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(req.Params, &p)
	text := ""
	if len(p.Prompt) > 0 {
		text = p.Prompt[0].Text
	}
	if strings.Contains(text, "CRASH") {
		os.Exit(3)
	}
	notify("session/update", map[string]any{
		"sessionId": p.SessionID,
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "working: " + text},
		},
	})
	if strings.Contains(text, "NEED_PERMISSION") {
		notify("session/update", map[string]any{
			"sessionId": p.SessionID,
			"update": map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    "call_edit",
				"title":         "Edit README.md",
				"kind":          "edit",
				"status":        "pending",
				"locations":     []any{map[string]any{"path": "/tmp/workspace/README.md"}},
			},
		})
		{
			mu.Lock()
			permSeq++
			permID := fmt.Sprintf("perm-%d", permSeq)
			permCh := make(chan request, 1)
			rpcWait[permID] = permCh
			mu.Unlock()
			permBody, _ := json.Marshal(response{
				JSONRPC: "2.0",
				ID:      permID,
				Method:  "session/request_permission",
				Params: map[string]any{
					"sessionId": p.SessionID,
					"toolCall": map[string]any{
						"toolCallId": "call_edit",
						"title":      "Edit README.md",
						"kind":       "edit",
						"locations":  []any{map[string]any{"path": "/tmp/workspace/README.md"}},
					},
					"options": []any{
						map[string]any{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
						map[string]any{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
					},
				},
			})
			writeFrame(permBody)
			select {
			case <-permCh:
			case <-time.After(30 * time.Second):
			}
		}
	}
	mu.Lock()
	cancelCh := cancels[p.SessionID]
	mu.Unlock()
	if strings.Contains(text, "SLOW") {
		select {
		case <-time.After(2 * time.Second):
		case <-cancelCh:
			reply(req.ID, map[string]any{"stopReason": "cancelled"})
			return
		}
	}
	select {
	case <-cancelCh:
		reply(req.ID, map[string]any{"stopReason": "cancelled"})
	default:
		reply(req.ID, map[string]any{"stopReason": "end_turn"})
	}
}

func reply(id any, result any) {
	body, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result})
	writeFrame(body)
}

func fail(id any, msg string) {
	body, _ := json.Marshal(response{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: -32000, Message: msg}})
	writeFrame(body)
}

func notify(method string, params any) {
	body, _ := json.Marshal(response{JSONRPC: "2.0", Method: method, Params: params})
	writeFrame(body)
}

func writeFrame(body []byte) {
	mu.Lock()
	defer mu.Unlock()
	_, _ = os.Stdout.Write(body)
	_, _ = os.Stdout.Write([]byte("\n"))
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	for {
		prefix, err := r.Peek(1)
		if err != nil {
			return nil, err
		}
		if prefix[0] == '{' {
			line, err := r.ReadBytes('\n')
			if err != nil && len(bytes.TrimSpace(line)) == 0 {
				return nil, err
			}
			return bytes.TrimSpace(line), nil
		}
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "content-length:") {
			n, err := strconv.Atoi(strings.TrimSpace(line[len("Content-Length:"):]))
			if err != nil {
				return nil, err
			}
			for {
				h, err := r.ReadString('\n')
				if err != nil {
					return nil, err
				}
				if h == "\r\n" || h == "\n" {
					break
				}
			}
			buf := make([]byte, n)
			if _, err := io.ReadFull(r, buf); err != nil {
				return nil, err
			}
			return buf, nil
		}
	}
}
