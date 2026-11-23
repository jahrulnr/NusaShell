package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeMCP records what the CLI posted to /api/mcp/conversation and answers
// with result.
type fakeMCP struct {
	mu     sync.Mutex
	method string
	path   string
	auth   string
	origin string
	tool   string
	args   map[string]any
	status int
	result string
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.method, f.path = r.Method, r.URL.Path
	f.auth, f.origin = r.Header.Get("Authorization"), r.Header.Get("Origin")
	raw, _ := io.ReadAll(r.Body)
	f.tool, f.args = "", nil
	var body struct {
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
	}
	if json.Unmarshal(raw, &body) == nil {
		f.tool, f.args = body.Tool, body.Arguments
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		w.Write([]byte(`{"title":"Unauthorized","detail":"unknown thread token"}`))
		return
	}
	w.Write([]byte(f.result))
}

func runConversations(t *testing.T, srv *httptest.Server, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args = append(args, "--url", srv.URL, "--token", "tok-1")
	code := Conversations(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestConversationsCallShape(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeMCP{result: `{"content":[{"type":"text","text":"{\"ok\":true}"}]}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, out, errOut := runConversations(t, srv, "send", "c1", "hello", "world")
	if code != 0 {
		t.Fatalf("send: %d %q", code, errOut)
	}
	if f.method != "POST" || f.path != "/api/mcp/conversation" {
		t.Fatalf("send: %s %s", f.method, f.path)
	}
	if f.auth != "Bearer tok-1" || f.origin != srv.URL {
		t.Fatalf("headers: auth %q origin %q", f.auth, f.origin)
	}
	if f.tool != "send" || f.args["conversationId"] != "c1" || f.args["text"] != "hello world" {
		t.Fatalf("send args = %v", f.args)
	}
	if !strings.Contains(out, `{"ok":true}`) {
		t.Fatalf("send out = %q", out)
	}

	code, _, _ = runConversations(t, srv, "send", "c2", "--text", "via flag")
	if code != 0 || f.args["text"] != "via flag" {
		t.Fatalf("send --text args = %v", f.args)
	}
}

func TestConversationsTools(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeMCP{result: `{"content":[{"type":"text","text":"done"}]}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, _, _ := runConversations(t, srv, "list", "--query", "q", "--project", "p", "--limit", "5", "--offset", "2")
	if code != 0 || f.tool != "list" || f.args["query"] != "q" || f.args["project"] != "p" ||
		f.args["limit"] != float64(5) || f.args["offset"] != float64(2) {
		t.Fatalf("list: %d %s %v", code, f.tool, f.args)
	}
	code, _, _ = runConversations(t, srv, "projects", "--query", "web")
	if code != 0 || f.tool != "projects" || f.args["query"] != "web" {
		t.Fatalf("projects: %d %s %v", code, f.tool, f.args)
	}
	code, _, _ = runConversations(t, srv, "search", "find", "this", "--project", "p", "--kinds", "text,tool", "--limit", "3")
	if code != 0 || f.tool != "search" || f.args["query"] != "find this" || f.args["project"] != "p" ||
		f.args["limit"] != float64(3) {
		t.Fatalf("search: %d %s %v", code, f.tool, f.args)
	}
	kinds, _ := f.args["kinds"].([]any)
	if len(kinds) != 2 || kinds[0] != "text" || kinds[1] != "tool" {
		t.Fatalf("search kinds = %v", f.args["kinds"])
	}
	code, _, _ = runConversations(t, srv, "search", "find", "--conversation", "c1")
	if code != 0 || f.args["conversationId"] != "c1" {
		t.Fatalf("search --conversation: %d %v", code, f.args)
	}
	code, _, _ = runConversations(t, srv, "search", "find", "--kinds", ",")
	if code != 0 || f.args["kinds"] != nil {
		t.Fatalf("empty --kinds must be omitted: %d %v", code, f.args)
	}
	code, _, _ = runConversations(t, srv, "export", "--conversation", "c1", "--include-tool-output")
	if code != 0 || f.tool != "export" || f.args["conversationId"] != "c1" || f.args["includeToolOutput"] != true {
		t.Fatalf("export: %d %s %v", code, f.tool, f.args)
	}
	code, _, _ = runConversations(t, srv, "read", "c2", "--after-seq", "3", "--before-seq", "9", "--limit", "5", "--include-tool-data")
	if code != 0 || f.tool != "read" || f.args["conversationId"] != "c2" || f.args["afterSeq"] != float64(3) ||
		f.args["beforeSeq"] != float64(9) || f.args["limit"] != float64(5) || f.args["includeToolData"] != true {
		t.Fatalf("read: %d %s %v", code, f.tool, f.args)
	}
	// An explicit --after-seq 0 differs from an absent one.
	code, _, _ = runConversations(t, srv, "read", "c2", "--after-seq", "0")
	v, ok := f.args["afterSeq"]
	if code != 0 || !ok || v != float64(0) {
		t.Fatalf("read --after-seq 0: %d %v", code, f.args)
	}
	code, _, _ = runConversations(t, srv, "wait", "c2", "--after-seq", "4", "--timeout", "10")
	if code != 0 || f.tool != "wait" || f.args["afterSeq"] != float64(4) || f.args["timeoutSeconds"] != float64(10) {
		t.Fatalf("wait: %d %s %v", code, f.tool, f.args)
	}
	code, _, _ = runConversations(t, srv, "stop", "c3")
	if code != 0 || f.tool != "stop" || f.args["conversationId"] != "c3" {
		t.Fatalf("stop: %d %s %v", code, f.tool, f.args)
	}
	code, _, _ = runConversations(t, srv, "spawn", "do", "stuff", "--title", "T", "--agent", "codex", "--project", "p1")
	if code != 0 || f.tool != "spawn" || f.args["prompt"] != "do stuff" || f.args["title"] != "T" ||
		f.args["agent"] != "codex" || f.args["project"] != "p1" {
		t.Fatalf("spawn: %d %s %v", code, f.tool, f.args)
	}
	code, _, _ = runConversations(t, srv, "report", "half", "done")
	if code != 0 || f.tool != "report" || f.args["text"] != "half done" {
		t.Fatalf("report: %d %s %v", code, f.tool, f.args)
	}
}

func TestConversationsResult(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeMCP{result: `{"content":[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	// Text blocks join with newlines.
	code, out, _ := runConversations(t, srv, "stop", "c1")
	if code != 0 || out != "line1\nline2\n" {
		t.Fatalf("text join: %d %q", code, out)
	}
	// --json prints the raw result.
	code, out, _ = runConversations(t, srv, "stop", "c1", "--json")
	if code != 0 || strings.TrimSpace(out) != f.result {
		t.Fatalf("json: %d %q", code, out)
	}
	// An error result is stderr + exit 1.
	f.result = `{"content":[{"type":"text","text":"that thread is busy"}],"isError":true}`
	code, _, errOut := runConversations(t, srv, "send", "c1", "hi")
	if code != 1 || !strings.Contains(errOut, "that thread is busy") {
		t.Fatalf("isError: %d %q", code, errOut)
	}
	// An HTTP problem surfaces its detail.
	f.status = 401
	code, _, errOut = runConversations(t, srv, "stop", "c1")
	if code != 1 || !strings.Contains(errOut, "unknown thread token") {
		t.Fatalf("problem: %d %q", code, errOut)
	}
}

func TestConversationsMissingToken(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("NUSASHELL_MCP_URL", "")
	t.Setenv("NUSASHELL_MCP_TOKEN", "")
	t.Setenv("NUSASHELL_ADDR", "")
	var out, errb bytes.Buffer
	code := Conversations([]string{"stop", "c1"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "NUSASHELL_MCP_URL") || !strings.Contains(errb.String(), "NUSASHELL_MCP_TOKEN") ||
		!strings.Contains(errb.String(), "mcp run") {
		t.Fatalf("missing token: %d %q", code, errb.String())
	}
}

func TestConversationsEnv(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeMCP{result: `{"content":[{"type":"text","text":"ok"}]}`}
	srv := httptest.NewServer(f)
	defer srv.Close()
	t.Setenv("NUSASHELL_MCP_URL", srv.URL)
	t.Setenv("NUSASHELL_MCP_TOKEN", "env-tok")
	t.Setenv("NUSASHELL_ADDR", "")

	var out, errb bytes.Buffer
	code := Conversations([]string{"stop", "c1"}, &out, &errb)
	if code != 0 || f.auth != "Bearer env-tok" {
		t.Fatalf("env: %d %q %q", code, errb.String(), f.auth)
	}

	// NUSASHELL_ADDR (what the owner's terminal exports) supplies the URL.
	t.Setenv("NUSASHELL_MCP_URL", "")
	t.Setenv("NUSASHELL_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	code = Conversations([]string{"stop", "c1"}, &out, &errb)
	if code != 0 || f.auth != "Bearer env-tok" {
		t.Fatalf("addr env: %d %q %q", code, errb.String(), f.auth)
	}
}

func TestConversationsUsageErrors(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeMCP{result: `{"content":[]}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	for _, args := range [][]string{
		{"wat"},
		{"send"},                              // no id
		{"send", "c1"},                        // no text
		{"send", "c1", "hi", "--text", "dup"}, // both texts
		{"stop"},                              // no id
		{"stop", "c1", "extra"},               // extra positional
		{"read"},                              // no id
		{"search"},                            // no query
		{"search", "q", "--conversation", "c1", "--project", "p"}, // both scopes
		{"search", "q", "--limit", "-3"},                          // negative limit
		{"list", "--offset", "-1"},                                // negative offset
		{"wait", "c1", "--timeout", "-5"},                         // negative timeout
		{"export"},                                                // nothing to export
		{"export", "--conversation", "c1", "--project", "p"},      // both
		{"spawn"}, // no prompt
		{"spawn", "p", "--agent", "a", "--provider", "g"}, // both runtimes
		{"spawn", "p", "--provider", "g"},                 // no model
		{"report"},                                        // no text
	} {
		if code, _, _ := runConversations(t, srv, args...); code != 2 {
			t.Fatalf("%v: want exit 2", args)
		}
	}
}
