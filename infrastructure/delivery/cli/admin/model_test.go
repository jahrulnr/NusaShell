package admin

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
