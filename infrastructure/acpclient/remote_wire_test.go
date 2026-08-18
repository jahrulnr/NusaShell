package acpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// TestRemoteWireRoundTrip verifies that remoteWire can write a JSON-RPC
// request over WebSocket and read back a JSON-RPC response.
func TestRemoteWireRoundTrip(t *testing.T) {
	var mu sync.Mutex
	var lastMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "bye")
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			lastMsg = string(msg)
			mu.Unlock()
			// Echo back a response with matching ID.
			reply := strings.Replace(string(msg), `"method":"test"`, `"result":{"ok":true}`, 1)
			reply = strings.Replace(reply, `"params":{}`, ``, 1)
			_ = conn.Write(ctx, websocket.MessageText, []byte(reply))
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w, err := DialRemote(ctx, strings.Replace(srv.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatalf("DialRemote: %v", err)
	}
	defer w.Close()

	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"test","params":{}}`)
	if err := w.Write(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := w.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(resp), `"ok":true`) {
		t.Fatalf("response = %s, want ok:true", string(resp))
	}
	mu.Lock()
	if !strings.Contains(lastMsg, `"method":"test"`) {
		t.Fatalf("server received %q, want method:test", lastMsg)
	}
	mu.Unlock()
}
