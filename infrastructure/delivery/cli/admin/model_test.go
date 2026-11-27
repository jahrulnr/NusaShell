package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func runModel(t *testing.T, srv *httptest.Server, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args = append(args, "--addr", strings.TrimPrefix(srv.URL, "http://"))
	code := Model(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestModelShowBinding(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{"id":"t1","providerId":"gw","modelId":"m1"}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, out, _ := runModel(t, srv, "t1")
	if code != 0 || f.method != "GET" || f.path != "/api/conversations/t1" {
		t.Fatalf("show: %d %s %s", code, f.method, f.path)
	}
	if !strings.Contains(out, "provider: gw") || !strings.Contains(out, "model:    m1") {
		t.Fatalf("show out = %q", out)
	}

	f.reply = `{"id":"t2","agentId":"codex","modelId":"smart"}`
	code, out, _ = runModel(t, srv, "t2")
	if code != 0 || !strings.Contains(out, "agent:    codex") || !strings.Contains(out, "model:    smart") {
		t.Fatalf("agent show: %d %q", code, out)
	}

	code, out, _ = runModel(t, srv, "t2", "--json")
	if code != 0 || !strings.Contains(out, `"agentId":"codex"`) {
		t.Fatalf("json show: %d %q", code, out)
	}
	f.status = 404
	code, _, errOut := runModel(t, srv, "nope")
	if code != 1 || !strings.Contains(errOut, "WhatsApp is not connected") {
		t.Fatalf("server problem: %d %q", code, errOut)
	}
}

func TestModelSelectors(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{"id":"t1","providerId":"gw","modelId":"m1"}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	for _, tc := range []struct {
		selector string
		want     map[string]any
	}{
		{"agent:codex", map[string]any{"agentId": "codex"}},
		{"agent:codex/gpt-5", map[string]any{"agentId": "codex", "modelId": "gpt-5"}},
		{"gw/m1", map[string]any{"providerId": "gw", "modelId": "m1"}},
		{"openrouter/a/b", map[string]any{"providerId": "openrouter", "modelId": "a/b"}},
	} {
		code, _, errOut := runModel(t, srv, "t1", tc.selector)
		if code != 0 {
			t.Fatalf("%s: %d %q", tc.selector, code, errOut)
		}
		if f.method != "PATCH" || f.path != "/api/conversations/t1" {
			t.Fatalf("%s: %s %s", tc.selector, f.method, f.path)
		}
		if len(f.body) != len(tc.want) {
			t.Fatalf("%s: body = %v", tc.selector, f.body)
		}
		for k, v := range tc.want {
			if f.body[k] != v {
				t.Fatalf("%s: body[%q] = %v, want %v (body %v)", tc.selector, k, f.body[k], v, f.body)
			}
		}
	}
}

func TestModelBadSelectors(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	if code, _, _ := runModel(t, srv); code != 2 {
		t.Fatal("no thread must be a usage error")
	}
	for _, sel := range []string{"nonsense", "openai/", "/m1", "agent:", "agent:/m", "a", "agent"} {
		if code, _, _ := runModel(t, srv, "t1", sel); code != 2 {
			t.Fatalf("selector %q: want exit 2", sel)
		}
		if f.method == "PATCH" {
			t.Fatalf("selector %q reached the server", sel)
		}
	}
	if code, _, _ := runModel(t, srv, "t1", "gw/m", "extra"); code != 2 {
		t.Fatal("a third argument must be a usage error")
	}
}

// modelListServer records every path the command asks for, after auth.
type modelListServer struct {
	mu    sync.Mutex
	paths []string
	agent string
	prov  string
	sets  string
	miss  int
}

func (s *modelListServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/auth/status" {
		w.Write([]byte(`{"required":false}`))
		return
	}
	s.mu.Lock()
	s.paths = append(s.paths, r.URL.Path)
	s.mu.Unlock()
	switch r.URL.Path {
	case "/api/agents/cursor/models":
		w.Write([]byte(s.agent))
	case "/api/providers/gw":
		if s.miss != 0 {
			w.WriteHeader(s.miss)
			w.Write([]byte(`{"detail":"provider not found"}`))
			return
		}
		w.Write([]byte(s.prov))
	case "/api/settings":
		w.Write([]byte(s.sets))
	default:
		w.WriteHeader(404)
		w.Write([]byte(`{"detail":"not found"}`))
	}
}

func (s *modelListServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

func runModelList(t *testing.T, srv *httptest.Server, args ...string) (int, string, string) {
	t.Helper()
	return runModel(t, srv, append([]string{"list"}, args...)...)
}

// Unknown is an answer. Rendering it as data:[] or as "no models" says the
// runtime offers nothing, which is a different fact.
func TestModelListUnknownIsNotAnEmptyCatalog(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	body := `{"status":"unknown","source":"cache","data":[],"default":{"id":"grok-4.7","by":"agent"},"reason":"the agent reported no model list"}`
	f := &modelListServer{agent: body, sets: `{}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, out, errOut := runModelList(t, srv, "--agent", "cursor")
	if code != 0 {
		t.Fatalf("unknown is an answer: %d %s", code, errOut)
	}
	if !strings.Contains(out, "unknown") || !strings.Contains(out, "grok-4.7") || !strings.Contains(out, "the agent reported no model list") {
		t.Fatalf("unknown catalog = %q", out)
	}
	if strings.Contains(out, "no models") || strings.TrimSpace(out) == "" {
		t.Fatalf("unknown rendered as an empty list: %q", out)
	}
	if strings.Join(f.seen(), ",") != "/api/agents/cursor/models" {
		t.Fatalf("paths = %v — the command must read the server catalog and nothing else", f.seen())
	}

	code, out, errOut = runModelList(t, srv, "--agent", "cursor", "--json")
	if code != 0 {
		t.Fatalf("json unknown: %d %s", code, errOut)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("json %q: %v", out, err)
	}
	if _, ok := doc["data"]; ok {
		t.Fatalf("unknown json rendered a list: %s", out)
	}
	if string(doc["status"]) != `"unknown"` || !strings.Contains(string(doc["default"]), `"id":"grok-4.7"`) {
		t.Fatalf("json = %s", out)
	}
}

func TestModelListUsageDoesNotCallTheServer(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &modelListServer{agent: `{"status":"known","source":"cache","data":[]}`}
	srv := httptest.NewServer(f)
	defer srv.Close()
	for _, args := range [][]string{
		{},
		{"--agent", "cursor", "--provider", "gw"},
	} {
		f.paths = nil
		code, _, _ := runModelList(t, srv, args...)
		if code != 2 {
			t.Fatalf("%v: want usage exit 2, got %d", args, code)
		}
		if len(f.seen()) != 0 {
			t.Fatalf("%v reached the server: %v", args, f.seen())
		}
	}
}

func TestModelListProviderCatalog(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &modelListServer{
		prov: `{"id":"gw","endpoint":"https://example.test","broken":false,"models":[{"id":"grok-4.7","name":"Grok 4.7"},{"id":"plain","name":""}]}`,
		sets: `{"models":{"threads":{"provider":"gw","model":"grok-4.7"}}}`,
	}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, out, errOut := runModelList(t, srv, "--provider", "gw")
	if code != 0 {
		t.Fatalf("known: %d %s", code, errOut)
	}
	if !strings.Contains(out, "grok-4.7  Grok 4.7  current") || !strings.Contains(out, "plain  plain") {
		t.Fatalf("rows = %q", out)
	}
	if strings.Contains(out, "unknown") || strings.Contains(out, "no models") {
		t.Fatalf("known catalog = %q", out)
	}
	if strings.Join(f.seen(), ",") != "/api/providers/gw,/api/settings" {
		t.Fatalf("paths = %v", f.seen())
	}

	f.paths = nil
	f.sets = `{"models":{"threads":{"provider":"gw","model":"not-in-the-catalog"}}}`
	code, out, errOut = runModelList(t, srv, "--provider", "gw", "--json")
	if code != 0 {
		t.Fatalf("missing default: %d %s", code, errOut)
	}
	if strings.Contains(out, `"current":true`) || strings.Contains(out, `"status":"unknown"`) {
		t.Fatalf("a settings id outside the catalog changed the answer: %s", out)
	}

	f.paths = nil
	f.prov = `{"id":"gw","endpoint":"https://example.test","broken":true,"models":[]}`
	f.sets = `{}`
	code, out, errOut = runModelList(t, srv, "--provider", "gw")
	if code != 0 || !strings.Contains(out, "credential: broken") || !strings.Contains(out, "no models") {
		t.Fatalf("broken known empty: %d %q %s", code, out, errOut)
	}
	if strings.Contains(out, "unknown") {
		t.Fatalf("a broken credential made a decoded catalog unknown: %q", out)
	}

	f.prov = `{"id":"gw","endpoint":"","broken":true,"models":[{"id":"hidden","name":"Hidden"}]}`
	f.sets = `{"models":{"threads":{"provider":"gw","model":"threads-model"}}}`
	code, out, errOut = runModelList(t, srv, "--provider", "gw", "--json")
	if code != 0 {
		t.Fatalf("undecoded: %d %s", code, errOut)
	}
	if strings.Contains(out, `"data"`) || strings.Contains(out, "hidden") || !strings.Contains(out, `"status":"unknown"`) ||
		!strings.Contains(out, "provider record cannot be read") || !strings.Contains(out, `"id":"threads-model"`) ||
		!strings.Contains(out, `"by":"settings"`) {
		t.Fatalf("undecoded catalog = %s", out)
	}

	f.miss = 404
	code, out, errOut = runModelList(t, srv, "--provider", "gw")
	if code == 0 || strings.TrimSpace(out) != "" || !strings.Contains(errOut, "provider not found") {
		t.Fatalf("404: code %d out %q err %q", code, out, errOut)
	}
}
