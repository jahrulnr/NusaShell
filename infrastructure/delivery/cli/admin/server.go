package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	appauth "github.com/jahrulnr/nusashell/application/auth"
	httpauth "github.com/jahrulnr/nusashell/infrastructure/delivery/http/auth"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	repoauth "github.com/jahrulnr/nusashell/infrastructure/repository/auth"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/infrastructure/service/password"
	"github.com/jahrulnr/nusashell/migration"
)

// EnvAddr names the host:port of the running server. `serve` sets it for
// its children (agents, shells), so commands they run reach the same
// server whatever --port it was given.
const EnvAddr = "NUSASHELL_ADDR"

// cliSessionTTL bounds the session a command opens on the server.
const cliSessionTTL = 5 * time.Minute

// serverClient calls the running server's HTTP API for commands that
// need its live state (a channel's connection). When the server asks for
// a login, it opens a short session in the application database — whoever
// runs this binary against that database is the owner — and ends it on
// close.
type serverClient struct {
	base  string
	token string
	csrf  string
	end   func()
	http  *http.Client
}

// dialServer finds the server: addr when given, else $NUSASHELL_ADDR,
// else the configured port on a loopback name the server accepts.
func dialServer(ctx context.Context, addr string) (*serverClient, error) {
	cfg, _, err := LoadConfig(false)
	if err != nil {
		return nil, err
	}
	if addr == "" {
		addr = os.Getenv(EnvAddr)
	}
	if addr == "" {
		srv := cfg.Server
		srv.Normalize()
		addr = srv.LocalAuthority(srv.Port)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("server address %q: want HOST:PORT", addr)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	s := &serverClient{base: "http://" + net.JoinHostPort(host, port), end: func() {}, http: &http.Client{}}
	var status struct {
		Required bool `json:"required"`
	}
	if err := s.call(ctx, http.MethodGet, "/api/auth/status", nil, nil, &status); err != nil {
		if _, ok := errors.AsType[*net.OpError](err); ok {
			return nil, fmt.Errorf("no %s server answers at %s — start it with `%s serve`, or pass --addr", ProductName, addr, CLIName)
		}
		return nil, err
	}
	if !status.Required {
		return s, nil
	}
	db, err := store.Open(ctx, filepath.Join(cfg.Storage.Dir, "nusashell.db"), migration.Files)
	if err != nil {
		return nil, err
	}
	auth, err := appauth.New(repoauth.New(db), password.Bcrypt{}, enum.AuthModeOn, false)
	if err == nil {
		s.token, s.csrf, err = auth.LocalSession(ctx, cliSessionTTL)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	s.end = func() {
		_ = auth.Logout(context.Background(), s.token)
		db.Close()
	}
	return s, nil
}

func (s *serverClient) close() { s.end() }

// call sends one request. body, when not nil, goes as JSON; a 2xx answer
// decodes into out (or is copied when out is a *[]byte). Any other answer
// becomes an error carrying the server's detail.
func (s *serverClient) call(ctx context.Context, method, path string, query url.Values, body []byte, out any) error {
	u := s.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The server refuses unsafe methods from another origin; this command
	// is a same-origin caller.
	req.Header.Set("Origin", s.base)
	if s.token != "" {
		req.AddCookie(&http.Cookie{Name: httpauth.CookieName, Value: s.token})
		req.Header.Set(httpauth.CSRFHeader, s.csrf)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var p struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(raw, &p) == nil && p.Detail != "" {
			return errors.New(p.Detail)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	switch o := out.(type) {
	case nil:
		return nil
	case *[]byte:
		*o = raw
		return nil
	default:
		return json.Unmarshal(raw, out)
	}
}
