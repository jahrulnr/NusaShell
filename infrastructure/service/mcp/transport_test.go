package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

var echoServer = Server{
	Name: "echo", Version: "1",
	Tools: []Tool{{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	Call: func(_ context.Context, _ string, args json.RawMessage) (Result, error) {
		return TextResult("echo " + string(args) + " " + os.Getenv("ECHO_SUFFIX")), nil
	},
}

// The test binary doubles as a stdio server for TestSpawn.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_TEST_SERVE") == "1" {
		_ = Serve(context.Background(), os.Stdin, os.Stdout, echoServer)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func checkEcho(t *testing.T, conn io.ReadWriteCloser, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Connect(ctx, conn, "test", "1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tools, err := c.Tools(ctx)
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	res, err := c.Call(ctx, "echo", json.RawMessage(`{"a":1}`))
	if err != nil || strings.TrimSpace(res.Text()) != want {
		t.Fatalf("call = %q, %v; want %q", res.Text(), err, want)
	}
}

func TestSpawn(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Spawn(exe, []string{"-test.run=^$"}, map[string]string{"MCP_TEST_SERVE": "1", "ECHO_SUFFIX": "stdio"})
	if err != nil {
		t.Fatal(err)
	}
	checkEcho(t, conn, `echo {"a":1} stdio`)
}

// streamable answers each POST: JSON for most requests, an event stream
// for tools/call, 202 for notifications; it checks the session header.
func streamable(t *testing.T) *httptest.Server {
	var mu sync.Mutex
	session := ""
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Key") != "k" {
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		var req message
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		if req.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != session {
			mu.Unlock()
			http.Error(w, "bad session", http.StatusBadRequest)
			return
		}
		mu.Unlock()
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			mu.Lock()
			session = "s1"
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", "s1")
			result = map[string]any{"protocolVersion": ProtocolVersion, "serverInfo": map[string]any{"name": "echo"}}
		case "tools/list":
			result = map[string]any{"tools": echoServer.Tools}
		case "tools/call":
			var p struct {
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": TextResult("echo " + string(p.Arguments) + " http")})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
}

func TestDialStreamableHTTP(t *testing.T) {
	srv := streamable(t)
	defer srv.Close()
	conn, err := Dial(srv.URL, map[string]string{"X-Key": "k"}, false)
	if err != nil {
		t.Fatal(err)
	}
	checkEcho(t, conn, `echo {"a":1} http`)

	// A refused request fails the call instead of hanging it.
	bad, _ := Dial(srv.URL, nil, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Connect(ctx, bad, "test", "1"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("connect without key = %v", err)
	}
}

// legacySSE is the older transport: GET opens an event stream that names a
// POST endpoint; answers to posts come back on the stream.
func TestDialLegacySSE(t *testing.T) {
	events := make(chan []byte, 16)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: endpoint\ndata: /messages?session=1\n\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case b := <-events:
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
				w.(http.Flusher).Flush()
			}
		}
	})
	mux.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		var req message
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.WriteHeader(http.StatusAccepted)
		if req.ID == nil {
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": ProtocolVersion, "serverInfo": map[string]any{"name": "echo"}}
		case "tools/list":
			result = map[string]any{"tools": echoServer.Tools}
		case "tools/call":
			var p struct {
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = TextResult("echo " + string(p.Arguments) + " sse")
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		events <- b
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	conn, err := Dial(srv.URL+"/sse", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	checkEcho(t, conn, `echo {"a":1} sse`)
}
