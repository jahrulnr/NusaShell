package deliverymcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
)

// relayTimeout covers the longest tool (a 30 s wait) with room to spare.
const relayTimeout = 2 * time.Minute

// Relay serves one built-in server on in/out by forwarding every call to
// the NusaShell server at baseURL. It is what an agent runs as
// `nusashell mcp run <server>`: the tools live in the server process, the
// relay only carries the protocol.
func Relay(ctx context.Context, in io.Reader, out io.Writer, baseURL, token, server string) error {
	if baseURL == "" || token == "" {
		return fmt.Errorf("mcp run is started by NusaShell for its agents and needs %s and %s", EnvURL, EnvToken)
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Host == "" {
		return fmt.Errorf("%s: bad URL %q", EnvURL, baseURL)
	}
	r := relay{endpoint: base.String() + "/api/mcp/" + url.PathEscape(server), origin: base.Scheme + "://" + base.Host,
		token: token, http: &http.Client{Timeout: relayTimeout}}
	var info struct {
		Name  string     `json:"name"`
		Tools []mcp.Tool `json:"tools"`
	}
	if err := r.do(ctx, http.MethodGet, nil, &info); err != nil {
		return fmt.Errorf("mcp run %s: %w", server, err)
	}
	return mcp.Serve(ctx, in, out, mcp.Server{Name: info.Name, Version: "1", Tools: info.Tools,
		Call: func(ctx context.Context, name string, args json.RawMessage) (mcp.Result, error) {
			var res mcp.Result
			err := r.do(ctx, http.MethodPost, map[string]any{"tool": name, "arguments": args}, &res)
			return res, err
		}})
}

type relay struct {
	endpoint, origin, token string
	http                    *http.Client
}

func (r relay) do(ctx context.Context, method string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.endpoint, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	// The server takes unsafe methods only from its own origin.
	req.Header.Set("Origin", r.origin)
	res, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		var problem struct {
			Detail string `json:"detail"`
			Title  string `json:"title"`
		}
		if json.Unmarshal(raw, &problem) == nil && (problem.Detail != "" || problem.Title != "") {
			return fmt.Errorf("%s", strings.TrimSpace(problem.Title+": "+problem.Detail))
		}
		return fmt.Errorf("server answered %s", res.Status)
	}
	return json.Unmarshal(raw, out)
}
