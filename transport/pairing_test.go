package transport

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/infrastructure/sqlitestore"
)

// newPairingServer creates a test server with pairing enabled. Returns the
// server and the pairing service for direct manipulation in tests.
func newPairingServer(t *testing.T) (*Server, *application.PairingService) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := sqlitestore.NewPairingStore(dataDir + "/pairing.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc := application.NewPairingService(store)
	// Inject a fixed clock for deterministic expiry tests.
	fixedNow := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixedNow })
	srv := &Server{
		Pairing: svc,
		Logger:  testLogger(),
		mux:     http.NewServeMux(),
	}
	srv.registerPairingRoutes(srv.mux)
	srv.mux.HandleFunc("POST /rpc/{method...}", srv.handleRPC)
	srv.mux.HandleFunc("GET /healthz", srv.handleHealth)
	return srv, svc
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// remoteRequest creates a request with a non-loopback RemoteAddr.
func remoteRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "192.168.1.100:54321"
	return req
}

// loopbackRequest creates a request with a loopback RemoteAddr and a loopback
// Host, i.e. a genuine direct local call. Both halves matter: IsLoopbackRequest
// requires a loopback client IP and a loopback Host, so httptest's default
// "example.com" Host would represent a forwarded public request instead.
func loopbackRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "127.0.0.1:10994"
	return req
}

func TestAuthMiddleware_LoopbackBypass(t *testing.T) {
	srv, _ := newPairingServer(t)
	called := false
	srv.mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	req := loopbackRequest("GET", "/test", nil)
	res := httptest.NewRecorder()
	srv.AuthMiddleware(srv.mux).ServeHTTP(res, req)
	if !called {
		t.Fatal("loopback request should bypass auth")
	}
}

func TestPairingRoutes_RemoteAccessDisabled(t *testing.T) {
	srv := &Server{Logger: testLogger(), mux: http.NewServeMux()}
	srv.registerPairingRoutes(srv.mux)
	req := remoteRequest(http.MethodGet, "/pairing/status?challenge_id=pair_x", nil)
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
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

func TestAuthMiddleware_RemoteDenied(t *testing.T) {
	srv, _ := newPairingServer(t)
	srv.mux.HandleFunc("POST /rpc/test", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called")
	})
	req := remoteRequest("POST", "/rpc/test", nil)
	res := httptest.NewRecorder()
	srv.AuthMiddleware(srv.mux).ServeHTTP(res, req)
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
}

func TestAuthMiddleware_PublicPathBypass(t *testing.T) {
	srv, _ := newPairingServer(t)
	called := false
	srv.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	req := remoteRequest("GET", "/index.html", nil)
	res := httptest.NewRecorder()
	srv.AuthMiddleware(srv.mux).ServeHTTP(res, req)
	if !called {
		t.Fatal("public static path should bypass auth")
	}
}

func TestAuthMiddleware_RemoteWithValidSession(t *testing.T) {
	srv, svc := newPairingServer(t)
	// Create a challenge and exchange it for a session.
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, "Test Device"); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	exchangeRes, rpcErr := svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}

	called := false
	srv.mux.HandleFunc("POST /rpc/test", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	req := remoteRequest("POST", "/rpc/test", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: exchangeRes.Token})
	res := httptest.NewRecorder()
	srv.AuthMiddleware(srv.mux).ServeHTTP(res, req)
	if !called {
		t.Fatal("remote request with valid session should be allowed")
	}
}

func TestAuthMiddleware_RemoteWithInvalidSession(t *testing.T) {
	srv, _ := newPairingServer(t)
	srv.mux.HandleFunc("POST /rpc/test", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("protected handler should not be called")
	})
	req := remoteRequest("POST", "/rpc/test", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "invalid-token"})
	res := httptest.NewRecorder()
	srv.AuthMiddleware(srv.mux).ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.Code)
	}
}

func TestPairingChallengeCreate_Loopback(t *testing.T) {
	srv, svc := newPairingServer(t)
	// Challenge creation is loopback-only via RPC (no public route).
	res, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatalf("create challenge failed: %v", rpcErr)
	}
	if res.ChallengeID == "" || res.Code == "" || res.ExpiresIn <= 0 {
		t.Fatalf("invalid create result: %+v", res)
	}
	// The public /pairing/challenge route must NOT exist (host-driven flow).
	req := remoteRequest("POST", "/pairing/challenge", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	srv.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("public /pairing/challenge should be gone, got %d", rr.Code)
	}
}

func TestPairingChallengeCreate_RemoteDeniedViaRPC(t *testing.T) {
	srv, _ := newPairingServer(t)
	// A remote caller cannot create a challenge via RPC: management is local-only.
	body := strings.NewReader(`{}`)
	req := remoteRequest("POST", "/rpc/pairing.challenge.create", body)
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.Code)
	}
	var resp contracts.Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != contracts.CodePairingUnauthorized {
		t.Fatalf("error = %+v, want PAIRING_UNAUTHORIZED", resp.Error)
	}
}

func TestPairingChallengeCreate_RateLimit(t *testing.T) {
	_, svc := newPairingServer(t)
	// Create 5 challenges (the limit) from the same remote source via the
	// local service (simulating the public create path before it was removed
	// from the public route; creation is now loopback-only via RPC).
	for i := 0; i < 5; i++ {
		_, rpcErr := svc.CreateChallenge("192.168.1.100")
		if rpcErr != nil {
			t.Fatalf("challenge %d should succeed: %v", i, rpcErr)
		}
	}
	// 6th should be rate-limited.
	_, rpcErr := svc.CreateChallenge("192.168.1.100")
	if rpcErr == nil {
		t.Fatal("6th challenge should be rate-limited")
	}
	if rpcErr.Code != contracts.CodePairingRateLimit {
		t.Fatalf("error code = %q, want PAIRING_RATE_LIMITED", rpcErr.Code)
	}
}

func TestPairingStatus_PollChallenge(t *testing.T) {
	srv, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	req := httptest.NewRequest("GET", "/pairing/status?challenge_id="+createRes.ChallengeID, nil)
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	var resp contracts.Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("status failed: %+v", resp.Error)
	}
	var result contracts.PairingChallengeStatusResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "pending" {
		t.Fatalf("state = %q, want pending", result.State)
	}
}

// TestPairingStatus_ExpiresInUsesServiceClock verifies that GetChallengeStatus
// computes ExpiresIn from the injected service clock, not the wall clock, so
// deterministic tests and production state agree.
func TestPairingStatus_ExpiresInUsesServiceClock(t *testing.T) {
	srv, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// The service clock is fixed at 2026-01-02 03:04:05 UTC and the TTL is
	// 5 minutes, so ExpiresIn must be exactly 300 seconds.
	req := httptest.NewRequest("GET", "/pairing/status?challenge_id="+createRes.ChallengeID, nil)
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	var resp contracts.Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("status failed: %+v", resp.Error)
	}
	var result contracts.PairingChallengeStatusResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.ExpiresIn != 300 {
		t.Fatalf("ExpiresIn = %d, want 300 (injected clock)", result.ExpiresIn)
	}
}

func TestPairingExchange_FullFlow(t *testing.T) {
	srv, svc := newPairingServer(t)
	// 1. Create challenge (loopback-only).
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// 2. Approve it.
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, "My Phone"); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// 3. Exchange via the public route.
	exchangeBody, _ := json.Marshal(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
		DeviceLabel: "My Phone",
	})
	req := httptest.NewRequest("POST", "/pairing/exchange", strings.NewReader(string(exchangeBody)))
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	var resp contracts.Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("exchange failed: %+v", resp.Error)
	}
	// The token MUST NOT be serialized into the public JSON body.
	var raw map[string]any
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		t.Fatal(err)
	}
	if _, hasToken := raw["token"]; hasToken {
		t.Fatalf("token must not appear in the JSON body, got: %s", resp.Result)
	}
	// Cookie should be set with the opaque token.
	var cookieValue string
	for _, c := range res.Result().Cookies() {
		if c.Name == SessionCookieName && c.Value != "" {
			cookieValue = c.Value
		}
	}
	if cookieValue == "" {
		t.Fatal("session cookie should be set with a non-empty token")
	}
}

func TestPairingExchange_RejectedChallenge(t *testing.T) {
	srv, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.RejectChallenge(createRes.ChallengeID); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	exchangeBody, _ := json.Marshal(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	})
	req := httptest.NewRequest("POST", "/pairing/exchange", strings.NewReader(string(exchangeBody)))
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	var resp contracts.Response
	_ = json.NewDecoder(res.Body).Decode(&resp)
	if resp.OK {
		t.Fatal("rejected challenge should not be exchangeable")
	}
	if resp.Error == nil || resp.Error.Code != contracts.CodePairingRejected {
		t.Fatalf("error = %+v, want PAIRING_REJECTED", resp.Error)
	}
}

func TestPairingExchange_PendingChallenge(t *testing.T) {
	srv, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	exchangeBody, _ := json.Marshal(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	})
	req := httptest.NewRequest("POST", "/pairing/exchange", strings.NewReader(string(exchangeBody)))
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	var resp contracts.Response
	_ = json.NewDecoder(res.Body).Decode(&resp)
	if resp.OK {
		t.Fatal("pending challenge should not be exchangeable")
	}
	if resp.Error == nil || resp.Error.Code != contracts.CodePairingRequired {
		t.Fatalf("error = %+v, want PAIRING_REQUIRED", resp.Error)
	}
}

func TestPairingExchange_InvalidCode(t *testing.T) {
	srv, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, ""); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	exchangeBody, _ := json.Marshal(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        "WRONGCODE",
	})
	req := httptest.NewRequest("POST", "/pairing/exchange", strings.NewReader(string(exchangeBody)))
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	var resp contracts.Response
	_ = json.NewDecoder(res.Body).Decode(&resp)
	if resp.OK {
		t.Fatal("invalid code should not be exchangeable")
	}
}

func TestPairingExchange_AlreadyUsed(t *testing.T) {
	_, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, ""); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// First exchange succeeds.
	_, rpcErr = svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// Second exchange should fail.
	_, rpcErr = svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, "")
	if rpcErr == nil {
		t.Fatal("second exchange should fail")
	}
	if rpcErr.Code != contracts.CodePairingUsed {
		t.Fatalf("error code = %q, want PAIRING_ALREADY_USED", rpcErr.Code)
	}
}

func TestPairingExchange_NotFound(t *testing.T) {
	srv, _ := newPairingServer(t)
	exchangeBody, _ := json.Marshal(contracts.PairingChallengeExchangeRequest{
		ChallengeID: "pair_nonexistent",
		Code:        "ABCD1234",
	})
	req := httptest.NewRequest("POST", "/pairing/exchange", strings.NewReader(string(exchangeBody)))
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	var resp contracts.Response
	_ = json.NewDecoder(res.Body).Decode(&resp)
	if resp.OK {
		t.Fatal("nonexistent challenge should not be exchangeable")
	}
	if resp.Error == nil || resp.Error.Code != contracts.CodePairingNotFound {
		t.Fatalf("error = %+v, want PAIRING_NOT_FOUND", resp.Error)
	}
}

// TestPairingExchange_OriginCheck verifies the public exchange route only
// accepts browser requests whose Origin matches the request host. A
// cross-origin page (or an opaque "null" origin from a sandboxed/file page)
// must not be able to plant a session cookie in the victim's browser, while a
// same-origin request still reaches the service and non-browser callers
// without an Origin header keep working.
func TestPairingExchange_OriginCheck(t *testing.T) {
	exchangeBody, _ := json.Marshal(contracts.PairingChallengeExchangeRequest{
		ChallengeID: "pair_nonexistent",
		Code:        "ABCD1234",
	})

	rejected := []struct {
		name   string
		origin string
	}{
		{"cross-origin page", "https://evil.example"},
		{"cross-origin with port on loopback", "http://127.0.0.1:3000"},
		{"opaque null origin", "null"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newPairingServer(t)
			req := httptest.NewRequest("POST", "/pairing/exchange", strings.NewReader(string(exchangeBody)))
			req.Host = "127.0.0.1:10994"
			req.Header.Set("Origin", tc.origin)
			res := httptest.NewRecorder()
			srv.mux.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", res.Code)
			}
		})
	}

	reached := []struct {
		name   string
		host   string
		origin string
	}{
		{"same-origin browser", "127.0.0.1:10994", "http://127.0.0.1:10994"},
		{"same-origin behind https tunnel", "nusashell.example.com", "https://nusashell.example.com"},
		{"non-browser without origin", "127.0.0.1:10994", ""},
	}
	for _, tc := range reached {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newPairingServer(t)
			req := httptest.NewRequest("POST", "/pairing/exchange", strings.NewReader(string(exchangeBody)))
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			res := httptest.NewRecorder()
			srv.mux.ServeHTTP(res, req)
			if res.Code == http.StatusForbidden {
				t.Fatalf("status = 403, want the request to reach the pairing service")
			}
			var resp contracts.Response
			if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}
			if resp.Error == nil || resp.Error.Code != contracts.CodePairingNotFound {
				t.Fatalf("error = %+v, want PAIRING_NOT_FOUND from the service", resp.Error)
			}
		})
	}
}

func TestPairingSessionRevoke(t *testing.T) {
	_, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, ""); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	exchangeRes, rpcErr := svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// Session should be valid.
	sess, ok := svc.ValidateSession(hashToken(exchangeRes.Token))
	if !ok || sess == nil {
		t.Fatal("session should be valid")
	}
	// Revoke it.
	if rpcErr := svc.RevokeSession(sess.ID); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// Session should no longer be valid.
	_, ok = svc.ValidateSession(hashToken(exchangeRes.Token))
	if ok {
		t.Fatal("revoked session should not be valid")
	}
	list, rpcErr := svc.ListSessions()
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if len(list.Sessions) != 0 {
		t.Fatalf("sessions = %d, want 0 after revoke", len(list.Sessions))
	}
}

func TestPairingSessionRevokeAll(t *testing.T) {
	_, svc := newPairingServer(t)
	// Create two sessions.
	for i := 0; i < 2; i++ {
		createRes, rpcErr := svc.CreateChallenge("")
		if rpcErr != nil {
			t.Fatal(rpcErr)
		}
		if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, ""); rpcErr != nil {
			t.Fatal(rpcErr)
		}
		_, rpcErr = svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
			ChallengeID: createRes.ChallengeID,
			Code:        createRes.Code,
		}, "")
		if rpcErr != nil {
			t.Fatal(rpcErr)
		}
	}
	// Revoke all.
	if rpcErr := svc.RevokeAllSessions(); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	list, rpcErr := svc.ListSessions()
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if len(list.Sessions) != 0 {
		t.Fatalf("sessions = %d, want 0 after revoke-all", len(list.Sessions))
	}
}

func TestPairingSessionList(t *testing.T) {
	_, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, "Phone"); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	_, rpcErr = svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	list, rpcErr := svc.ListSessions()
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if len(list.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(list.Sessions))
	}
	if list.Sessions[0].DeviceLabel != "Phone" {
		t.Fatalf("label = %q, want Phone", list.Sessions[0].DeviceLabel)
	}
}

func TestPairingStatus_NotFound(t *testing.T) {
	srv, _ := newPairingServer(t)
	req := httptest.NewRequest("GET", "/pairing/status?challenge_id=pair_nonexistent", nil)
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	var resp contracts.Response
	_ = json.NewDecoder(res.Body).Decode(&resp)
	if resp.OK {
		t.Fatal("nonexistent challenge status should fail")
	}
}

func TestPairingStatus_MissingChallengeID(t *testing.T) {
	srv, _ := newPairingServer(t)
	req := httptest.NewRequest("GET", "/pairing/status", nil)
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
}

// TestIsLoopbackRequest covers both halves of the trust rule: the effective
// client IP must be loopback (proxy-aware, see TestIsLoopbackRequest_ProxyAware)
// AND the request Host must be a loopback authority. The Host half is what
// keeps a host-local tunnel or proxy from impersonating a local caller by
// forwarding a public Host without X-Forwarded-For.
func TestIsLoopbackRequest(t *testing.T) {
	tests := []struct {
		name string
		addr string
		host string
		want bool
	}{
		{"loopback peer with loopback host", "127.0.0.1:8080", "127.0.0.1:10994", true},
		{"loopback peer with alternate 127/8 host", "127.0.0.1:8080", "127.5.6.7:10994", true},
		{"loopback peer with ipv6 loopback host", "[::1]:8080", "[::1]:10994", true},
		{"loopback peer with localhost host", "127.0.0.1:8080", "localhost:10994", true},
		{"loopback peer with localhost host without port", "127.0.0.1:8080", "localhost", true},
		{"loopback peer with public hostname", "127.0.0.1:8080", "nusashell.example.com", false},
		{"loopback peer with public hostname and port", "127.0.0.1:8080", "nusashell.example.com:443", false},
		{"loopback peer with tunnel hostname", "127.0.0.1:8080", "calm-words.trycloudflare.com", false},
		{"loopback peer with lan host", "127.0.0.1:8080", "192.168.18.81:10994", false},
		{"loopback peer with wildcard host", "127.0.0.1:8080", "0.0.0.0:10994", false},
		{"loopback peer with empty host", "127.0.0.1:8080", "", false},
		{"lan peer with lan host", "192.168.1.1:8080", "192.168.1.1:10994", false},
		{"lan peer with loopback host", "10.0.0.1:8080", "127.0.0.1:10994", false},
		{"private peer with private host", "172.16.0.1:8080", "172.16.0.1:10994", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/rpc/app/info", nil)
			req.RemoteAddr = tt.addr
			req.Host = tt.host
			if got := IsLoopbackRequest(req); got != tt.want {
				t.Errorf("IsLoopbackRequest(addr=%q, host=%q) = %v, want %v", tt.addr, tt.host, got, tt.want)
			}
		})
	}
}

func TestIsProtectedPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/rpc/agent.conversations.list", true},
		{"/ws", true},
		{"/stream", true},
		{"/stream/acp", true},
		{"/local-file", true},
		{"/plugins", true},
		{"/plugins/foo", true},
		{"/plugins/foo/tools", true},
		{"/healthz", false},
		{"/pairing/challenge", false},
		{"/pairing/status", false},
		{"/pairing/exchange", false},
		{"/index.html", false},
		{"/js/app.js", false},
		{"/sounds/turn-complete.mp3", false},
	}
	for _, tt := range tests {
		if got := isProtectedPath(tt.path); got != tt.want {
			t.Errorf("isProtectedPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestPairingExchange_RateLimit verifies the failed-attempt budget is exactly
// PairingExchangeRateLimitMax per window: each failure costs one slot, so the
// first N failures pass the entry check and the (N+1)th attempt is blocked.
func TestPairingExchange_RateLimit(t *testing.T) {
	_, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, ""); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	const source = "203.0.113.50"
	bad := contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        "WRONGCODE",
	}
	for i := 0; i < application.PairingExchangeRateLimitMax; i++ {
		_, rpcErr := svc.ExchangeChallenge(bad, source)
		if rpcErr == nil {
			t.Fatalf("bad exchange %d should fail", i)
		}
		if rpcErr.Code == contracts.CodePairingRateLimit {
			t.Fatalf("failure %d hit the rate limit early — double counting", i+1)
		}
	}
	_, rpcErr = svc.ExchangeChallenge(bad, source)
	if rpcErr == nil || rpcErr.Code != contracts.CodePairingRateLimit {
		t.Fatalf("attempt %d should be rate-limited, got %+v", application.PairingExchangeRateLimitMax+1, rpcErr)
	}
}

// TestPairingExchange_SuccessDoesNotDrainBudget verifies that a successful
// exchange does not consume a rate-limit slot — the limiter only counts
// failures.
func TestPairingExchange_SuccessDoesNotDrainBudget(t *testing.T) {
	_, svc := newPairingServer(t)
	const source = "203.0.113.51"
	// A few failures to start filling the bucket.
	for i := 0; i < 3; i++ {
		createRes, rpcErr := svc.CreateChallenge("")
		if rpcErr != nil {
			t.Fatal(rpcErr)
		}
		if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, ""); rpcErr != nil {
			t.Fatal(rpcErr)
		}
		_, rpcErr = svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
			ChallengeID: createRes.ChallengeID,
			Code:        "WRONGCODE",
		}, source)
		if rpcErr == nil || rpcErr.Code == contracts.CodePairingRateLimit {
			t.Fatalf("failure %d unexpectedly rate-limited", i)
		}
	}
	// A successful exchange must not consume budget.
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, ""); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if _, rpcErr := svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, source); rpcErr != nil {
		t.Fatalf("successful exchange should not be rate-limited: %+v", rpcErr)
	}
	// The remaining budget is 10-3 failures, not reduced by the success.
	for i := 0; i < application.PairingExchangeRateLimitMax-3; i++ {
		_, rpcErr := svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
			ChallengeID: createRes.ChallengeID,
			Code:        "WRONGCODE",
		}, source)
		if rpcErr != nil && rpcErr.Code == contracts.CodePairingRateLimit {
			t.Fatalf("failure %d hit the rate limit early — success consumed budget", i)
		}
	}
	_, rpcErr = svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        "WRONGCODE",
	}, source)
	if rpcErr == nil || rpcErr.Code != contracts.CodePairingRateLimit {
		t.Fatalf("expected rate limit after %d total failures, got %+v", application.PairingExchangeRateLimitMax, rpcErr)
	}
}

// TestPairingExchange_ConcurrentSingleUse verifies that exactly one concurrent
// exchange can consume an approved challenge; the rest must see USED.
func TestPairingExchange_ConcurrentSingleUse(t *testing.T) {
	_, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, ""); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	req := contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}
	const n = 16
	results := make(chan *contracts.PairingChallengeExchangeResult, n)
	errs := make(chan *contracts.RPCError, n)
	for i := 0; i < n; i++ {
		go func() {
			res, e := svc.ExchangeChallenge(req, "")
			if res != nil {
				results <- res
			} else {
				errs <- e
			}
		}()
	}
	success := 0
	used := 0
	for i := 0; i < n; i++ {
		select {
		case res := <-results:
			if res.Token == "" {
				t.Fatal("successful exchange must return a token")
			}
			success++
		case e := <-errs:
			if e == nil {
				t.Fatal("nil error on failed exchange")
			}
			if e.Code != contracts.CodePairingUsed {
				t.Fatalf("loser error code = %q, want PAIRING_ALREADY_USED", e.Code)
			}
			used++
		}
	}
	if success != 1 {
		t.Fatalf("success = %d, want exactly 1", success)
	}
	if used != n-1 {
		t.Fatalf("used = %d, want %d", used, n-1)
	}
}

// TestPairingManagement_RemoteSessionDenied verifies that a paired remote
// session (valid cookie) cannot invoke pairing management RPCs.
func TestPairingManagement_RemoteSessionDenied(t *testing.T) {
	srv, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, "Phone"); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	exchangeRes, rpcErr := svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	req := remoteRequest("POST", "/rpc/pairing.sessions.list", strings.NewReader(`{}`))
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: exchangeRes.Token})
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (management is local-only)", res.Code)
	}
	var resp contracts.Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != contracts.CodePairingUnauthorized {
		t.Fatalf("error = %+v, want PAIRING_UNAUTHORIZED", resp.Error)
	}
}

// TestPublicBootstrap_Restrictions verifies the public pairing surface: only
// status + exchange are public; challenge creation has no public route.
func TestPublicBootstrap_Restrictions(t *testing.T) {
	srv, _ := newPairingServer(t)
	req := remoteRequest("POST", "/pairing/challenge", strings.NewReader("{}"))
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("POST /pairing/challenge should be 404, got %d", res.Code)
	}
	req = remoteRequest("GET", "/pairing/status?challenge_id=pair_x", nil)
	res = httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /pairing/status should be 200, got %d", res.Code)
	}
	exchBody := strings.NewReader(`{"challenge_id":"pair_x","code":"x"}`)
	req = remoteRequest("POST", "/pairing/exchange", exchBody)
	res = httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("POST /pairing/exchange should be 200, got %d", res.Code)
	}
}

// TestPairingExchange_RemoteRPCDenied verifies that a paired remote session
// cannot exchange a challenge via the RPC method (pairing.challenge.exchange).
// The public /pairing/exchange route is the only exchange path for remote
// devices; the RPC method is local-only.
func TestPairingExchange_RemoteRPCDenied(t *testing.T) {
	srv, svc := newPairingServer(t)
	createRes, rpcErr := svc.CreateChallenge("")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if rpcErr := svc.ApproveChallenge(createRes.ChallengeID, "Phone"); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	exchangeRes, rpcErr := svc.ExchangeChallenge(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	}, "")
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	// A remote caller with a valid session cookie cannot exchange via RPC.
	exchBody, _ := json.Marshal(contracts.PairingChallengeExchangeRequest{
		ChallengeID: createRes.ChallengeID,
		Code:        createRes.Code,
	})
	req := remoteRequest("POST", "/rpc/pairing.challenge.exchange", strings.NewReader(string(exchBody)))
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: exchangeRes.Token})
	res := httptest.NewRecorder()
	srv.mux.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (exchange RPC is local-only)", res.Code)
	}
	var resp contracts.Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Code != contracts.CodePairingUnauthorized {
		t.Fatalf("error = %+v, want PAIRING_UNAUTHORIZED", resp.Error)
	}
}
