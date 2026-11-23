package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/config"
)

func TestServeHelpRequested(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		if !serveHelpRequested(args) {
			t.Fatalf("serveHelpRequested(%v) = false, want true", args)
		}
	}
	for _, args := range [][]string{nil, {"--port", "1"}, {"--port", "help"}, {"--port=help"}, {"-port", "-h"}} {
		if serveHelpRequested(args) {
			t.Fatalf("serveHelpRequested(%v) = true, want false", args)
		}
	}
}

func TestServePort(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    int
		open    bool
		wantErr bool
	}{
		{"default", nil, 10994, false, false},
		{"flag", []string{"--port", "9090"}, 9090, false, false},
		{"flag equals", []string{"--port=9090"}, 9090, false, false},
		{"edge low", []string{"--port", "1"}, 1, false, false},
		{"edge high", []string{"--port", "65535"}, 65535, false, false},
		{"open", []string{"--open"}, 10994, true, false},
		{"open with port", []string{"--open", "--port", "9090"}, 9090, true, false},
		{"zero", []string{"--port", "0"}, 0, false, true},
		{"negative", []string{"--port", "-1"}, 0, false, true},
		{"too high", []string{"--port", "65536"}, 0, false, true},
		{"not a number", []string{"--port", "abc"}, 0, false, true},
		{"help as port value", []string{"--port", "help"}, 0, false, true},
		{"help as port equals", []string{"--port=help"}, 0, false, true},
		{"positional", []string{"extra"}, 0, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, open, err := serveFlags(tc.args, 10994)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got port %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %d, got %d", tc.want, got)
			}
			if open != tc.open {
				t.Fatalf("open = %v, want %v", open, tc.open)
			}
		})
	}
}

func TestOpenTargetURL(t *testing.T) {
	c := config.ServerConfig{Bind: "127.0.0.1", Port: 10994}
	c.Normalize()
	if got := openTargetURL(c, 10994); got != "http://127.0.0.1:10994" {
		t.Fatalf("default target = %q", got)
	}
	c.PublicURL = "https://shell.example.com"
	c.Normalize()
	if got := openTargetURL(c, 10994); got != "https://shell.example.com" {
		t.Fatalf("public target = %q", got)
	}
}

// The EADDRINUSE reuse probe: a server answering /api/health counts as
// already running; anything else is a foreign process.
func TestHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	authority := strings.TrimPrefix(srv.URL, "http://")
	if !healthy(authority) {
		t.Fatal("healthy authority not recognized")
	}
	if healthy("127.0.0.1:1") {
		t.Fatal("closed port reported healthy")
	}
}
