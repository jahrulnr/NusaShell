package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceLoginStartPendingAndExchange(t *testing.T) {
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != oauthClientID {
				t.Error("missing client ID")
			}
			w.Write([]byte(`{"device_auth_id":"device-1","usercode":"ABCD-EFGH","interval":"5"}`))
		case "/api/accounts/deviceauth/token":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["device_auth_id"] != "device-1" || body["user_code"] != "ABCD-EFGH" {
				t.Error("wrong device poll")
			}
			polls++
			if polls == 1 {
				w.WriteHeader(403)
				return
			}
			w.Write([]byte(`{"authorization_code":"auth-code","code_verifier":"verifier"}`))
		case "/oauth/token":
			r.ParseForm()
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code_verifier") != "verifier" || r.Form.Get("code") != "auth-code" || r.Form.Get("redirect_uri") == "" {
				t.Error("invalid token exchange")
			}
			w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","id_token":""}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client := deviceAuthClient{issuer: server.URL, client: server.Client()}
	code, err := client.start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code.UserCode != "ABCD-EFGH" || code.IntervalSeconds != 5 || code.VerificationURL != server.URL+"/codex/device" {
		t.Fatalf("code: %+v", code)
	}
	tok, err := client.poll(context.Background(), code.DeviceAuthID, code.UserCode)
	if err != nil || tok != nil {
		t.Fatalf("pending = %+v %v", tok, err)
	}
	tok, err = client.poll(context.Background(), code.DeviceAuthID, code.UserCode)
	if err != nil || tok == nil || tok.AccessToken != "access" {
		t.Fatalf("complete = %+v %v", tok, err)
	}
}

func TestDeviceLoginErrorsAndCancellation(t *testing.T) {
	for _, status := range []int{400, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			client := deviceAuthClient{issuer: server.URL, client: server.Client()}
			if _, err := client.start(context.Background()); err == nil {
				t.Fatal("start accepted error")
			}
			if _, err := client.poll(context.Background(), "id", "code"); err == nil {
				t.Fatal("poll accepted error")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := deviceAuthClient{issuer: "http://127.0.0.1:1", client: http.DefaultClient}
	if _, err := client.start(ctx); err == nil {
		t.Fatal("start ignored cancellation")
	}
}
