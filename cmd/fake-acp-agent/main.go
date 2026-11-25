// Command fake-acp-agent is a deterministic ACP agent used by tests and for
// exercising the NusaShell web UI without a network-backed agent. It streams a
// thought, a plan, a tool call and a permission request before answering.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

type server struct {
	mu      sync.Mutex
	out     *bufio.Writer
	nextID  int
	pending map[string]chan json.RawMessage
	cancel  chan struct{}
	values  map[string]any
	legacy  bool
	// modelSet records whether a set_config_option picked the model, as
	// opposed to the value the process was seeded with — the gate behind
	// which EFFORT_AFTER_MODEL's dependent option hides.
	modelSet bool
	busy     bool
	// sessionCounter mints a unique id per session/new like a real agent.
	// current is the id the latest session call addressed; updates carry it.
	sessionCounter int
	current        string
	// mcp are the MCP servers the latest session call handed over, by name.
	mcp map[string]mcpServer
	// cwd is the working directory of the latest session call.
	cwd string
	// authed reports a sign-in; with FAKE_ACP_AUTH=1 sessions need one.
	authed bool
	// clientCaps is the clientCapabilities object of the latest initialize —
	// agents gate offered options on what the client says it understands.
	clientCaps map[string]any
}

type mcpServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"env"`
}

func main() {
	// terminalDemo re-executes this binary as a plain process: print two
	// chunks with a pause between them and exit 3. Deterministic output and a
	// nonzero exit code without depending on a system shell, which is not
	// guaranteed to exist on Windows.
	if len(os.Args) > 1 && os.Args[1] == "--emit-terminal-demo" {
		fmt.Print("first")
		time.Sleep(500 * time.Millisecond)
		fmt.Print("second")
		os.Exit(3)
	}
	// --login is the "Terminal login" method's command: an interactive
	// sign-in that asks for a name on the terminal and exits.
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--login" {
		fmt.Print("Fake ACP sign-in\r\nName: ")
		name, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Printf("Signed in as %s.\r\n", strings.TrimSpace(name))
		return
	}
	s := &server{
		out:     bufio.NewWriter(os.Stdout),
		pending: map[string]chan json.RawMessage{},
		cancel:  make(chan struct{}),
		values:  map[string]any{"mode": "ask", "model": "fake-fast"},
		legacy:  os.Getenv("FAKE_ACP_MODES") == "legacy",
	}
	if os.Getenv("FAKE_ACP_BOOL_WHEN_CAP") == "1" {
		s.values["cap_bool"] = false
	}
	fmt.Fprintln(os.Stderr, "fake-acp: started, pid", os.Getpid())
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Method == "" && len(msg.ID) > 0 {
			key := string(msg.ID)
			s.mu.Lock()
			ch := s.pending[key]
			delete(s.pending, key)
			s.mu.Unlock()
			if ch != nil {
				select {
				case ch <- msg.Result:
				default:
				}
			}
			continue
		}
		// Prompt and cancel state changes happen here, in stdin order, so a
		// cancel always targets the prompt written before it.
		switch msg.Method {
		case "session/prompt":
			if os.Getenv("FAKE_ACP_MIDTURN") == "1" && s.isBusy() {
				go s.steerPrompt(msg)
				break
			}
			s.setBusy(true)
			cancel := s.newPromptCancel()
			s.startPrompt(msg, cancel)
		case "session/cancel":
			s.closeCancel()
		default:
			go s.handle(msg)
		}
	}
}

func (s *server) newPromptCancel() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel = make(chan struct{})
	return s.cancel
}

func (s *server) isBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

func (s *server) setBusy(v bool) {
	s.mu.Lock()
	s.busy = v
	s.mu.Unlock()
}

func (s *server) steerPrompt(msg message) {
	var p struct {
		Prompt []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	text := strings.TrimSpace(promptText(p.Prompt))
	trace("steer " + text)
	s.update(map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"messageId":     "steer",
		"content":       map[string]any{"type": "text", "text": "saw-steer: " + text},
	})
	s.respond(msg.ID, map[string]any{"stopReason": "end_turn"})
}

func (s *server) closeCancel() {
	trace("cancel")
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.cancel:
	default:
		close(s.cancel)
	}
}

func (s *server) handle(msg message) {
	switch msg.Method {
	case "initialize":
		var init struct {
			ClientCapabilities map[string]any `json:"clientCapabilities"`
		}
		_ = json.Unmarshal(msg.Params, &init)
		s.mu.Lock()
		s.clientCaps = init.ClientCapabilities
		s.mu.Unlock()
		if dump := os.Getenv("FAKE_ACP_CAPS_DUMP"); dump != "" {
			raw, _ := json.Marshal(init.ClientCapabilities)
			_ = os.WriteFile(dump, raw, 0o600)
		}
		caps := map[string]any{
			"loadSession": true,
			"promptCapabilities": map[string]any{
				"image": true, "audio": false, "embeddedContext": true,
			},
			"mcpCapabilities": map[string]any{"http": true, "sse": true},
			"sessionCapabilities": map[string]any{
				"list":   map[string]any{},
				"delete": map[string]any{},
				"close":  map[string]any{},
			},
			"auth": map[string]any{},
		}
		if os.Getenv("FAKE_ACP_SYSTEM_PROMPT") == "1" {
			caps["sessionCapabilities"].(map[string]any)["systemPrompt"] = map[string]any{}
		}
		if os.Getenv("FAKE_ACP_RESUME") == "1" {
			caps["sessionCapabilities"].(map[string]any)["resume"] = map[string]any{}
		}
		// An empty object is how an agent says yes to a capability that has no
		// options; Devin advertises additionalDirectories this way.
		if os.Getenv("FAKE_ACP_ADDITIONAL_DIRS") == "1" {
			caps["sessionCapabilities"].(map[string]any)["additionalDirectories"] = map[string]any{}
		}
		if os.Getenv("FAKE_ACP_AUDIO") == "1" {
			caps["promptCapabilities"].(map[string]any)["audio"] = true
		}
		meta := map[string]any{}
		if os.Getenv("FAKE_ACP_MIDTURN") == "1" {
			meta["cognition.ai/midTurnPrompts"] = true
		}
		if os.Getenv("FAKE_ACP_STEERING") == "1" {
			meta["steering"] = true
		}
		if len(meta) > 0 {
			caps["_meta"] = meta
		}
		auths := []any{}
		if os.Getenv("FAKE_ACP_AUTH") == "1" {
			auths = []any{
				map[string]any{"id": "fake-login", "name": "Fake account", "description": "Signs in instantly"},
				map[string]any{"id": "fake-terminal", "name": "Terminal login", "type": "terminal", "args": []string{"--login"}},
			}
			caps["auth"] = map[string]any{"logout": map[string]any{}}
		}
		s.respond(msg.ID, map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": caps,
			"authMethods":       auths,
			"agentInfo":         map[string]any{"name": "fake-acp", "title": "Fake ACP Agent", "version": "0.1.0"},
		})
	case "authenticate":
		s.mu.Lock()
		s.authed = true
		s.mu.Unlock()
		fmt.Fprintln(os.Stderr, "fake-acp: signed in")
		s.respond(msg.ID, map[string]any{})
	case "logout":
		s.mu.Lock()
		s.authed = false
		s.mu.Unlock()
		fmt.Fprintln(os.Stderr, "fake-acp: signed out")
		s.respond(msg.ID, map[string]any{})
	case "session/new":
		s.mu.Lock()
		authed := s.authed
		s.mu.Unlock()
		if os.Getenv("FAKE_ACP_AUTH") == "1" && !authed {
			fmt.Fprintln(os.Stderr, "fake-acp: refusing session/new, not signed in")
			s.write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32000, "message": "Authentication required"}})
			return
		}
		traceSessionPrompt(msg.Method, msg.Params)
		trace("session_new")
		var request struct {
			Cwd        string      `json:"cwd"`
			MCPServers []mcpServer `json:"mcpServers"`
		}
		if json.Unmarshal(msg.Params, &request) == nil {
			s.mu.Lock()
			s.cwd = request.Cwd
			s.mcp = map[string]mcpServer{}
			for _, server := range request.MCPServers {
				trace("session_mcp " + server.Name)
				s.mcp[server.Name] = server
			}
			s.mu.Unlock()
		}
		id := s.newSessionID()
		s.mu.Lock()
		s.cancel = make(chan struct{})
		s.values["mode"] = "ask"
		s.mu.Unlock()
		s.notify("session/update", map[string]any{
			"sessionId": id,
			"update":    map[string]any{"sessionUpdate": "config_option_update", "configOptions": s.configOptions()},
		})
		s.notify("session/update", map[string]any{
			"sessionId": id,
			"update": map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{
				map[string]any{"name": "compact", "description": "Compact the fake context"},
			}},
		})
		s.respond(msg.ID, s.sessionResult(id))
	case "session/load", "session/resume":
		traceSessionPrompt(msg.Method, msg.Params)
		if os.Getenv("FAKE_ACP_REJECT_LOAD") == "1" {
			s.write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32602, "message": "invalid system prompt"}})
			return
		}
		// Resume echoes the requested id, like an agent that owns its
		// sessions; it also becomes the id subsequent updates carry.
		var req struct {
			SessionID  string      `json:"sessionId"`
			MCPServers []mcpServer `json:"mcpServers"`
		}
		_ = json.Unmarshal(msg.Params, &req)
		s.mu.Lock()
		s.mcp = map[string]mcpServer{}
		for _, server := range req.MCPServers {
			s.mcp[server.Name] = server
		}
		if req.SessionID != "" {
			s.current = req.SessionID
		}
		id := s.current
		s.mu.Unlock()
		s.respond(msg.ID, s.sessionResult(id))
	case "session/set_config_option":
		var req struct {
			ConfigID string `json:"configId"`
			Value    any    `json:"value"`
		}
		_ = json.Unmarshal(msg.Params, &req)
		trace(fmt.Sprintf("config %s %v", req.ConfigID, req.Value))
		s.mu.Lock()
		s.values[req.ConfigID] = req.Value
		if req.ConfigID == "model" {
			s.modelSet = true
		}
		if req.ConfigID == "model" && os.Getenv("FAKE_ACP_MODEL_RESETS_EFFORT") == "1" {
			// Emulate agents whose model value implies an effort level (like
			// Devin's swe-2-high): picking a model resets effort to its default.
			s.values["effort"] = "low"
		}
		s.mu.Unlock()
		options := s.configOptions()
		if os.Getenv("FAKE_ACP_PARTIAL_CONFIG") == "1" {
			// Emulate agents that answer a write with the entry it changed and
			// nothing else — the response is not the offered list.
			options = nil
			for _, o := range s.configOptions() {
				if m, ok := o.(map[string]any); ok && m["id"] == req.ConfigID {
					options = append(options, m)
				}
			}
		}
		s.respond(msg.ID, map[string]any{"configOptions": options})
	case "session/set_mode":
		var req struct {
			ModeID string `json:"modeId"`
		}
		_ = json.Unmarshal(msg.Params, &req)
		trace("set_mode " + req.ModeID)
		s.mu.Lock()
		s.values["mode"] = req.ModeID
		s.mu.Unlock()
		s.respond(msg.ID, map[string]any{})
		s.update(map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": req.ModeID})
	case "_session/steering":
		var p struct {
			Prompt []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				MIMEType string `json:"mimeType"`
				Data     string `json:"data"`
			} `json:"prompt"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		var textBuilder strings.Builder
		for _, block := range p.Prompt {
			textBuilder.WriteString(block.Text)
			if block.Type == "image" && block.Data != "" {
				trace("steering_image " + block.MIMEType)
			}
		}
		text := strings.TrimSpace(strings.ReplaceAll(textBuilder.String(), "\n", " "))
		trace("steering " + text)
		switch {
		case os.Getenv("FAKE_ACP_STEER_DROP") == "1":
			// Accept but produce nothing — the dropped-steer case.
		case os.Getenv("FAKE_ACP_STEER_TOOL") == "1":
			s.update(map[string]any{
				"sessionUpdate": "tool_call", "toolCallId": "steer-tool-1",
				"title": "Check the request", "name": "read", "kind": "read", "status": "completed",
			})
		default:
			s.update(map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"messageId":     "steer",
				"content":       map[string]any{"type": "text", "text": "saw-steer: " + text},
			})
		}
		s.respond(msg.ID, map[string]any{"outcome": "injected"})
	case "session/close":
		trace("session_close")
		s.respond(msg.ID, map[string]any{})
	case "session/list":
		s.respond(msg.ID, map[string]any{"sessions": []any{map[string]any{"sessionId": s.sessionID(), "title": "Fake session"}}})
	case "session/delete":
		trace("session_delete")
		s.respond(msg.ID, map[string]any{})

	}
}

func (s *server) configOptions() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A boolean option exists so the client's non-select path is exercised:
	// its value is a bool on the wire, not a choice string.
	strict, _ := s.values["strict"].(bool)
	options := []any{
		map[string]any{
			"id": "model", "name": "Model", "category": "model", "type": "select",
			"currentValue": s.values["model"], "options": []any{
				map[string]any{"value": "fake-fast", "name": "Fake Fast"},
				map[string]any{"value": "fake-smart", "name": "Fake Smart"},
			},
		},
	}
	if os.Getenv("FAKE_ACP_DYNAMIC_CONFIG") == "1" || os.Getenv("FAKE_ACP_MODEL_RESETS_EFFORT") == "1" || os.Getenv("FAKE_ACP_EFFORT_AFTER_MODEL") == "1" {
		effort := []any{
			map[string]any{"value": "low", "name": "Low"},
			map[string]any{"value": "high", "name": "High"},
		}
		if os.Getenv("FAKE_ACP_DYNAMIC_CONFIG") == "1" {
			effort = effort[:1]
			if s.values["model"] == "fake-smart" {
				effort = append(effort, map[string]any{"value": "high", "name": "High"})
			}
		}
		// Codex gates reasoning_effort on the current model: the option
		// exists only once a model pick resolved (B-038).
		if os.Getenv("FAKE_ACP_EFFORT_AFTER_MODEL") != "1" || s.modelSet {
			current := s.values["effort"]
			if current == nil {
				current = "low"
			}
			options = append(options, map[string]any{"id": "effort", "name": "Effort", "category": "thought_level", "type": "select", "currentValue": current, "options": effort})
		}
	}
	if os.Getenv("FAKE_ACP_LADDER") == "1" {
		// A chain that always materializes the next rung when the previous
		// one resolves — exercises the bounded retry that must stop anyway.
		options = append(options, map[string]any{"id": "ladder", "name": "Ladder", "type": "select",
			"currentValue": s.values["ladder"], "options": []any{
				map[string]any{"value": "a", "name": "A"},
				map[string]any{"value": "b", "name": "B"},
			}})
		prev := "ladder"
		for k := 1; k <= 5 && s.values[prev] != nil; k++ {
			id := fmt.Sprintf("ladder%d", k)
			options = append(options, map[string]any{"id": id, "name": "Ladder " + id, "type": "select",
				"currentValue": s.values[id], "options": []any{
					map[string]any{"value": "a", "name": "A"},
					map[string]any{"value": "b", "name": "B"},
				}})
			prev = id
		}
	}
	if s.legacy {
		return options
	}
	options = append(options, map[string]any{"id": "strict", "name": "Strict mode", "category": "behavior", "type": "boolean", "currentValue": strict})
	// Codex's fast-mode gate: a boolean option offered only when the client
	// advertised session.configOptions.boolean in its capabilities.
	if os.Getenv("FAKE_ACP_BOOL_WHEN_CAP") == "1" {
		if sess, _ := s.clientCaps["session"].(map[string]any); sess != nil {
			if co, _ := sess["configOptions"].(map[string]any); co != nil && co["boolean"] != nil {
				options = append(options, map[string]any{"id": "cap_bool", "name": "Cap bool", "type": "boolean", "currentValue": s.values["cap_bool"]})
			}
		}
	}
	mode := map[string]any{
		"id": "mode", "name": "Session Mode", "category": "mode", "type": "select",
		"currentValue": s.values["mode"], "options": []any{
			map[string]any{"value": "ask", "name": "Ask"},
			map[string]any{"value": "accept-edits", "name": "Code"},
			map[string]any{"value": "bypass", "name": "Bypass permissions"},
		},
	}
	return append([]any{mode}, options...)
}

// startPrompt records dispatch in stdin order before the worker can race with
// the next session/cancel notification.
func (s *server) startPrompt(msg message, cancel chan struct{}) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	trace(fmt.Sprintf("prompt_start %s %s", msg.ID, promptText(p.Prompt)))
	types := make([]string, 0, len(p.Prompt))
	for _, block := range p.Prompt {
		types = append(types, block.Type)
	}
	trace("prompt_blocks " + strings.Join(types, ","))
	go func() {
		defer s.setBusy(false)
		s.prompt(msg.ID, p.SessionID, p.Prompt, cancel)
	}()
}

func (s *server) prompt(id json.RawMessage, sessionID string, prompt []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}, cancel chan struct{}) {
	var text strings.Builder
	for _, p := range prompt {
		if p.Text != "" {
			text.WriteString(p.Text)
		}
	}
	if strings.Contains(text.String(), "timeline-terminal-test") || strings.Contains(text.String(), "timeline-background-test") {
		s.terminalDemo(id, sessionID, strings.Contains(text.String(), "timeline-background-test"))
		return
	}
	if v := os.Getenv("FAKE_ACP_ELICIT"); v != "" {
		s.elicitDemo(id, sessionID, v)
		return
	}
	if strings.Contains(text.String(), "elicit-demo") {
		variant := "form"
		if strings.Contains(text.String(), "elicit-demo url") {
			variant = "url"
		} else if strings.Contains(text.String(), "elicit-demo broken") {
			variant = "broken"
		}
		s.elicitDemo(id, sessionID, variant)
		return
	}
	if strings.Contains(text.String(), "fs-demo") {
		s.fsDemo(id, sessionID)
		return
	}
	if rest, ok := strings.CutPrefix(strings.TrimSpace(text.String()), "mcp-call "); ok {
		s.mcpDemo(id, rest)
		return
	}
	if strings.Contains(text.String(), "mode-switch-test") {
		s.modeSwitchDemo(id, sessionID)
		return
	}
	toolDelay := 400 * time.Millisecond
	if strings.Contains(text.String(), "slow") {
		toolDelay = 4 * time.Second
	}
	if strings.Contains(text.String(), "after-tools-two-tools") {
		s.toolsDemo(id, cancel, false, toolDelay)
		return
	}
	if strings.Contains(text.String(), "after-tools-ignore-cancel") {
		s.toolsDemo(id, cancel, true, toolDelay)
		return
	}
	if strings.Contains(text.String(), "after-tools-no-tools") {
		s.noToolsDemo(id, cancel)
		return
	}
	if strings.Contains(text.String(), "after-tools-permission") {
		s.permissionDemo(id, sessionID, cancel)
		return
	}
	if strings.Contains(text.String(), "info-demo") {
		s.infoDemo(id)
		return
	}
	if strings.Contains(text.String(), "send-media-test") {
		s.mediaDemo(id, text.String())
		return
	}
	if strings.Contains(text.String(), "steer-hold") {
		s.chunk("working ")
		select {
		case <-cancel:
			s.respond(id, map[string]any{"stopReason": "cancelled"})
		case <-time.After(4 * time.Second):
			s.chunk("finished ")
			s.respond(id, map[string]any{"stopReason": "end_turn"})
		}
		return
	}
	if strings.Contains(text.String(), "steer-quiet-hold") {
		s.chunk("working ")
		select {
		case <-cancel:
			s.respond(id, map[string]any{"stopReason": "cancelled"})
		case <-time.After(4 * time.Second):
			s.respond(id, map[string]any{"stopReason": "end_turn"})
		}
		return
	}
	if strings.Contains(text.String(), "segment-reasons-test") {
		s.reasonsDemo(id)
		return
	}
	s.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{
		"type": "text", "text": "The fake agent received the prompt and is checking a safe path before acting.",
	}})
	s.update(map[string]any{"sessionUpdate": "plan", "entries": []any{
		map[string]any{"title": "Understand the request", "status": "completed"},
		map[string]any{"title": "Inspect the workspace", "status": "in_progress"},
		map[string]any{"title": "Reply with a summary", "status": "pending"},
	}})
	s.update(map[string]any{
		"sessionUpdate": "tool_call", "toolCallId": "fake-tool-1", "title": "Read workspace files",
		"name": "read", "kind": "read", "status": "pending",
		"rawInput":  map[string]any{"path": "."},
		"locations": []any{map[string]any{"path": "workspace/README.md"}},
	})
	select {
	case <-cancel:
		s.respond(id, map[string]any{"stopReason": "cancelled"})
		return
	case <-time.After(120 * time.Millisecond):
	}
	option := s.askPermission(sessionID, fakeTool{id: "fake-tool-1", title: "Read workspace files", name: "read", kind: "read"})
	if option == "" {
		s.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "fake-tool-1", "status": "failed", "rawOutput": map[string]any{"error": "permission denied"}})
		s.chunk("I did not get permission to inspect the workspace, so I stopped there.")
		s.respond(id, map[string]any{"stopReason": "end_turn"})
		return
	}
	s.update(map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": "fake-tool-1", "status": "completed",
		"rawOutput": map[string]any{"files": []string{"README.md", "AGENTS.md"}, "note": "read-only inspection"},
		"content":   []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "README.md\nAGENTS.md"}}},
	})
	s.update(map[string]any{"sessionUpdate": "plan", "entries": []any{
		map[string]any{"title": "Understand the request", "status": "completed"},
		map[string]any{"title": "Inspect the workspace", "status": "completed"},
		map[string]any{"title": "Reply with a summary", "status": "in_progress"},
	}})
	answer := "I inspected the workspace safely. The fake agent saw README.md and AGENTS.md. " +
		"If you asked for a demo, this is the demo: NusaShell streams thoughts, tools and approvals to web, while chat channels only receive this final digest."
	if strings.Contains(strings.ToLower(text.String()), "poem") {
		answer = "A quiet tide writes silver on the shore,\nAnd NusaShell answers, asking nothing more."
	}
	for word := range strings.FieldsSeq(answer) {
		select {
		case <-cancel:
			s.respond(id, map[string]any{"stopReason": "cancelled"})
			return
		case <-time.After(35 * time.Millisecond):
		}
		s.chunk(word + " ")
	}
	s.update(map[string]any{"sessionUpdate": "usage_update", "used": 1280, "size": 200000})
	s.update(map[string]any{"sessionUpdate": "plan", "entries": []any{
		map[string]any{"title": "Understand the request", "status": "completed"},
		map[string]any{"title": "Inspect the workspace", "status": "completed"},
		map[string]any{"title": "Reply with a summary", "status": "completed"},
	}})
	s.respond(id, map[string]any{"stopReason": "end_turn", "usage": map[string]any{
		"totalTokens": 1280, "inputTokens": 1180, "outputTokens": 100,
	}})
}

// infoDemo sends what an agent says about the session rather than the work:
// its own title for it, a notice, and a context compaction.
func (s *server) infoDemo(id json.RawMessage) {
	s.update(map[string]any{"sessionUpdate": "session_info_update", "title": "Fake agent's own title"})
	s.update(map[string]any{"sessionUpdate": "notice", "severity": "warning", "title": "Heads up", "description": "The fake agent has something to say."})
	s.update(map[string]any{"sessionUpdate": "compaction_update"})
	s.update(map[string]any{"sessionUpdate": "compaction_summary_chunk", "content": map[string]any{"type": "text", "text": "SECRET-SUMMARY"}})
	s.chunk("Done with the info demo.")
	s.respond(id, map[string]any{"stopReason": "end_turn"})
}

// mediaDemo emits a base64 image content block and, if the prompt carried a
// file:// URI, echoes it back inside a markdown link so the bridge can test
// both outbound media paths.
func (s *server) mediaDemo(id json.RawMessage, text string) {
	const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	// The harness prepends its system prompt, which itself mentions file://
	// as a link form; only the user's own words (from the scenario marker)
	// can name a file to echo back.
	if i := strings.Index(text, "send-media-test"); i > 0 {
		text = text[i:]
	}
	s.chunk("Here is the media you asked for. ")
	s.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{
		"type": "image", "mimeType": "image/png", "data": pngB64, "name": "pixel.png",
	}})
	if i := strings.Index(text, "file://"); i >= 0 {
		ref := strings.TrimRight(strings.Fields(text[i:])[0], ".,;")
		s.chunk(fmt.Sprintf("And a file: [result](%s)", ref))
	}
	s.respond(id, map[string]any{"stopReason": "end_turn"})
}

// toolsDemo announces tools and completes them one at a time. With
// ignoreCancel it never honours session/cancel and answers after a delay.
func (s *server) toolsDemo(id json.RawMessage, cancel chan struct{}, ignoreCancel bool, delay time.Duration) {
	tools := []string{"tool-a", "tool-b"}
	if ignoreCancel {
		tools = tools[:1]
	}
	for _, tid := range tools {
		s.update(map[string]any{
			"sessionUpdate": "tool_call", "toolCallId": tid, "title": "Step " + tid,
			"name": tid, "kind": "read", "status": "pending", "rawInput": map[string]any{},
		})
	}
	for _, tid := range tools {
		time.Sleep(delay)
		s.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": tid, "status": "completed"})
		trace("tool_done " + tid)
	}
	if ignoreCancel {
		time.Sleep(800 * time.Millisecond)
		s.respond(id, map[string]any{"stopReason": "end_turn"})
		return
	}
	for range 300 {
		select {
		case <-cancel:
			s.respond(id, map[string]any{"stopReason": "cancelled"})
			return
		case <-time.After(35 * time.Millisecond):
		}
		s.chunk("lorem ")
	}
	s.respond(id, map[string]any{"stopReason": "end_turn"})
}

// noToolsDemo streams text until cancelled, then answers only after a delay
// and sends one more chunk, which must be attributed to this turn.
func (s *server) noToolsDemo(id json.RawMessage, cancel chan struct{}) {
	s.chunk("early ")
	select {
	case <-cancel:
	case <-time.After(10 * time.Second):
	}
	time.Sleep(400 * time.Millisecond)
	s.chunk("late-chunk ")
	s.respond(id, map[string]any{"stopReason": "cancelled"})
}

// permissionDemo waits on a permission request for one tool and answers with
// the stop reason implied by whether the turn was cancelled.
func (s *server) permissionDemo(id json.RawMessage, sessionID string, cancel chan struct{}) {
	s.update(map[string]any{
		"sessionUpdate": "tool_call", "toolCallId": "tool-a", "title": "Step tool-a",
		"name": "tool-a", "kind": "edit", "status": "pending", "rawInput": map[string]any{},
	})
	s.askPermission(sessionID, fakeTool{id: "tool-a", title: "Step tool-a", name: "tool-a", kind: "edit"})
	select {
	case <-cancel:
		s.respond(id, map[string]any{"stopReason": "cancelled"})
	default:
		s.respond(id, map[string]any{"stopReason": "end_turn"})
	}
}

// traceSessionPrompt records the structured lifecycle request for protocol tests.
func traceSessionPrompt(method string, params json.RawMessage) {
	if os.Getenv("FAKE_ACP_TRACE_SESSION_PROMPT") == "1" {
		trace("lifecycle " + method + " " + string(params))
	}
}

// trace appends one line to $FAKE_ACP_TRACE so tests can assert RPC order.
func trace(line string) {
	path := os.Getenv("FAKE_ACP_TRACE")
	if path == "" {
		return
	}
	traceMu.Lock()
	defer traceMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

var traceMu sync.Mutex

func promptText(prompt []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var b strings.Builder
	for _, p := range prompt {
		b.WriteString(p.Text)
	}
	return strings.ReplaceAll(b.String(), "\n", " ")
}

// terminalDemo exercises the ACP terminal lifecycle for integration tests.
func (s *server) terminalDemo(id json.RawMessage, sessionID string, background bool) {
	s.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "Checking the command."}})
	// Re-exec this binary: prints "first" then "second" after a pause and
	// exits 3, giving the terminal pipeline deterministic output on every
	// platform. A shell command like `sh -c` is not portable to Windows.
	exe, err := os.Executable()
	if err != nil {
		s.respond(id, map[string]any{"stopReason": "end_turn"})
		return
	}
	command, args := exe, []string{"--emit-terminal-demo"}
	result := s.callClient("terminal/create", map[string]any{"sessionId": sessionID, "command": command, "args": args, "outputByteLimit": 4})
	var terminal struct {
		TerminalID string `json:"terminalId"`
	}
	if json.Unmarshal(result, &terminal) != nil || terminal.TerminalID == "" {
		s.respond(id, map[string]any{"stopReason": "end_turn"})
		return
	}
	s.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "terminal-demo", "title": "Run demo command", "kind": "execute", "status": "in_progress", "content": []any{map[string]any{"type": "terminal", "terminalId": terminal.TerminalID}}})
	if background {
		s.chunk("Command continues in the background.")
		s.respond(id, map[string]any{"stopReason": "end_turn"})
		go func() {
			s.callClient("terminal/wait_for_exit", map[string]any{"sessionId": sessionID, "terminalId": terminal.TerminalID})
			s.callClient("terminal/release", map[string]any{"sessionId": sessionID, "terminalId": terminal.TerminalID})
		}()
		return
	}
	s.callClient("terminal/wait_for_exit", map[string]any{"sessionId": sessionID, "terminalId": terminal.TerminalID})
	s.callClient("terminal/release", map[string]any{"sessionId": sessionID, "terminalId": terminal.TerminalID})
	s.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "terminal-demo", "status": "failed"})
	s.chunk("Command finished.")
	s.respond(id, map[string]any{"stopReason": "end_turn"})
}

type fakeTool struct {
	id, title, name, kind string
}

// newSessionID mints a unique id per session/new, like a real agent that owns
// its sessions. FAKE_ACP_REUSE_SESSION pins one id for every new session — the
// dishonest mode NusaShell's reuse guard has to catch (an agent backend
// attaching its latest session instead of starting fresh).
func (s *server) newSessionID() string {
	if os.Getenv("FAKE_ACP_REUSE_SESSION") == "1" {
		s.mu.Lock()
		s.current = "fake-session"
		s.mu.Unlock()
		return "fake-session"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionCounter++
	s.current = fmt.Sprintf("fake-session-%d", s.sessionCounter)
	return s.current
}

// sessionID returns the id the latest session call addressed; updates carry
// it so notifications route to the conversation that owns the session.
func (s *server) sessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == "" {
		return "fake-session"
	}
	return s.current
}

func (s *server) sessionResult(sessionID string) map[string]any {
	result := map[string]any{"sessionId": sessionID, "configOptions": s.configOptions()}
	if s.legacy {
		s.mu.Lock()
		current, _ := s.values["mode"].(string)
		s.mu.Unlock()
		result["modes"] = map[string]any{"currentModeId": current, "availableModes": []any{
			map[string]any{"id": "ask", "name": "Ask", "description": "Request permission before making any changes"},
			map[string]any{"id": "accept-edits", "name": "Code", "description": "Write and modify code with full tool access"},
			map[string]any{"id": "bypass", "name": "Bypass permissions", "description": "Runs tools without asking"},
		}}
	}
	return result
}

func (s *server) currentMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	mode, _ := s.values["mode"].(string)
	return mode
}

func (s *server) modeSwitchDemo(id json.RawMessage, sessionID string) {
	s.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "Two tool calls ahead; checking permission for each."}})
	for _, tool := range []fakeTool{
		{id: "fake-tool-1", title: "Read workspace files", name: "read", kind: "read"},
		{id: "fake-tool-2", title: "Write notes", name: "write_notes", kind: "edit"},
	} {
		s.update(map[string]any{
			"sessionUpdate": "tool_call", "toolCallId": tool.id, "title": tool.title,
			"name": tool.name, "kind": tool.kind, "status": "pending",
			"rawInput": map[string]any{"path": "workspace"},
		})
		if s.currentMode() != "bypass" && s.askPermission(sessionID, tool) == "" {
			s.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": tool.id, "status": "failed", "rawOutput": map[string]any{"error": "permission denied"}})
			s.chunk("Permission was denied, so I stopped.")
			s.respond(id, map[string]any{"stopReason": "end_turn"})
			return
		}
		s.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": tool.id, "status": "completed", "rawOutput": map[string]any{"mode": s.currentMode()}})
	}
	s.chunk("Both tools finished.")
	s.respond(id, map[string]any{"stopReason": "end_turn"})
}

func (s *server) callClient(method string, params any) json.RawMessage {
	return s.callClientWait(method, params, 10*time.Second)
}

// callClientWait is callClient for a request a person answers.
func (s *server) callClientWait(method string, params any, wait time.Duration) json.RawMessage {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	ch := make(chan json.RawMessage, 1)
	s.pending[fmt.Sprintf("%d", id)] = ch
	s.mu.Unlock()
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	select {
	case result := <-ch:
		return result
	case <-time.After(wait):
		return nil
	}
}

func (s *server) askPermission(sessionID string, tool fakeTool) string {
	s.mu.Lock()
	s.nextID++
	reqID := s.nextID
	ch := make(chan json.RawMessage, 1)
	s.pending[fmt.Sprintf("%d", reqID)] = ch
	s.mu.Unlock()
	s.write(map[string]any{
		"jsonrpc": "2.0", "id": reqID, "method": "session/request_permission",
		"params": map[string]any{
			"sessionId": sessionID,
			"toolCall": map[string]any{
				"toolCallId": tool.id, "title": tool.title,
				"name": tool.name, "kind": tool.kind, "status": "pending",
				"rawInput": map[string]any{"path": "workspace"},
			},
			"options": []any{
				map[string]any{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
				map[string]any{"optionId": "allow-always", "name": "Always allow reads", "kind": "allow_always"},
				map[string]any{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
			},
		},
	})
	select {
	case raw := <-ch:
		var res struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		if json.Unmarshal(raw, &res) != nil {
			return ""
		}
		if res.Outcome.Outcome != "selected" || strings.HasPrefix(res.Outcome.OptionID, "reject") {
			return ""
		}
		return res.Outcome.OptionID
	case <-time.After(10 * time.Minute):
		return ""
	}
}

func (s *server) reasonsDemo(id json.RawMessage) {
	reasons := []string{"reason one", "reason two", "reason three"}
	for i, reason := range reasons {
		s.update(map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"messageId":     fmt.Sprintf("reason-%d", i+1),
			"content":       map[string]any{"type": "text", "text": reason},
		})
		if i < len(reasons)-1 {
			s.update(map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    fmt.Sprintf("seg-tool-%d", i+1),
				"title":         "Step",
				"name":          "read",
				"kind":          "read",
				"status":        "completed",
			})
			if i == 0 {
				time.Sleep(time.Second)
			}
		}
	}
	s.respond(id, map[string]any{"stopReason": "end_turn"})
}

func (s *server) update(update map[string]any) {
	s.notify("session/update", map[string]any{"sessionId": s.sessionID(), "update": update})
}

func (s *server) chunk(text string) {
	s.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

func (s *server) notify(method string, params any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *server) respond(id json.RawMessage, result any) {
	trace("respond " + string(id))
	s.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *server) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(b)
	_ = s.out.WriteByte('\n')
	_ = s.out.Flush()
}

// mcpDemo handles "mcp-call <server> <tool> <json args>": it launches the
// session's MCP server the way a real agent does, calls one tool, and
// replies with the result text.
func (s *server) mcpDemo(id json.RawMessage, rest string) {
	parts := strings.SplitN(rest, " ", 3)
	reply := func(text string) {
		s.chunk(text)
		s.respond(id, map[string]any{"stopReason": "end_turn"})
	}
	if len(parts) < 2 {
		reply("usage: mcp-call <server> <tool> [json]")
		return
	}
	args := "{}"
	if len(parts) == 3 {
		args = parts[2]
	}
	s.mu.Lock()
	cfg, ok := s.mcp[parts[0]]
	s.mu.Unlock()
	if !ok {
		reply("no MCP server " + parts[0] + " in this session")
		return
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for _, e := range cfg.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}
	cmd.Stderr = os.Stderr
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		reply("mcp start: " + err.Error())
		return
	}
	defer func() { _ = in.Close(); _ = cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := mcp.Connect(ctx, pipe{out, in}, "fake-acp", "0.1.0")
	if err != nil {
		reply("mcp connect: " + err.Error())
		return
	}
	res, err := c.Call(ctx, parts[1], json.RawMessage(args))
	if err != nil {
		reply("mcp call: " + err.Error())
		return
	}
	prefix := "mcp result: "
	if res.IsError {
		prefix = "mcp error: "
	}
	images := 0
	for _, c := range res.Content {
		if c.Type == "image" {
			images++
		}
	}
	reply(fmt.Sprintf("%s%s (images: %d)", prefix, res.Text(), images))
}

type pipe struct {
	io.Reader
	w io.WriteCloser
}

func (p pipe) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p pipe) Close() error                { return p.w.Close() }

// fsDemo writes a file through the client, reads it back, and tries a
// write the client must refuse.
// elicitDemo asks the client for a form (or a URL visit, or a schema the
// card cannot render) and reports the answer it got.
func (s *server) elicitDemo(id json.RawMessage, sessionID string, variant string) {
	params := map[string]any{
		"sessionId": sessionID, "mode": "form", "message": "Which environment should I deploy to?",
		"requestedSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"environment": map[string]any{"type": "string", "title": "Environment", "enum": []string{"staging", "production"}},
				"replicas":    map[string]any{"type": "integer", "title": "Replicas", "minimum": 1, "maximum": 5},
				"notify":      map[string]any{"type": "boolean", "title": "Notify the team"},
				"note":        map[string]any{"type": "string", "title": "Note", "maxLength": 80},
			},
			"required": []string{"environment", "replicas"},
		},
	}
	switch variant {
	case "url":
		params = map[string]any{"sessionId": sessionID, "mode": "url", "message": "Sign in to continue.", "url": "https://example.com/login", "elicitationId": "demo"}
	case "broken":
		// A valid object schema whose property type no card can render —
		// the fallback is the message plus decline.
		params["requestedSchema"] = map[string]any{
			"type":       "object",
			"properties": map[string]any{"pick": map[string]any{"type": "fancypicker", "title": "Pick one"}},
		}
	}
	s.chunk("Asking you first. ")
	raw := s.callClientWait("elicitation/create", params, 10*time.Minute)
	s.chunk("answer: " + string(raw))
	s.respond(id, map[string]any{"stopReason": "end_turn"})
}

func (s *server) fsDemo(id json.RawMessage, sessionID string) {
	s.mu.Lock()
	cwd := s.cwd
	s.mu.Unlock()
	path := filepath.Join(cwd, "notes", "hello.txt")
	s.callClient("fs/write_text_file", map[string]any{"sessionId": sessionID, "path": path, "content": "line one\nline two\n"})
	raw := s.callClient("fs/read_text_file", map[string]any{"sessionId": sessionID, "path": path, "line": 2, "limit": 1})
	var read struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(raw, &read)
	refused := s.callClient("fs/write_text_file", map[string]any{"sessionId": sessionID, "path": filepath.Join(cwd, ".env"), "content": "X=1"}) == nil
	outside := s.callClient("fs/read_text_file", map[string]any{"sessionId": sessionID, "path": "/etc/hostname"}) == nil
	s.chunk(fmt.Sprintf("read: %q env-refused: %v outside-refused: %v", read.Content, refused, outside))
	s.respond(id, map[string]any{"stopReason": "end_turn"})
}
