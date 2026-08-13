package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/frontend"
	"nusashell/infrastructure/ai"
	"nusashell/infrastructure/docs"
	"nusashell/infrastructure/jsonstore"
	"nusashell/infrastructure/mcpclient"
	"nusashell/infrastructure/sqlitestore"
	"nusashell/infrastructure/tools"

	"nhooyr.io/websocket"
)

// ---- fake LLM server ----

// llmStep is one scripted provider response step.
type llmStep struct {
	Text      string
	Reasoning string // thinking content (reasoning_content / reasoning_text / thinking_delta)
	Tool      *llmToolCall
}

type llmToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

type fakeFailure struct {
	Status  int
	Body    string
	Headers http.Header
}

// fakeLLM serves both OpenAI-compatible and Anthropic wire formats from one
// server, switching on the request path. Streaming requests consume one
// round from the scripts queue; when the queue is empty the provider returns
// an empty completion.
type fakeLLM struct {
	*httptest.Server

	mu               sync.Mutex
	models           []string
	scripts          [][]llmStep
	complete         llmStep // non-streaming response (compaction, ping)
	seen             []map[string]any
	seenBodies       []map[string]any
	delay            time.Duration
	failStatus       int
	failBody         string
	failModelsStatus int
	failures         []fakeFailure
	truncateOpenAI   bool
}

func newFakeLLM(t *testing.T) *fakeLLM {
	t.Helper()
	f := &fakeLLM{
		models: []string{"fake-model-1", "fake-model-2"},
		scripts: [][]llmStep{
			{{Text: "Hello from fake LLM"}},
		},
		complete: llmStep{Text: "complete reply"},
	}
	srv := httptest.NewServer(f)
	f.Server = srv
	t.Cleanup(srv.Close)
	return f
}

// setScript sets a single streaming round.
func (f *fakeLLM) setScript(steps []llmStep) {
	f.setRounds([][]llmStep{steps})
}

// setRounds sets per-request streaming scripts; each request consumes one.
func (f *fakeLLM) setRounds(rounds [][]llmStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = rounds
}

// popScript returns the next streaming round, or an empty one when exhausted.
func (f *fakeLLM) popScript() []llmStep {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.scripts) == 0 {
		return nil
	}
	next := f.scripts[0]
	f.scripts = f.scripts[1:]
	return next
}

func (f *fakeLLM) setComplete(step llmStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.complete = step
}

func (f *fakeLLM) failOnce(status int, headers http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, fakeFailure{Status: status, Headers: headers})
}

func (f *fakeLLM) truncateNextOpenAIStream() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.truncateOpenAI = true
}

func (f *fakeLLM) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

// completeRequestCount returns the number of non-streaming (Complete) requests
// received so far, safe to call from test goroutines.
func (f *fakeLLM) completeRequestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, body := range f.seenBodies {
		if body != nil && body["stream"] != true {
			count++
		}
	}
	return count
}

func (f *fakeLLM) lastSeenPath() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) == 0 {
		return ""
	}
	if p, ok := f.seen[len(f.seen)-1]["path"].(string); ok {
		return p
	}
	return ""
}

func (f *fakeLLM) lastBody() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seenBodies) == 0 {
		return nil
	}
	return f.seenBodies[len(f.seenBodies)-1]
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.seen = append(f.seen, map[string]any{"method": r.Method, "path": r.URL.Path, "headers": r.Header})
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)
	f.seenBodies = append(f.seenBodies, parsed)
	var failure fakeFailure
	if len(f.failures) > 0 {
		failure = f.failures[0]
		f.failures = f.failures[1:]
	} else if f.failStatus != 0 {
		failure = fakeFailure{Status: f.failStatus, Body: f.failBody}
	}
	if failure.Status != 0 {
		f.mu.Unlock()
		for key, values := range failure.Headers {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(failure.Status)
		if failure.Body != "" {
			fmt.Fprint(w, failure.Body)
		} else {
			fmt.Fprint(w, `{"error":{"message":"simulated failure"}}`)
		}
		return
	}
	delay := f.delay
	complete := f.complete
	models := f.models
	failModelsStatus := f.failModelsStatus
	failBody := f.failBody
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}

	streaming := false
	if r.Method == http.MethodPost {
		var probe struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &probe)
		streaming = probe.Stream
	}
	truncateOpenAI := false
	if streaming && strings.HasSuffix(r.URL.Path, "/chat/completions") {
		f.mu.Lock()
		truncateOpenAI = f.truncateOpenAI
		f.truncateOpenAI = false
		f.mu.Unlock()
	}

	var script []llmStep
	if streaming {
		script = f.popScript()
	}

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
		if failModelsStatus != 0 {
			w.WriteHeader(failModelsStatus)
			if failBody != "" {
				fmt.Fprint(w, failBody)
			} else {
				fmt.Fprint(w, `{"error":{"message":"models endpoint not found"}}`)
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": mapModels(models)})
	case strings.HasSuffix(r.URL.Path, "/responses"):
		f.serveResponses(w, r, body, script, complete)
	case strings.HasSuffix(r.URL.Path, "/chat/completions"):
		f.serveOpenAI(w, r, body, script, complete, truncateOpenAI)
	case strings.HasSuffix(r.URL.Path, "/v1/messages"):
		f.serveAnthropic(w, r, body, script, complete)
	default:
		http.NotFound(w, r)
	}
}

func mapModels(models []string) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]any{"id": m})
	}
	return out
}

// ---- OpenAI-compatible wire ----

func (f *fakeLLM) serveOpenAI(w http.ResponseWriter, r *http.Request, body []byte, script []llmStep, complete llmStep, truncate bool) {
	var req struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)
	if !req.Stream {
		writeJSON(w, http.StatusOK, map[string]any{
			"choices": []any{map[string]any{
				"message":       openAIMessageFor(complete),
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	writeSSEFrame := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	for _, step := range script {
		chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}}}}
		if step.Text != "" {
			chunk["choices"].([]any)[0].(map[string]any)["delta"] = map[string]any{"content": step.Text}
		}
		if step.Reasoning != "" {
			chunk["choices"].([]any)[0].(map[string]any)["delta"] = map[string]any{"reasoning_content": step.Reasoning}
		}
		if step.Tool != nil {
			args, _ := json.Marshal(step.Tool.Args)
			chunk["choices"].([]any)[0].(map[string]any)["delta"] = map[string]any{
				"tool_calls": []any{map[string]any{
					"index": 0,
					"id":    step.Tool.ID,
					"type":  "function",
					"function": map[string]any{
						"name":      step.Tool.Name,
						"arguments": string(args),
					},
				}},
			}
		}
		writeSSEFrame(chunk)
		if truncate {
			return
		}
	}
	writeSSEFrame(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
	})
	fmt.Fprintf(w, "data: [DONE]\n\n")
	fl.Flush()
}

func openAIMessageFor(step llmStep) map[string]any {
	if step.Tool != nil {
		args, _ := json.Marshal(step.Tool.Args)
		return map[string]any{
			"content": step.Text,
			"tool_calls": []any{map[string]any{
				"id":   step.Tool.ID,
				"type": "function",
				"function": map[string]any{
					"name":      step.Tool.Name,
					"arguments": string(args),
				},
			}},
		}
	}
	return map[string]any{"content": step.Text}
}

// ---- Responses wire ----

func (f *fakeLLM) serveResponses(w http.ResponseWriter, r *http.Request, body []byte, script []llmStep, complete llmStep) {
	var req struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)
	if !req.Stream {
		var output []any
		if complete.Text != "" {
			output = append(output, map[string]any{
				"type":    "message",
				"content": []any{map[string]any{"type": "output_text", "text": complete.Text}},
			})
		}
		if complete.Tool != nil {
			args, _ := json.Marshal(complete.Tool.Args)
			output = append(output, map[string]any{
				"type": "function_call", "call_id": complete.Tool.ID, "name": complete.Tool.Name, "arguments": string(args),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"output": output,
			"usage": map[string]any{
				"input_tokens": 7, "output_tokens": 3,
				"input_tokens_details": map[string]any{"cached_tokens": 2},
			},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	writeEvent := func(typ string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
		fl.Flush()
	}
	outputIndex := 0
	for _, step := range script {
		if step.Text != "" {
			writeEvent("response.output_text.delta", map[string]any{
				"type": "response.output_text.delta", "output_index": outputIndex, "delta": step.Text,
			})
			outputIndex++
		}
		if step.Tool != nil {
			writeEvent("response.output_item.added", map[string]any{
				"type": "response.output_item.added", "output_index": outputIndex,
				"item": map[string]any{"type": "function_call", "call_id": step.Tool.ID, "name": step.Tool.Name, "arguments": map[string]any{}},
			})
			args, _ := json.Marshal(step.Tool.Args)
			writeEvent("response.function_call_arguments.delta", map[string]any{
				"type": "response.function_call_arguments.delta", "output_index": outputIndex, "delta": string(args),
			})
			outputIndex++
		}
	}
	writeEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"usage": map[string]any{
				"input_tokens": 7, "output_tokens": 3,
				"input_tokens_details": map[string]any{"cached_tokens": 2},
			},
		},
	})
	// gateways terminate with the chat-completions sentinel
	fmt.Fprintf(w, "data: [DONE]\n\n")
	fl.Flush()
}

// addResponsesProvider registers a provider of the Responses kind.
func (h *harness) addResponsesProvider(t *testing.T, name string) string {
	t.Helper()
	res := h.rpcOK(t, "ai.providers.save", map[string]any{
		"kind":     "responses",
		"name":     name,
		"base_url": h.llm.URL + "/v1",
		"api_key":  "test-key-" + name,
		"enabled":  true,
	})
	var out struct {
		Providers []struct {
			ID string `json:"id"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil || len(out.Providers) == 0 {
		t.Fatalf("save provider result malformed: %s (%v)", res.Result, err)
	}
	return out.Providers[0].ID
}

// ---- Anthropic wire ----

func (f *fakeLLM) serveAnthropic(w http.ResponseWriter, r *http.Request, body []byte, script []llmStep, complete llmStep) {
	var req struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)
	if !req.Stream {
		content := []any{}
		if complete.Text != "" {
			content = append(content, map[string]any{"type": "text", "text": complete.Text})
		}
		if complete.Tool != nil {
			args, _ := json.Marshal(complete.Tool.Args)
			content = append(content, map[string]any{
				"type": "tool_use", "id": complete.Tool.ID, "name": complete.Tool.Name, "input": json.RawMessage(args),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"content": content,
			"usage": map[string]any{
				"input_tokens": 10, "output_tokens": 5,
				"cache_creation_input_tokens": 8, "cache_read_input_tokens": 4,
			},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	writeEvent := func(typ string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
		fl.Flush()
	}
	writeEvent("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 0, "cache_creation_input_tokens": 8, "cache_read_input_tokens": 4},
	}})
	blockIndex := 0
	for _, step := range script {
		if step.Text != "" {
			writeEvent("content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{"type": "text", "text": ""}})
			writeEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "text_delta", "text": step.Text}})
			writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
			blockIndex++
		}
		if step.Tool != nil {
			writeEvent("content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{"type": "tool_use", "id": step.Tool.ID, "name": step.Tool.Name, "input": map[string]any{}}})
			args, _ := json.Marshal(step.Tool.Args)
			writeEvent("content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(args)}})
			writeEvent("content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
			blockIndex++
		}
	}
	writeEvent("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 5}})
	writeEvent("message_stop", map[string]any{"type": "message_stop"})
}

// ---- harness ----

type harness struct {
	t      *testing.T
	app    *application.App
	server *httptest.Server
	store  *jsonstore.Store
	creds  *sqlitestore.CredentialStore
	llm    *fakeLLM
	mcpBin string
}

var fakemcpBin string

func TestMain(m *testing.M) {
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "find module root:", err)
		os.Exit(1)
	}
	bin, err := os.CreateTemp("", "fakemcp-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin.Close()
	os.Remove(bin.Name())
	fakemcpBin = bin.Name()
	if runtime.GOOS == "windows" {
		fakemcpBin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", fakemcpBin, filepath.Join(root, "testdata", "fakemcp"))
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build fakemcp: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.Remove(fakemcpBin)
	os.Exit(code)
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

func newHarness(t *testing.T, llm *fakeLLM) *harness {
	t.Helper()
	if llm == nil {
		llm = newFakeLLM(t)
	}
	dataDir := t.TempDir()
	store, err := jsonstore.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := sqlitestore.NewCredentials(filepath.Join(dataDir, "credentials.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { creds.Close() })

	docSource, err := docs.New("")
	if err != nil {
		t.Fatal(err)
	}
	mcpManager := mcpclient.NewManager()
	bus := application.NewBus()
	app := application.NewApp(application.Deps{
		Version:       "test",
		DataDir:       dataDir,
		Conversations: store,
		Providers:     &jsonstore.Providers{S: store},
		Credentials:   creds,
		Skills:        &jsonstore.Skills{S: store},
		Memory:        &jsonstore.Memory{S: store},
		MCP:           &jsonstore.MCP{S: store},
		Logs:          &jsonstore.Logs{S: store},
		Settings:      &jsonstore.Settings{S: store},
		Docs:          docSource,
		Bus:           bus,
		Toolbox: &tools.Toolbox{
			Skills:     &jsonstore.Skills{S: store},
			Memory:     &jsonstore.Memory{S: store},
			Docs:       docSource,
			MCPServers: &jsonstore.MCP{S: store},
			MCP:        mcpManager,
		},
		MCPToolbox: mcpManager,
		Factory:    ai.Factory,
		RetrySleeper: func(context.Context, time.Duration) error {
			return nil
		},
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(app, logger, StaticHandler(frontend.FS, false), false)
	httpSrv := httptest.NewServer(srv.Routes())
	t.Cleanup(httpSrv.Close)
	return &harness{t: t, app: app, server: httpSrv, store: store, creds: creds, llm: llm, mcpBin: fakemcpBin}
}

// rpc performs a POST /rpc call and decodes the envelope.
func (h *harness) rpc(t *testing.T, method string, payload any) contractsResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"method": method, "payload": payload})
	resp, err := http.Post(h.server.URL+"/rpc", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("rpc %s: %v", method, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out contractsResult
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("rpc %s: bad envelope %s: %v", method, b, err)
	}
	return out
}

func (h *harness) rpcOK(t *testing.T, method string, payload any) contractsResult {
	t.Helper()
	res := h.rpc(t, method, payload)
	if !res.OK {
		t.Fatalf("rpc %s failed: %+v", method, res.Error)
	}
	return res
}

type contractsResult struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (h *harness) addOpenAIProvider(t *testing.T, name string) string {
	t.Helper()
	res := h.rpcOK(t, "ai.providers.save", map[string]any{
		"kind":     "chat",
		"name":     name,
		"base_url": h.llm.URL + "/v1",
		"api_key":  "test-key-" + name,
		"enabled":  true,
	})
	var out struct {
		Providers []struct {
			ID string `json:"id"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil || len(out.Providers) == 0 {
		t.Fatalf("save provider result malformed: %s (%v)", res.Result, err)
	}
	return out.Providers[0].ID
}

func (h *harness) addAnthropicProvider(t *testing.T, name string) string {
	t.Helper()
	res := h.rpcOK(t, "ai.providers.save", map[string]any{
		"kind":     "messages",
		"name":     name,
		"base_url": h.llm.URL,
		"api_key":  "test-key-" + name,
		"enabled":  true,
	})
	var out struct {
		Providers []struct {
			ID string `json:"id"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil || len(out.Providers) == 0 {
		t.Fatalf("save provider result malformed: %s (%v)", res.Result, err)
	}
	return out.Providers[0].ID
}

func (h *harness) newConversation(t *testing.T) string {
	t.Helper()
	res := h.rpcOK(t, "agent.conversations.create", map[string]any{})
	var out struct {
		Conversation struct {
			ID string `json:"id"`
		} `json:"conversation"`
	}
	if err := json.Unmarshal(res.Result, &out); err != nil || out.Conversation.ID == "" {
		t.Fatalf("create conversation malformed: %s", res.Result)
	}
	return out.Conversation.ID
}

// readSSEUntil reads SSE frames from a stream until a type matches.
func readSSEUntil(t *testing.T, ctx context.Context, url string, wantType string) ([]map[string]any, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sse status %d", resp.StatusCode)
	}
	var frames []map[string]any
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			var ev struct {
				Type    string          `json:"type"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal([]byte(data.String()), &ev); err != nil {
				return frames, err
			}
			data.Reset()
			var payload map[string]any
			_ = json.Unmarshal(ev.Payload, &payload)
			frames = append(frames, map[string]any{"type": ev.Type, "payload": payload})
			if ev.Type == wantType {
				return frames, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			data.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	if err := scanner.Err(); err != nil {
		return frames, err
	}
	return frames, fmt.Errorf("stream ended before %s", wantType)
}

// readWSUntil connects to the /ws endpoint, subscribes, and reads frames
// until a message with type == wantType arrives. It returns all frames seen.
func readWSUntil(ctx context.Context, url string, wantType string) ([]map[string]any, error) {
	wsURL := "ws" + strings.TrimPrefix(url, "http")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	var frames []map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return frames, err
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if _, ok := msg["id"]; ok {
			continue // rpc reply, not an event
		}
		typ, _ := msg["type"].(string)
		frames = append(frames, msg)
		if typ == wantType {
			return frames, nil
		}
	}
	return frames, fmt.Errorf("stream ended before %s", wantType)
}
