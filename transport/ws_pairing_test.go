package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/infrastructure/sqlitestore"
)

// newPairingWSServer starts a real httptest server with the WS handler wired
// to a pairing service, returning the server, the service, and a way to build
// remote/loopback WS URLs.
func newPairingWSServer(t *testing.T) (*httptest.Server, *application.PairingService) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := sqlitestore.NewPairingStore(dataDir + "/pairing.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	svc := application.NewPairingService(store)
	app := application.NewApp(application.Deps{})
	srv := &Server{
		App:     app,
		Pairing: svc,
		Logger:  testLogger(),
		mux:     nil,
	}
	// Use a simple handler that delegates to handleWS so we can control
	// RemoteAddr via the test server's connections. httptest records the
	// real remote address of the dialing client, which is loopback when the
	// test dials 127.0.0.1. To simulate a remote session, we wrap the handler
	// and override RemoteAddr.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Force a non-loopback RemoteAddr to simulate a remote paired device.
		r.RemoteAddr = "203.0.113.99:54321"
		srv.handleWS(w, r)
	}))
	t.Cleanup(func() { ts.Close() })
	return ts, svc
}

// dialWS dials the test server's /ws endpoint with the given Origin and
// session cookie.
func dialWS(t *testing.T, ts *httptest.Server, origin, cookie string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hdr := map[string][]string{}
	if origin != "" {
		hdr["Origin"] = []string{origin}
	}
	if cookie != "" {
		hdr["Cookie"] = []string{SessionCookieName + "=" + cookie}
	}
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// TestWS_RemoteManagementDenied verifies that a remote paired session cannot
// invoke pairing management RPCs over a WebSocket frame.
func TestWS_RemoteManagementDenied(t *testing.T) {
	ts, svc := newPairingWSServer(t)
	// Create + approve + exchange to get a real session token.
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
	// Dial with the valid session cookie and a same-origin browser Origin so
	// the upgrade succeeds.
	conn := dialWS(t, ts, ts.URL, exchangeRes.Token)
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Try to call a management method.
	req := contracts.WSRequest{ID: 1, Method: contracts.MethodPairingSessionsList, Payload: json.RawMessage(`{}`)}
	b, _ := json.Marshal(req)
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp contracts.WSResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("remote WS frame should be denied for management method")
	}
	if resp.Error == nil || resp.Error.Code != contracts.CodePairingUnauthorized {
		t.Fatalf("error = %+v, want PAIRING_UNAUTHORIZED", resp.Error)
	}
}

// TestWS_RemoteCrossOriginRejected verifies that a remote session with a
// cross-origin browser Origin is rejected at upgrade time (HTTP 403).
func TestWS_RemoteCrossOriginRejected(t *testing.T) {
	ts, svc := newPairingWSServer(t)
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
	// Cross-origin: the test server is 127.0.0.1, but Origin claims attacker.com.
	// The upgrade must be rejected (HTTP 403), so Dial fails.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	hdr := map[string][]string{
		"Origin": {"https://attacker.com"},
		"Cookie": {SessionCookieName + "=" + exchangeRes.Token},
	}
	_, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err == nil {
		t.Fatal("expected cross-origin WS dial to be rejected (403)")
	}
}

// TestWS_RemoteRevokedMidConnection verifies that revoking a session cuts
// live access: the next frame after revocation gets the connection closed
// instead of a dispatch.
func TestWS_RemoteRevokedMidConnection(t *testing.T) {
	ts, svc := newPairingWSServer(t)
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
	conn := dialWS(t, ts, ts.URL, exchangeRes.Token)
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Sanity: a frame gets a response while the session is valid. An unknown
	// method is used because the test App has no wired services — the reply
	// (a validation error) still proves the connection round-trips.
	frame := func(id int, method string) {
		b, _ := json.Marshal(contracts.WSRequest{ID: id, Method: method, Payload: json.RawMessage(`{}`)})
		if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatalf("write frame %d: %v", id, err)
		}
	}
	frame(1, "test.noop")
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("pre-revocation frame should get a response: %v", err)
	}

	// Revoke the session; the next frame must close the connection.
	sessions, rpcErr := svc.ListSessions()
	if rpcErr != nil || len(sessions.Sessions) == 0 {
		t.Fatalf("list sessions: %v", rpcErr)
	}
	if rpcErr := svc.RevokeSession(sessions.Sessions[0].ID); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	frame(2, "test.noop")
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("revoked session frame should close the connection")
	}
	if status := websocket.CloseStatus(err); status != websocket.StatusPolicyViolation {
		t.Fatalf("close status = %v, want StatusPolicyViolation", status)
	}
}

// TestWS_RemoteExchangeDenied verifies that a remote paired session cannot
// exchange a challenge via the RPC method (pairing.challenge.exchange) over a
// WebSocket frame. The public /pairing/exchange route is the only exchange
// path for remote devices; the RPC method is local-only.
func TestWS_RemoteExchangeDenied(t *testing.T) {
	ts, svc := newPairingWSServer(t)
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
	conn := dialWS(t, ts, ts.URL, exchangeRes.Token)
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := contracts.WSRequest{ID: 1, Method: contracts.MethodPairingChallengeExchange, Payload: json.RawMessage(`{"challenge_id":"pair_x","code":"x"}`)}
	b, _ := json.Marshal(req)
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var resp contracts.WSResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("remote WS frame should be denied for exchange method")
	}
	if resp.Error == nil || resp.Error.Code != contracts.CodePairingUnauthorized {
		t.Fatalf("error = %+v, want PAIRING_UNAUTHORIZED", resp.Error)
	}
}
