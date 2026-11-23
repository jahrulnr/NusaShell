package browser_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	reporecording "github.com/jahrulnr/nusashell/infrastructure/repository/recording"
	"github.com/jahrulnr/nusashell/infrastructure/service/browser"
)

// A recording catches the page's own events (clicks, typing), navigations,
// and the summary written when it stops.
func TestRecording(t *testing.T) {
	m := browser.New(browser.Options{ProfileDir: t.TempDir()})
	defer m.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<title>Rec</title><button id="b" style="width:200px;height:80px" onclick="location.href='/next'">Press</button><input id="i">`))
	}))
	defer srv.Close()
	s, err := m.Open(ctx, "c1")
	if err != nil {
		t.Skip("no browser:", err)
	}
	if err := s.Navigate(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	frames := s.Watch(watchCtx)
	go func() {
		for range frames {
		}
	}()
	time.Sleep(2 * time.Second)
	store := reporecording.NewStore(t.TempDir(), nil)
	w, err := store.Start("c1/default", "c1", "default", "", func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	s.Record(w)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("starting a recording blocked for %s", d)
	}
	time.Sleep(2 * time.Second)
	// The click navigates away at once: it must still be recorded.
	_ = s.Press(ctx, 100, 40, "left", 1, true)
	_ = s.Press(ctx, 100, 40, "left", 1, false)
	time.Sleep(1500 * time.Millisecond)
	meta, err := w.Stop()
	if err != nil {
		t.Fatal(err)
	}
	r, _ := store.Get(meta.ID)
	events, _, _ := r.Events(0, 100, nil)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Origin+":"+e.Type)
	}
	if !strings.Contains(strings.Join(kinds, " "), "dom:click") {
		t.Fatalf("events = %v", kinds)
	}
}
