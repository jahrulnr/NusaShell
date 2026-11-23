package httpmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	deliverymcp "github.com/jahrulnr/nusashell/infrastructure/delivery/mcp"
	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
)

// An agent's relay reaches a built-in server as its own thread, and only
// with that thread's token.
func TestRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	grants := deliverymcp.NewGrants()
	echo := deliverymcp.Catalog{
		Name:  "echo",
		Tools: []mcp.Tool{{Name: "whoami", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Call: func(_ context.Context, convID, tool string, args json.RawMessage) (mcp.Result, error) {
			if tool != "whoami" {
				return mcp.Result{}, errors.New("no such tool")
			}
			return mcp.TextResult(convID + " " + string(args)), nil
		},
	}
	r := gin.New()
	Register(r.Group("/api"), Deps{Grants: grants, Catalogs: []deliverymcp.Catalog{echo}})
	srv := httptest.NewServer(r)
	defer srv.Close()

	connect := func(token, server string) (*mcp.Client, error) {
		a, b := net.Pipe()
		errc := make(chan error, 1)
		go func() { errc <- deliverymcp.Relay(context.Background(), b, b, srv.URL, token, server); b.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := mcp.Connect(ctx, a, "test", "1")
		if err != nil {
			select {
			case rerr := <-errc:
				return nil, rerr
			case <-time.After(time.Second):
			}
			return nil, err
		}
		return c, nil
	}

	launch := deliverymcp.Launch{Exec: "/bin/nusashell", URL: srv.URL, Grants: grants, Servers: []string{"echo"}}.For("c_1")
	if len(launch) != 1 || launch[0].Args[2] != "echo" || launch[0].Env[deliverymcp.EnvURL] != srv.URL {
		t.Fatalf("launch = %+v", launch)
	}
	c, err := connect(launch[0].Env[deliverymcp.EnvToken], "echo")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tools, err := c.Tools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "whoami" {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	res, err := c.Call(context.Background(), "whoami", json.RawMessage(`{"x":1}`))
	if err != nil || res.IsError || res.Text() != `c_1 {"x":1}` {
		t.Fatalf("call = %+v, %v", res, err)
	}
	res, _ = c.Call(context.Background(), "nope", nil)
	if !res.IsError || !strings.Contains(res.Text(), "no such tool") {
		t.Fatalf("unknown tool = %+v", res)
	}

	if _, err := connect("forged", "echo"); err == nil || !strings.Contains(err.Error(), "unknown thread token") {
		t.Fatalf("forged token = %v", err)
	}
	grants.Revoke("c_1")
	if _, err := connect(launch[0].Env[deliverymcp.EnvToken], "echo"); err == nil {
		t.Fatal("a revoked token must stop working")
	}
}
