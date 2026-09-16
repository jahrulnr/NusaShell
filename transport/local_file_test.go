package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nusashell/contracts"
)

func TestLocalFilePDFUsesPDFContentTypeWithoutExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preview.bin")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n1 0 obj\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A genuine direct local call: loopback client IP and loopback Host.
	req := loopbackRequest("GET", "/local-file?path="+path, nil)
	res := httptest.NewRecorder()
	(&Server{}).handleLocalFile(res, req)

	if res.Code != 200 {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/pdf") {
		t.Fatalf("Content-Type = %q, want application/pdf", got)
	}
}

// TestLocalFile_RemoteCallerGuard verifies the handler re-checks the remote
// session itself instead of trusting the auth middleware alone (parity with
// /ws and /stream). A remote caller without a paired session must never read a
// local file, and the file content must not leak in the rejection body.
func TestLocalFile_RemoteCallerGuard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("top-secret-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := "/local-file?path=" + path

	t.Run("remote access disabled", func(t *testing.T) {
		srv := &Server{Logger: testLogger()}
		req := remoteRequest("GET", target, nil)
		res := httptest.NewRecorder()
		srv.handleLocalFile(res, req)
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
		if strings.Contains(res.Body.String(), "top-secret-content") {
			t.Fatal("rejection must not serve the file")
		}
	})

	t.Run("remote without session", func(t *testing.T) {
		srv, _ := newPairingServer(t)
		req := remoteRequest("GET", target, nil)
		res := httptest.NewRecorder()
		srv.handleLocalFile(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", res.Code)
		}
		if strings.Contains(res.Body.String(), "top-secret-content") {
			t.Fatal("rejection must not serve the file")
		}
	})

	t.Run("remote with paired session", func(t *testing.T) {
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
		req := remoteRequest("GET", target, nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: exchangeRes.Token})
		res := httptest.NewRecorder()
		srv.handleLocalFile(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", res.Code)
		}
		if !strings.Contains(res.Body.String(), "top-secret-content") {
			t.Fatal("paired remote caller should receive the file")
		}
	})
}
