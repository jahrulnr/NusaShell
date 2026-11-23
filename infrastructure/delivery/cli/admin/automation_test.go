package admin

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func runAutomation(t *testing.T, srv *httptest.Server, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	args = append(args, "--addr", strings.TrimPrefix(srv.URL, "http://"))
	code := Automation(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestAutomationAdd(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{"id":"a1","name":"nightly"}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, _, errOut := runAutomation(t, srv, "add",
		"--name", "nightly", "--prompt", "summarize", "--provider", "gw", "--model", "m1",
		"--cron", "0 9 * * *", "--tz", "Asia/Jakarta", "--thread", "same", "--project", "p1",
		"--notify-channel", "whatsapp", "--notify-target", "123@g.us", "--enabled=false")
	if code != 0 {
		t.Fatalf("add: %d %q", code, errOut)
	}
	if f.method != "POST" || f.path != "/api/automations" {
		t.Fatalf("add: %s %s", f.method, f.path)
	}
	want := map[string]any{"name": "nightly", "prompt": "summarize", "providerId": "gw", "modelId": "m1",
		"schedule": "cron", "cron": "0 9 * * *", "timezone": "Asia/Jakarta", "thread": "same", "projectId": "p1",
		"notify": map[string]any{"channel": "whatsapp", "chatId": "123@g.us"}, "enabled": false}
	if got, _ := json.Marshal(f.body); string(got) != string(must(json.Marshal(want))) {
		t.Fatalf("add body = %s", got)
	}

	// An agent runtime sends agentId alone; --at in --tz's zone reads local time.
	code, _, errOut = runAutomation(t, srv, "add", "--name", "n", "--prompt", "p",
		"--agent", "codex", "--at", "2026-12-05T09:30", "--tz", "Asia/Jakarta")
	if code != 0 {
		t.Fatalf("add agent: %d %q", code, errOut)
	}
	if f.body["agentId"] != "codex" || f.body["providerId"] != nil || f.body["schedule"] != "once" ||
		f.body["runAt"] != "2026-12-05T09:30:00+07:00" {
		t.Fatalf("add agent body = %v", f.body)
	}

	// A local --at without --tz reads the server's zone from the list reply.
	f.reply = `{"data":[],"timezone":"Asia/Jakarta"}`
	code, _, errOut = runAutomation(t, srv, "add", "--name", "n", "--prompt", "p",
		"--provider", "gw", "--model", "m", "--at", "2026-12-05T09:30")
	if code != 0 {
		t.Fatalf("add at: %d %q", code, errOut)
	}
	if f.body["runAt"] != "2026-12-05T09:30:00+07:00" {
		t.Fatalf("add at body = %v", f.body)
	}
}

func TestAutomationAddUsageErrors(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	for _, args := range [][]string{
		{"add", "--prompt", "p", "--provider", "gw", "--model", "m", "--cron", "* * * * *"},                    // no name
		{"add", "--name", "n", "--provider", "gw", "--model", "m", "--cron", "* * * * *"},                      // no prompt
		{"add", "--name", "n", "--prompt", "p", "--cron", "* * * * *"},                                         // no runtime
		{"add", "--name", "n", "--prompt", "p", "--agent", "a", "--provider", "g", "--cron", "x"},              // both runtimes
		{"add", "--name", "n", "--prompt", "p", "--provider", "gw", "--cron", "x"},                             // no model
		{"add", "--name", "n", "--prompt", "p", "--provider", "gw", "--model", "m"},                            // no schedule
		{"add", "--name", "n", "--prompt", "p", "--agent", "a", "--cron", "x", "--at", "t"},                    // two schedules
		{"add", "--name", "n", "--prompt", "p", "--agent", "a", "--cron", "x", "--tz", "Mars/Olympus"},         // bad zone
		{"add", "--name", "n", "--prompt", "p", "--agent", "a", "--cron", "x", "--notify-channel", "whatsapp"}, // half notify
	} {
		if code, _, _ := runAutomation(t, srv, args...); code != 2 {
			t.Fatalf("add %v: want exit 2, got %d", args, code)
		}
	}
}

func TestAutomationSetMergesOntoCurrent(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	current := `{"id":"a1","name":"old","prompt":"old prompt","agentId":"bot","schedule":"cron","cron":"0 9 * * *",` +
		`"timezone":"Asia/Jakarta","thread":"new","expiresAt":"2027-01-01T00:00:00+07:00","enabled":true,` +
		`"notify":{"channel":"whatsapp","chatId":"1@g.us","thread":"t"},` +
		`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-11-01T00:00:00Z"}`
	f := &fakeServer{reply: current}
	srv := httptest.NewServer(f)
	defer srv.Close()

	// Only the given flags change; updatedAt rides along as the conflict token.
	code, _, errOut := runAutomation(t, srv, "set", "a1", "--name", "new")
	if code != 0 {
		t.Fatalf("set: %d %q", code, errOut)
	}
	if f.method != "PUT" || f.path != "/api/automations/a1" {
		t.Fatalf("set: %s %s", f.method, f.path)
	}
	want := map[string]any{"name": "new", "prompt": "old prompt", "agentId": "bot", "schedule": "cron",
		"cron": "0 9 * * *", "timezone": "Asia/Jakarta", "thread": "new", "enabled": true,
		"expiresAt": "2027-01-01T00:00:00+07:00",
		"notify":    map[string]any{"channel": "whatsapp", "chatId": "1@g.us", "thread": "t"},
		"updatedAt": "2026-11-01T00:00:00Z"}
	if got, _ := json.Marshal(f.body); string(got) != string(must(json.Marshal(want))) {
		t.Fatalf("set body = %s", got)
	}

	// --at reads in the automation's own zone and turns the schedule once.
	code, _, errOut = runAutomation(t, srv, "set", "a1", "--at", "2026-12-05T09:30")
	if code != 0 {
		t.Fatalf("set at: %d %q", code, errOut)
	}
	if f.path != "/api/automations/a1" || f.body["schedule"] != "once" || f.body["runAt"] != "2026-12-05T09:30:00+07:00" {
		t.Fatalf("set at body = %v", f.body)
	}

	// A provider route replaces the agent; expires "" clears; notify "" clears.
	code, _, errOut = runAutomation(t, srv, "set", "a1", "--provider", "gw", "--model", "openrouter/auto",
		"--expires", "", "--notify-channel", "")
	if code != 0 {
		t.Fatalf("set provider: %d %q", code, errOut)
	}
	if f.body["agentId"] != "" || f.body["providerId"] != "gw" || f.body["modelId"] != "openrouter/auto" {
		t.Fatalf("set provider body = %v", f.body)
	}
	if v, ok := f.body["expiresAt"]; !ok || v != nil {
		t.Fatalf("expiresAt = %v (want explicit null)", f.body["expiresAt"])
	}
	if v, ok := f.body["notify"]; !ok || v != nil {
		t.Fatalf("notify = %v (want explicit null)", f.body["notify"])
	}

	if code, _, _ := runAutomation(t, srv, "set", "a1"); code != 2 {
		t.Fatal("set without flags must be a usage error")
	}
	if code, _, _ := runAutomation(t, srv, "set", "a1", "--thread", "forever"); code != 2 {
		t.Fatal("a bad --thread must be a usage error")
	}
	// Conflicting runtimes and bad zones fail before the server is called.
	if code, _, _ := runAutomation(t, srv, "set", "a1", "--agent", "x", "--provider", "gw"); code != 2 {
		t.Fatal("set with --agent and --provider must be a usage error")
	}
	if code, _, _ := runAutomation(t, srv, "set", "a1", "--tz", "Mars/Olympus"); code != 2 {
		t.Fatal("a bad --tz must be a usage error")
	}
}

func TestAutomationOps(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{"id":"a1"}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, out, _ := runAutomation(t, srv, "pause", "a1")
	if code != 0 || f.method != "PATCH" || f.path != "/api/automations/a1" || f.body["enabled"] != false ||
		!strings.Contains(out, "paused automation a1") {
		t.Fatalf("pause: %d %q %s %s %v", code, out, f.method, f.path, f.body)
	}
	code, out, _ = runAutomation(t, srv, "resume", "a1")
	if code != 0 || f.body["enabled"] != true || !strings.Contains(out, "resumed automation a1") {
		t.Fatalf("resume: %d %q %v", code, out, f.body)
	}
	code, out, _ = runAutomation(t, srv, "remove", "a1")
	if code != 0 || f.method != "DELETE" || !strings.Contains(out, "removed automation a1") {
		t.Fatalf("remove: %d %q %s", code, out, f.method)
	}
	code, _, _ = runAutomation(t, srv, "get", "a1")
	if code != 0 || f.method != "GET" || f.path != "/api/automations/a1" {
		t.Fatalf("get: %d %s %s", code, f.method, f.path)
	}

	f.reply = `{"id":"r9","scheduledAt":"2026-11-24T09:00:00Z","startedAt":"2026-11-24T09:00:01Z","status":"running"}`
	code, out, _ = runAutomation(t, srv, "run", "a1")
	if code != 0 || f.method != "POST" || f.path != "/api/automations/a1/runs" || !strings.Contains(out, "run r9 running") {
		t.Fatalf("run: %d %q %s %s", code, out, f.method, f.path)
	}

	f.reply = `{"data":[{"id":"r9","scheduledAt":"2026-11-24T09:00:00Z","status":"completed","note":"","conversationId":"c1"}]}`
	code, out, _ = runAutomation(t, srv, "history", "a1")
	if code != 0 || f.method != "GET" || f.path != "/api/automations/a1/runs" ||
		!strings.Contains(out, "r9") || !strings.Contains(out, "completed") || !strings.Contains(out, "c1") {
		t.Fatalf("history: %d %q %s %s", code, out, f.method, f.path)
	}
	f.reply = `{"data":[]}`
	code, out, _ = runAutomation(t, srv, "history", "a1")
	if code != 0 || !strings.Contains(out, "no runs") {
		t.Fatalf("empty history: %d %q", code, out)
	}

	f.status = 404
	code, _, errOut := runAutomation(t, srv, "get", "nope")
	if code != 1 || !strings.Contains(errOut, "WhatsApp is not connected") {
		t.Fatalf("server problem: %d %q", code, errOut)
	}
}

func TestAutomationList(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{"data":[{"id":"a1","name":"nightly","enabled":true,"schedule":"cron","cron":"0 9 * * *",` +
		`"timezone":"Asia/Jakarta","nextRunAt":"2026-11-25T09:00:00+07:00"},` +
		`{"id":"a2","name":"once","enabled":false,"schedule":"once","runAt":"2026-12-01T09:00:00Z","timezone":"UTC"}],` +
		`"timezone":"Asia/Jakarta"}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	code, out, errOut := runAutomation(t, srv, "list")
	if code != 0 {
		t.Fatalf("list: %d %q", code, errOut)
	}
	for _, want := range []string{"a1", "nightly", "cron 0 9 * * *", "(Asia/Jakarta)", "2026-11-25T09:00:00+07:00", "a2", "once", "-"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
	code, out, _ = runAutomation(t, srv, "list", "--json")
	if code != 0 || !strings.Contains(out, `"data":[`) || !strings.Contains(out, `"id":"a1"`) ||
		!strings.Contains(out, `"timezone":"Asia/Jakarta"`) {
		t.Fatalf("list --json: %d %q", code, out)
	}
}

func TestAutomationCommandErrors(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	f := &fakeServer{reply: `{}`}
	srv := httptest.NewServer(f)
	defer srv.Close()

	if code, _, _ := runAutomation(t, srv); code != 2 {
		t.Fatal("no command must be a usage error")
	}
	if code, _, _ := runAutomation(t, srv, "frobnicate"); code != 2 {
		t.Fatal("unknown command must be a usage error")
	}
	if code, _, _ := runAutomation(t, srv, "pause"); code != 2 {
		t.Fatal("pause without id must be a usage error")
	}
	if code, _, _ := runAutomation(t, srv, "get", "a1", "extra"); code != 2 {
		t.Fatal("unexpected positional must be a usage error")
	}
}
