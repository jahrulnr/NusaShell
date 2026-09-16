package transport

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthzReturnsCoreIdentity(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	srv := NewWithIdentity(nil, slog.Default(), http.NotFoundHandler(), false, CoreIdentity{
		PID:       123,
		Port:      10994,
		Version:   "test",
		Owner:     "systemd",
		StartedAt: started,
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	// A genuine direct local call carries a loopback Host; IsLoopbackRequest
	// requires both halves.
	req.Host = "127.0.0.1:10994"
	res := httptest.NewRecorder()
	srv.Routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}

	var got struct {
		OK        bool      `json:"ok"`
		Service   string    `json:"service"`
		PID       int       `json:"pid"`
		Port      int       `json:"port"`
		Version   string    `json:"version"`
		Owner     string    `json:"owner"`
		StartedAt time.Time `json:"started_at"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Service != "nusashell-core" || got.PID != 123 || got.Port != 10994 || got.Version != "test" || got.Owner != "systemd" || !got.StartedAt.Equal(started) {
		t.Fatalf("health = %+v", got)
	}
}

// TestHealthz_RemoteMinimalIdentity verifies that a non-loopback caller gets
// only the minimal {ok, service} identity — the full process identity (PID,
// port, version, owner, started-at) must not leak to unpaired remotes even
// though /healthz is a public path.
func TestHealthz_RemoteMinimalIdentity(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	srv := NewWithIdentity(nil, slog.Default(), http.NotFoundHandler(), false, CoreIdentity{
		PID:       123,
		Port:      10994,
		Version:   "test",
		Owner:     "systemd",
		StartedAt: started,
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "203.0.113.7:4321"
	res := httptest.NewRecorder()
	srv.Routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}

	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true || got["service"] != "nusashell-core" {
		t.Fatalf("minimal health = %+v", got)
	}
	for _, k := range []string{"pid", "port", "version", "owner", "started_at"} {
		if _, present := got[k]; present {
			t.Fatalf("remote health leaked %q: %+v", k, got)
		}
	}
}

// TestHealthz_LoopbackPeerWithForwardedHostMinimalIdentity covers the
// host-local forwarder case: the TCP peer is loopback but the request carries a
// public Host, so the caller is remote and must not receive the process
// identity.
func TestHealthz_LoopbackPeerWithForwardedHostMinimalIdentity(t *testing.T) {
	srv := NewWithIdentity(nil, slog.Default(), http.NotFoundHandler(), false, CoreIdentity{
		PID:       123,
		Port:      10994,
		Version:   "test",
		Owner:     "systemd",
		StartedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	req.Host = "nusashell.example.com"
	res := httptest.NewRecorder()
	srv.Routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}

	var got map[string]any
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true || got["service"] != "nusashell-core" {
		t.Fatalf("minimal health = %+v", got)
	}
	for _, k := range []string{"pid", "port", "version", "owner", "started_at"} {
		if _, present := got[k]; present {
			t.Fatalf("forwarded public host leaked %q: %+v", k, got)
		}
	}
}
