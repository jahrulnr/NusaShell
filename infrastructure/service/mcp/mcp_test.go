package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func pair(t *testing.T, s Server) *Client {
	t.Helper()
	a, b := net.Pipe()
	go func() { _ = Serve(context.Background(), b, b, s); b.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Connect(ctx, a, "test", "1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestClientServer(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	c := pair(t, Server{
		Name: "demo", Version: "2",
		Tools: []Tool{{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "block", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Call: func(ctx context.Context, name string, args json.RawMessage) (Result, error) {
			switch name {
			case "echo":
				return TextResult(string(args)), nil
			case "block":
				once.Do(func() { close(release) })
				<-ctx.Done()
				return Result{}, ctx.Err()
			}
			return Result{}, errors.New("no such tool")
		},
	})
	if c.Info.Name != "demo" || c.Info.Version != "2" {
		t.Fatalf("server info = %+v", c.Info)
	}
	tools, err := c.Tools(context.Background())
	if err != nil || len(tools) != 2 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}

	// A blocked call does not hold up another one, and cancelling it
	// reaches the server.
	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan error, 1)
	go func() { _, err := c.Call(ctx, "block", nil); blocked <- err }()
	<-release
	res, err := c.Call(context.Background(), "echo", json.RawMessage(`{"a":1}`))
	if err != nil || res.IsError || res.Text() != `{"a":1}` {
		t.Fatalf("echo = %+v, %v", res, err)
	}
	cancel()
	if err := <-blocked; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled call = %v", err)
	}

	res, err = c.Call(context.Background(), "nope", nil)
	if err != nil || !res.IsError || !strings.Contains(res.Text(), "no such tool") {
		t.Fatalf("unknown tool = %+v, %v", res, err)
	}
}

// A server answers a Content-Length framed request in the same framing.
func TestServeFramed(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		_ = Serve(context.Background(), b, b, Server{Name: "x", Call: func(context.Context, string, json.RawMessage) (Result, error) { return Result{}, nil }})
		b.Close()
	}()
	body := `{"jsonrpc":"2.0","id":7,"method":"ping"}`
	go func() { _, _ = a.Write([]byte("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body)) }()
	got, framed, err := readMessage(bufio.NewReader(a))
	a.Close()
	if err != nil || !framed || !strings.Contains(string(got), `"id":7`) {
		t.Fatalf("framed reply = %q framed=%v err=%v", got, framed, err)
	}
}

// Closing the server side fails the client's pending calls.
func TestClientConnectionLost(t *testing.T) {
	a, b := net.Pipe()
	started := make(chan struct{})
	go func() {
		_ = Serve(context.Background(), b, b, Server{Name: "x", Call: func(ctx context.Context, _ string, _ json.RawMessage) (Result, error) {
			close(started)
			<-ctx.Done()
			return Result{}, nil
		}})
	}()
	c, err := Connect(context.Background(), a, "t", "1")
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := c.Call(context.Background(), "wait", nil); errc <- err }()
	<-started
	b.Close()
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Fatalf("call after close = %v", err)
	}
	<-c.Done()
}
