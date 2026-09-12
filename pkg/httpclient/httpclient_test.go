package httpclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClientsShareTransport(t *testing.T) {
	first := New()
	second := New()
	if first == second {
		t.Fatal("New returned the same client pointer; callers should get isolated client wrappers")
	}
	firstTransport, ok := first.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("first transport = %T, want *http.Transport", first.Transport)
	}
	secondTransport, ok := second.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("second transport = %T, want *http.Transport", second.Transport)
	}
	if firstTransport != secondTransport {
		t.Fatal("clients do not share the central transport")
	}
}

func TestNewWithTimeoutSharesTransport(t *testing.T) {
	client := NewWithTimeout(17 * time.Second)
	if client.Timeout != 17*time.Second {
		t.Fatalf("client timeout = %s, want 17s", client.Timeout)
	}
	if client.Transport != Shared().Transport {
		t.Fatal("timeout client does not share the central transport")
	}
}

func TestNewPublicUsesSeparateTransportAndRejectsLoopback(t *testing.T) {
	public := NewPublic()
	if public.Transport == Shared().Transport {
		t.Fatal("public client unexpectedly uses the unrestricted transport")
	}
	if public.Transport != NewPublic().Transport {
		t.Fatal("public clients do not share their transport")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	_, err = public.Do(req)
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("public client error = %v, want non-public destination rejection", err)
	}
}

func TestNewPublicRejectsLoopbackRedirectTarget(t *testing.T) {
	public := NewPublic()
	base := public.Transport
	first := true
	public.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if first {
			first = false
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"http://127.0.0.1:1/private"}},
				Body:       io.NopCloser(strings.NewReader("redirect")),
				Request:    req,
			}, nil
		}
		return base.RoundTrip(req)
	})

	req, err := http.NewRequest(http.MethodGet, "https://public.example/redirect", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, err = public.Do(req)
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("redirect error = %v, want non-public destination rejection", err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestSharedTransportReusesKeepAliveConnectionAcrossClients(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		_, _ = w.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	// The production transport rejects loopback destinations. Use a cloned
	// transport with a plain dialer here so this test can exercise keep-alive
	// reuse against httptest's loopback listener without weakening production
	// destination filtering.
	transport := Shared().Transport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: DialTimeout, KeepAlive: KeepAlive}).DialContext
	defer transport.CloseIdleConnections()
	clients := []*http.Client{{Transport: transport}, {Transport: transport, Timeout: time.Second}}
	for i, client := range clients {
		resp, err := client.Get(server.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			_ = resp.Body.Close()
			t.Fatalf("read response %d: %v", i+1, err)
		}
		_ = resp.Body.Close()
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("new connections = %d, want 1 when clients share transport", got)
	}
}

func TestCentralTransportHasConnectionPoolPolicy(t *testing.T) {
	transport, ok := Shared().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("shared transport = %T, want *http.Transport", Shared().Transport)
	}
	if transport.MaxIdleConnsPerHost < 8 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want at least 8", transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout <= 0 {
		t.Fatal("IdleConnTimeout must be bounded")
	}
}

func TestClonePreservesTransportAndTimeout(t *testing.T) {
	base := NewWithTimeout(23 * time.Second)
	clone := Clone(base)
	if clone == base {
		t.Fatal("Clone returned the original client pointer")
	}
	if clone.Transport != base.Transport {
		t.Fatal("Clone did not preserve the central transport")
	}
	if clone.Timeout != base.Timeout {
		t.Fatalf("clone timeout = %s, want %s", clone.Timeout, base.Timeout)
	}
}
