package transport

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nusashell/contracts"
)

// TestClientIP_ProxyAware verifies the safe trusted-proxy rule for deriving
// the effective client IP. A loopback reverse proxy may forward the real
// client via X-Forwarded-For; a public peer's forwarded headers are ignored.
func TestClientIP_ProxyAware(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"direct loopback ipv4", "127.0.0.1:1234", "", "127.0.0.1"},
		{"direct loopback ipv6", "[::1]:1234", "", "::1"},
		{"direct public", "8.8.8.8:1234", "", "8.8.8.8"},
		{"loopback proxy forwarding public client", "127.0.0.1:1234", "203.0.113.7", "203.0.113.7"},
		// With an append-style proxy the client controls every entry except
		// the rightmost (which the trusted loopback proxy appended from the
		// real TCP peer). Spoofed loopback entries must not win.
		{"client-spoofed loopback xff entry ignored", "127.0.0.1:1234", "127.0.0.1, 203.0.113.7", "203.0.113.7"},
		{"client-spoofed xff chain ignored", "127.0.0.1:1234", "10.9.9.9, 127.0.0.1, 203.0.113.7", "203.0.113.7"},
		{"loopback proxy multiple appended hops", "127.0.0.1:1234", "198.51.100.1, 203.0.113.7", "203.0.113.7"},
		{"trailing empty xff entry skipped", "127.0.0.1:1234", "203.0.113.7, ", "203.0.113.7"},
		{"public peer forwarded headers ignored", "8.8.8.8:1234", "127.0.0.1", "8.8.8.8"},
		{"loopback proxy empty xff ignored", "127.0.0.1:1234", "  ", "127.0.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			got := ClientIP(req)
			if got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthMiddleware_RemoteAccessDisabled(t *testing.T) {
	srv := &Server{Logger: testLogger(), mux: http.NewServeMux()}
	srv.mux.HandleFunc("POST /rpc/test", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called while remote access is disabled")
	})
	req := httptest.NewRequest(http.MethodPost, "/rpc/test", nil)
	req.RemoteAddr = "192.168.1.100:54321"
	res := httptest.NewRecorder()
	srv.AuthMiddleware(srv.mux).ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.Code)
	}
	var body contracts.Response
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error == nil || body.Error.Code != contracts.CodeRemoteAccessDisabled {
		t.Fatalf("error = %+v, want REMOTE_ACCESS_DISABLED", body.Error)
	}
}

func TestIsRemoteAccessSettingsChange(t *testing.T) {
	enabled := true
	payload, err := json.Marshal(contracts.SettingsSetRequest{RemoteAccessEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if !IsRemoteAccessSettingsChange(contracts.MethodSettingsSet, payload) {
		t.Fatal("remote_access_enabled should be recognized as a host-only change")
	}
	if IsRemoteAccessSettingsChange(contracts.MethodSettingsGet, payload) {
		t.Fatal("settings.get must not be treated as a host-only change")
	}
	if IsRemoteAccessSettingsChange(contracts.MethodSettingsSet, []byte("{")) {
		t.Fatal("malformed payload must not be classified as a host-only change")
	}
}

// TestIsLoopbackRequest_ProxyAware verifies that a loopback reverse proxy
// forwarding an external client does NOT grant loopback bypass, while direct
// loopback access remains loopback.
func TestIsLoopbackRequest_ProxyAware(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       bool
	}{
		{"direct loopback", "127.0.0.1:1234", "", true},
		{"direct loopback ipv6", "[::1]:1234", "", true},
		{"direct public", "8.8.8.8:1234", "", false},
		{"loopback proxy forwarding public", "127.0.0.1:1234", "203.0.113.7", false},
		{"spoofed loopback prefix behind proxy", "127.0.0.1:1234", "127.0.0.1, 203.0.113.7", false},
		{"public peer claiming loopback via xff", "8.8.8.8:1234", "127.0.0.1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remoteAddr
			// This table is about the client-IP rule only, so every case
			// carries a local Host; the Host requirement itself is covered by
			// TestIsLoopbackRequest (pairing_test.go) and
			// TestAuthMiddleware_AlienHostOnLoopbackPeerRequiresPairing.
			req.Host = "127.0.0.1:10994"
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := IsLoopbackRequest(req); got != tc.want {
				t.Fatalf("IsLoopbackRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAuthMiddleware_AlienHostOnLoopbackPeerRequiresPairing verifies the
// fail-closed rule for host-local tunnels and proxies: when the TCP peer is
// loopback but the request carries a non-loopback Host, the caller is treated
// as remote, so the pairing gate still applies. Without this rule a local L4
// forwarder that forwards the public Host and injects no X-Forwarded-For made
// every remote client look like a local caller, which bypassed pairing for all
// protected routes.
func TestAuthMiddleware_AlienHostOnLoopbackPeerRequiresPairing(t *testing.T) {
	t.Run("pairing required", func(t *testing.T) {
		srv, _ := newPairingServer(t)
		called := false
		srv.mux.HandleFunc("POST /rpc/test", func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})
		req := loopbackRequest("POST", "/rpc/test", nil)
		req.Host = "nusashell.example.com"
		res := httptest.NewRecorder()
		srv.AuthMiddleware(srv.mux).ServeHTTP(res, req)
		if called {
			t.Fatal("a public Host on a loopback peer must not bypass pairing")
		}
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", res.Code)
		}
		var body contracts.Response
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Error == nil || body.Error.Code != contracts.CodePairingRequired {
			t.Fatalf("error = %+v, want PAIRING_REQUIRED", body.Error)
		}
	})
	t.Run("remote access disabled stays closed", func(t *testing.T) {
		srv := &Server{Logger: testLogger(), mux: http.NewServeMux()}
		called := false
		srv.mux.HandleFunc("POST /rpc/test", func(w http.ResponseWriter, r *http.Request) {
			called = true
		})
		req := loopbackRequest("POST", "/rpc/test", nil)
		req.Host = "nusashell.example.com"
		res := httptest.NewRecorder()
		srv.AuthMiddleware(srv.mux).ServeHTTP(res, req)
		if called {
			t.Fatal("a public Host on a loopback peer must not reach a protected handler")
		}
		if res.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", res.Code)
		}
		var body contracts.Response
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Error == nil || body.Error.Code != contracts.CodeRemoteAccessDisabled {
			t.Fatalf("error = %+v, want REMOTE_ACCESS_DISABLED", body.Error)
		}
	})
}

// TestSetSessionCookie_SecureBehavior verifies the Secure flag is set only
// when the request is over HTTPS, including behind a trusted loopback TLS
// proxy (X-Forwarded-Proto: https from a loopback peer). A public peer's
// X-Forwarded-Proto must not enable Secure.
func TestSetSessionCookie_SecureBehavior(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		tls        bool
		xfp        string
		wantSecure bool
	}{
		{"direct http loopback", "127.0.0.1:1234", false, "", false},
		{"direct https loopback", "127.0.0.1:1234", true, "", true},
		{"direct http public", "8.8.8.8:1234", false, "", false},
		{"loopback proxy http", "127.0.0.1:1234", false, "http", false},
		{"loopback proxy https termination", "127.0.0.1:1234", false, "https", true},
		{"public peer claiming https via xfp ignored", "8.8.8.8:1234", false, "https", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			rec := httptest.NewRecorder()
			SetSessionCookie(rec, req, "tok", time.Now().Add(30*24*time.Hour))
			cookies := rec.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("want 1 cookie, got %d", len(cookies))
			}
			if cookies[0].Secure != tc.wantSecure {
				t.Fatalf("Secure = %v, want %v", cookies[0].Secure, tc.wantSecure)
			}
			if !cookies[0].HttpOnly {
				t.Fatal("cookie must be HttpOnly")
			}
		})
	}
}

// TestRemoteWSOriginAllowed verifies the same-origin browser Origin policy for
// authenticated remote WebSocket upgrades: the origin scheme + host must match
// the effective request scheme/host. Same-origin (direct and proxy) and
// missing Origin are allowed; cross-origin and scheme-mismatch are rejected.
func TestRemoteWSOriginAllowed(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		origin     string
		tls        bool
		remoteAddr string
		xfp        string
		want       bool
	}{
		// Non-browser clients (no Origin) are always allowed.
		{"missing origin (non-browser)", "example.com", "", false, "", "", true},
		// Same-origin direct HTTP: scheme + host match.
		{"same origin http direct", "example.com", "http://example.com", false, "", "", true},
		// Different scheme same host: https origin on http request is rejected.
		{"https origin on http request", "example.com", "https://example.com", false, "", "", false},
		// Different scheme same host: http origin on https request is rejected.
		{"http origin on https request", "example.com", "http://example.com", true, "", "", false},
		// Same-origin direct HTTPS (TLS): scheme + host match.
		{"same origin https direct TLS", "example.com", "https://example.com", true, "", "", true},
		// Same-origin via loopback TLS proxy: X-Forwarded-Proto: https.
		{"same origin https via proxy", "example.com", "https://example.com", false, "127.0.0.1:1234", "https", true},
		// Public peer claiming https via X-Forwarded-Proto is NOT trusted.
		{"public peer xfp https ignored", "example.com", "https://example.com", false, "8.8.8.8:1234", "https", false},
		// Same-origin with port (HTTPS direct).
		{"same origin https with port direct TLS", "example.com:8443", "https://example.com:8443", true, "", "", true},
		// Same-origin with port (HTTP direct).
		{"same origin http with port direct", "example.com:8080", "http://example.com:8080", false, "", "", true},
		// Cross-origin browser: different host.
		{"cross origin browser", "example.com", "https://attacker.com", true, "", "", false},
		// Cross-origin same host different port.
		{"cross origin same host different port", "example.com:8443", "https://example.com:9000", true, "", "", false},
		// Malformed origin.
		{"malformed origin", "example.com", "://", false, "", "", false},
		// Empty host origin.
		{"empty host origin", "example.com", "data:text/html,foo", false, "", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws", nil)
			req.Host = tc.host
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.remoteAddr != "" {
				req.RemoteAddr = tc.remoteAddr
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := remoteWSOriginAllowed(req); got != tc.want {
				t.Fatalf("remoteWSOriginAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}
