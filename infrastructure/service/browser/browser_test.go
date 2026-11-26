package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

const testPage = `<!doctype html><title>Form</title>
<input id="name" aria-label="Name">
<button onclick="document.title='clicked ' + document.getElementById('name').value">Go</button>
<a href="/next">Next</a>`

func TestSession(t *testing.T) {
	if _, _, err := resolveExec("", ""); err != nil {
		t.Skip("no Chrome or Chromium on this host")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/next" {
			_, _ = w.Write([]byte(`<title>Next</title><p>second page</p>`))
			return
		}
		_, _ = w.Write([]byte(testPage))
	}))
	defer srv.Close()

	m := New(Options{ProfileDir: t.TempDir()})
	defer m.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := m.Open(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := m.Open(ctx, "c1"); again != s {
		t.Fatal("a second Open must return the same session")
	}
	if err := s.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if p := s.Page(); p.Title != "Form" || !strings.HasPrefix(p.URL, srv.URL) {
		t.Fatalf("page = %+v", p)
	}
	tree, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(pattern string) string {
		m := regexp.MustCompile(pattern + `.*\[ref=(e\d+)\]`).FindStringSubmatch(tree)
		if m == nil {
			t.Fatalf("no %s in snapshot:\n%s", pattern, tree)
		}
		return m[1]
	}
	if ok, err := s.Focus(ctx, ref(`textbox "Name"`)); err != nil || !ok {
		t.Fatalf("focus = %v, %v", ok, err)
	}
	if err := s.Type(ctx, "Ada"); err != nil {
		t.Fatal(err)
	}
	x, y, err := s.Point(ctx, ref(`button "Go"`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Click(ctx, x, y, "", 1); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Eval(ctx, "document.title")
	if err != nil {
		t.Fatal(err)
	}
	var title string
	_ = json.Unmarshal(raw, &title)
	if title != "clicked Ada" {
		t.Fatalf("title after click = %q", title)
	}
	if _, _, err := s.Point(ctx, "e999"); err != ErrRefNotFound {
		t.Fatalf("stale ref = %v", err)
	}

	if err := s.Navigate(ctx, srv.URL+"/next"); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitText(ctx, "second page", false); err != nil {
		t.Fatal(err)
	}
	if err := s.History(ctx, -1); err != nil {
		t.Fatal(err)
	}
	// The back-forward cache restores the page as it was left.
	if p := s.Page(); p.URL != srv.URL+"/" || p.Title != "clicked Ada" {
		t.Fatalf("after back: %+v", p)
	}
	shot, err := s.Screenshot(ctx)
	if err != nil || len(shot) < 100 || shot[0] != 0xFF {
		t.Fatalf("screenshot: %d bytes, %v", len(shot), err)
	}

	watchCtx, stopWatch := context.WithCancel(ctx)
	frames := s.Watch(watchCtx)
	if f := <-frames; f.State == nil || f.State.Page.URL != srv.URL+"/" || f.State.Profile != "default" {
		t.Fatalf("first watch frame = %+v", f.State)
	}
	if f := <-frames; len(f.JPEG) == 0 {
		t.Fatal("a viewer starts from a screenshot")
	}

	// Lease: a takeover ends the agent's lock, and viewers hear of it.
	s.SetAgentLock(true)
	s.Takeover("v1")
	if l := s.Lease(); l.AgentLock || l.Controller != "v1" {
		t.Fatalf("lease after takeover = %+v", l)
	}
	for f := range frames {
		if f.State != nil && f.State.Lease.Controller == "v1" {
			break
		}
	}
	stopWatch()

	// A phone profile: a mobile viewport whose clicks are taps.
	if err := s.UpdateDevice(ctx, func(dservice.BrowserDevice) (dservice.BrowserDevice, error) {
		return dservice.BrowserDevice{Preset: "mobile", Width: 390, Height: 844, DPR: 3, Mobile: true, Touch: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err = s.Eval(ctx, "[screen.width, navigator.maxTouchPoints > 0]")
	if err != nil || string(raw) != "[390,true]" {
		t.Fatalf("emulated viewport = %s, %v", raw, err)
	}

	// Another profile reopens the page there.
	moved, err := m.SwitchProfile(ctx, "c1", "work")
	if err != nil {
		t.Fatal(err)
	}
	if moved.Profile() != "work" || moved.Page().URL != srv.URL+"/" || moved.Device().Width != 390 {
		t.Fatalf("switched session = %s %+v %+v", moved.Profile(), moved.Page(), moved.Device())
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the old session did not end")
	}
	if got := m.Profiles(); len(got) != 2 || got[0] != "default" || got[1] != "work" {
		t.Fatalf("profiles = %v", got)
	}
	s = moved.(*Session)

	m.Close("c1")
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("closed session did not end")
	}
	if _, ok := m.Session("c1"); ok {
		t.Fatal("closed session still listed")
	}
}

func TestNetworkTools(t *testing.T) {
	if _, _, err := resolveExec("", ""); err != nil {
		t.Skip("no Chrome or Chromium on this host")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/api/data":
			_, _ = w.Write([]byte(`{"real":true}`))
		default:
			_, _ = w.Write([]byte(`<title>Net</title><script>window.seen = window.stubbed || false;</script>`))
		}
	}))
	defer srv.Close()
	m := New(Options{ProfileDir: t.TempDir()})
	defer m.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, err := m.Open(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	s := sess.(*Session)

	if _, err := s.AddRoute(RouteRule{Pattern: "**/api/data", Action: RouteFulfill, Body: `{"fake":1}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRoute(RouteRule{Pattern: "**/blocked", Action: RouteAbort, ErrorReason: "failed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRoute(RouteRule{Pattern: "[", Regex: true, Action: RouteAbort}); err == nil {
		t.Fatal("a bad regex must be refused")
	}
	if _, err := s.AddInitScript(ctx, "window.stubbed = 'yes'", "stub", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	eval := func(expr string) string {
		raw, err := s.Eval(ctx, expr)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if got := eval("window.seen"); got != `"yes"` {
		t.Fatalf("init script ran before the page: seen = %s", got)
	}
	if got := eval("fetch('/api/data').then(r => r.text())"); got != `"{\"fake\":1}"` {
		t.Fatalf("fulfilled fetch = %s", got)
	}
	if got := eval("fetch('/blocked').then(() => 'ok', () => 'failed')"); got != `"failed"` {
		t.Fatalf("aborted fetch = %s", got)
	}
	if routes := s.Routes(); len(routes) != 2 || routes[0].Hits != 1 {
		t.Fatalf("routes = %+v", routes)
	}
	var sawData, sawFail bool
	for _, e := range s.NetLog() {
		sawData = sawData || (strings.HasSuffix(e.URL, "/api/data") && e.Status == 200)
		sawFail = sawFail || (strings.HasSuffix(e.URL, "/blocked") && e.Error != "")
	}
	if !sawData || !sawFail {
		t.Fatalf("network log = %+v", s.NetLog())
	}
	if n := s.ClearRoutes(""); n != 2 {
		t.Fatalf("cleared %d routes", n)
	}
	if got := eval("fetch('/api/data').then(r => r.text())"); got != `"{\"real\":true}"` {
		t.Fatalf("fetch after clearing = %s", got)
	}

	// A WebSocket route rewrites where the page connects; ws.url keeps the
	// original.
	if _, err := s.AddWSRoute(ctx, WSRouteRule{Pattern: "ws://example.invalid/**", Target: "ws://127.0.0.1:9/"}, true); err != nil {
		t.Fatal(err)
	}
	if got := eval("new WebSocket('ws://example.invalid/hub').url"); got != `"ws://example.invalid/hub"` {
		t.Fatalf("routed socket url = %s", got)
	}
	if n, err := s.ClearWSRoutes(ctx, false); err != nil || n != 1 {
		t.Fatalf("clear ws routes = %d, %v", n, err)
	}
}

func TestTypeSecret(t *testing.T) {
	if _, _, err := resolveExec("", ""); err != nil {
		t.Skip("no Chrome or Chromium on this host")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<title>Login</title><input aria-label="Password" type="password" maxlength="8" value="old">`))
	}))
	defer srv.Close()
	m := New(Options{ProfileDir: t.TempDir()})
	defer m.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, _ := m.Open(ctx, "c1")
	if err := sess.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	tree, _ := sess.Snapshot(ctx)
	ref := regexp.MustCompile(`textbox "Password".*\[ref=(e\d+)\]`).FindStringSubmatch(tree)
	if ref == nil {
		t.Fatalf("no password box in %s", tree)
	}
	if err := sess.TypeSecret(ctx, ref[1], "hunter2", true); err != nil {
		t.Fatal(err)
	}
	raw, _ := sess.Eval(ctx, "document.querySelector('input').value")
	if string(raw) != `"hunter2"` {
		t.Fatalf("value = %s", raw)
	}
	if err := sess.TypeSecret(ctx, ref[1], "far-too-long", true); err == nil {
		t.Fatal("a secret longer than maxlength must be refused")
	}
}

// Building the manager — what serve does at boot — starts nothing. A browser
// waits for the first tool call that needs it; an idle instance holds no Chrome
// process at all.
func TestConstructionStartsNoBrowser(t *testing.T) {
	if _, _, err := resolveExec("", ""); err != nil {
		t.Skip("no Chrome or Chromium on this host")
	}
	m := New(Options{ProfileDir: t.TempDir()})
	defer m.Shutdown()
	// Long enough for a launch that a boot-time warm would have started: a cold
	// Chrome takes a few hundred milliseconds, so a short window would pass even
	// with the regression in place.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		running := len(m.browsers)
		m.mu.Unlock()
		if running != 0 {
			t.Fatalf("boot started %d browser(s), want none until a tool needs one", running)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
