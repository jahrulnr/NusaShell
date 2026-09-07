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
