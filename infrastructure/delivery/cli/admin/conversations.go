package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	cliterm "github.com/jahrulnr/nusashell/infrastructure/delivery/cli/term"
	deliverymcp "github.com/jahrulnr/nusashell/infrastructure/delivery/mcp"
	"github.com/jahrulnr/nusashell/infrastructure/service/mcp"
)

const conversationsUsage = `usage: ` + CLIName + ` conversations <command> [flags]

Work with NusaShell threads from a shell. This calls the running server's
conversation MCP endpoint — POST <url>/api/mcp/conversation — which
wants a base URL and a thread token, not the owner's login. The URL
defaults to $` + deliverymcp.EnvURL + `, else $` + EnvAddr + `; the token to
$` + deliverymcp.EnvToken + `, which NusaShell exports on the MCP children it
starts (nusashell mcp run …) — not on your shell, so pass --token or
export it. <id> is a thread id or its @label.

  list [--query Q] [--project P] [--limit N] [--offset N]
                            threads, most recent first
  projects [--query Q]      projects: id, name, folders
  search <text> [--conversation ID | --project P] [--kinds K,K] [--limit N]
                            message snippets across threads; K: text, tool
  export (--conversation ID | --project P) [--include-tool-output]
                            write the thread(s) as JSONL, print the path
  read <id> [--after-seq N] [--before-seq N] [--limit N] [--include-tool-data]
                            a window of a thread, oldest first
  wait <id> [--after-seq N] [--timeout S]
                            up to S seconds (server caps at 30) for new
                            messages; a timeout is a normal answer
  send <id> <text...>       a prompt for the thread (or --text S)
  stop <id>                 cancel its running turn
  spawn <prompt...> [--title T] [--agent A | --provider P --model M] [--project P]
                            open a thread and start its first turn
  report <text...>          a progress note to the chat your turn came from

--json prints the raw MCP result instead of the tool's text.
`

// Conversations is the `nusashell conversations` handler.
func Conversations(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, conversationsUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, conversationsUsage)
		return 0
	}
	op, rest := args[0], args[1:]
	switch op {
	case "list", "projects", "search", "export", "read", "wait", "send", "stop", "spawn", "report":
	default:
		fmt.Fprintf(stderr, "%s: unknown conversations command %q\n", CLIName, op)
		fmt.Fprint(stderr, conversationsUsage)
		return 2
	}
	fs := flag.NewFlagSet("conversations "+op, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, conversationsUsage) }
	mcpURL := fs.String("url", "", "server base URL (default $"+deliverymcp.EnvURL+", else $"+EnvAddr+")")
	mcpToken := fs.String("token", "", "thread token (default $"+deliverymcp.EnvToken+")")
	rawOut := fs.Bool("json", false, "print the raw MCP result")
	var (
		query, project, kinds, convID, text string
		title, agentID, providerID, modelID string
		limit, offset, afterSeq, beforeSeq  int
		timeout                             int
		includeToolData, includeToolOutput  bool
	)
	switch op {
	case "list":
		fs.StringVar(&query, "query", "", "")
		fs.StringVar(&project, "project", "", "")
		fs.IntVar(&limit, "limit", 0, "")
		fs.IntVar(&offset, "offset", 0, "")
	case "projects":
		fs.StringVar(&query, "query", "", "")
	case "search":
		fs.StringVar(&convID, "conversation", "", "")
		fs.StringVar(&project, "project", "", "")
		fs.StringVar(&kinds, "kinds", "", "")
		fs.IntVar(&limit, "limit", 0, "")
	case "export":
		fs.StringVar(&convID, "conversation", "", "")
		fs.StringVar(&project, "project", "", "")
		fs.BoolVar(&includeToolOutput, "include-tool-output", false, "")
	case "read":
		fs.IntVar(&afterSeq, "after-seq", 0, "")
		fs.IntVar(&beforeSeq, "before-seq", 0, "")
		fs.IntVar(&limit, "limit", 0, "")
		fs.BoolVar(&includeToolData, "include-tool-data", false, "")
	case "wait":
		fs.IntVar(&afterSeq, "after-seq", 0, "")
		fs.IntVar(&timeout, "timeout", 0, "")
	case "send":
		fs.StringVar(&text, "text", "", "")
	case "spawn":
		fs.StringVar(&title, "title", "", "")
		fs.StringVar(&agentID, "agent", "", "")
		fs.StringVar(&providerID, "provider", "", "")
		fs.StringVar(&modelID, "model", "", "")
		fs.StringVar(&project, "project", "", "")
	}
	pos, err := parseInterleaved(fs, rest)
	if err != nil {
		return 2
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "%s: %s\n", CLIName, fmt.Sprintf(format, a...))
		return 2
	}
	for _, n := range []struct {
		flag string
		val  int
	}{{"limit", limit}, {"offset", offset}, {"timeout", timeout}} {
		if n.val < 0 {
			return fail("--%s needs a non-negative number", n.flag)
		}
	}
	// Map argv onto the tool call before checking the endpoint: a usage
	// mistake fails without one.
	toolArgs := map[string]any{}
	switch op {
	case "list":
		if len(pos) > 0 {
			return fail("unexpected arguments: %s", strings.Join(pos, " "))
		}
		toolArgs["query"], toolArgs["project"] = query, project
		if limit > 0 {
			toolArgs["limit"] = limit
		}
		if offset > 0 {
			toolArgs["offset"] = offset
		}
	case "projects":
		if len(pos) > 0 {
			return fail("unexpected arguments: %s", strings.Join(pos, " "))
		}
		toolArgs["query"] = query
	case "search":
		if len(pos) == 0 {
			return fail("search needs the text to find")
		}
		if convID != "" && project != "" {
			return fail("search scopes to one of --conversation or --project")
		}
		toolArgs["query"] = strings.Join(pos, " ")
		if convID != "" {
			toolArgs["conversationId"] = convID
		}
		if project != "" {
			toolArgs["project"] = project
		}
		if kinds != "" {
			var ks []string
			for _, k := range strings.Split(kinds, ",") {
				if k = strings.TrimSpace(k); k != "" {
					ks = append(ks, k)
				}
			}
			if len(ks) > 0 {
				toolArgs["kinds"] = ks
			}
		}
		if limit > 0 {
			toolArgs["limit"] = limit
		}
	case "export":
		if len(pos) > 0 {
			return fail("unexpected arguments: %s", strings.Join(pos, " "))
		}
		if (convID == "") == (project == "") {
			return fail("export takes one of --conversation ID or --project P")
		}
		if convID != "" {
			toolArgs["conversationId"] = convID
		} else {
			toolArgs["project"] = project
		}
		if includeToolOutput {
			toolArgs["includeToolOutput"] = true
		}
	case "read", "wait", "stop":
		if len(pos) != 1 {
			return fail("%s needs a thread id or @label", op)
		}
		toolArgs["conversationId"] = pos[0]
		if given["after-seq"] {
			toolArgs["afterSeq"] = afterSeq
		}
		if op == "read" {
			if given["before-seq"] {
				toolArgs["beforeSeq"] = beforeSeq
			}
			if limit > 0 {
				toolArgs["limit"] = limit
			}
			if includeToolData {
				toolArgs["includeToolData"] = true
			}
		}
		if op == "wait" && timeout > 0 {
			toolArgs["timeoutSeconds"] = timeout
		}
	case "send":
		if len(pos) == 0 {
			return fail("send needs a thread id or @label")
		}
		msg := text
		if len(pos) > 1 {
			if text != "" {
				return fail("send takes --text or positional text, not both")
			}
			msg = strings.Join(pos[1:], " ")
		}
		if msg == "" {
			return fail("send needs text — send <id> <text> or send <id> --text S")
		}
		toolArgs["conversationId"], toolArgs["text"] = pos[0], msg
	case "spawn":
		if len(pos) == 0 {
			return fail("spawn needs a prompt")
		}
		if agentID != "" && (providerID != "" || modelID != "") {
			return fail("spawn runs on --agent, or on --provider with --model — not both")
		}
		if (providerID == "") != (modelID == "") {
			return fail("a provider run needs --provider and --model")
		}
		toolArgs["prompt"] = strings.Join(pos, " ")
		for _, kv := range [][2]string{{"title", title}, {"agent", agentID}, {"provider", providerID}, {"model", modelID}, {"project", project}} {
			if kv[1] != "" {
				toolArgs[kv[0]] = kv[1]
			}
		}
	case "report":
		if len(pos) == 0 {
			return fail("report needs text")
		}
		toolArgs["text"] = strings.Join(pos, " ")
	}

	if *mcpURL == "" {
		*mcpURL = os.Getenv(deliverymcp.EnvURL)
	}
	if *mcpURL == "" {
		if addr := os.Getenv(EnvAddr); addr != "" {
			*mcpURL = "http://" + addr
		}
	}
	if *mcpToken == "" {
		*mcpToken = os.Getenv(deliverymcp.EnvToken)
	}
	if *mcpURL == "" || *mcpToken == "" {
		fmt.Fprintf(stderr, "%s: conversations needs a server URL and a thread token.\n", CLIName)
		if *mcpURL == "" {
			fmt.Fprintf(stderr, "URL: pass --url, or export %s or %s (the server's address).\n", deliverymcp.EnvURL, EnvAddr)
		}
		if *mcpToken == "" {
			fmt.Fprintf(stderr, "Token: pass --token, or export %s — NusaShell installs it on the\n", deliverymcp.EnvToken)
			fmt.Fprintf(stderr, "MCP children it starts (`%s mcp run conversation`), not on your shell.\n", CLIName)
		}
		return 1
	}
	client, err := newConversationMCP(*mcpURL, *mcpToken)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	raw, err := client.call(ctx, op, toolArgs)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	var res mcp.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		printErr(stderr, fmt.Errorf("bad MCP result: %w", err))
		return 1
	}
	if *rawOut {
		fmt.Fprintln(stdout, strings.TrimSpace(string(raw)))
	} else if text := res.Text(); text != "" {
		if res.IsError {
			fmt.Fprintln(stderr, cliterm.Sanitize(text))
		} else {
			fmt.Fprintln(stdout, text)
		}
	}
	if res.IsError {
		return 1
	}
	return 0
}

// conversationMCP posts one tool call to the server's conversation MCP
// endpoint. Unlike serverClient it is not the owner: the Bearer token
// stands in for the thread the call is made on behalf of.
type conversationMCP struct {
	endpoint, origin, token string
	http                    *http.Client
}

func newConversationMCP(rawURL, token string) (*conversationMCP, error) {
	base, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("bad %s %q — want the server's base URL", deliverymcp.EnvURL, rawURL)
	}
	return &conversationMCP{
		endpoint: base.String() + "/api/mcp/conversation",
		origin:   base.Scheme + "://" + base.Host,
		token:    token,
		http:     &http.Client{},
	}, nil
}

// call posts {tool, arguments} and returns the raw MCP result.
func (c *conversationMCP) call(ctx context.Context, tool string, args map[string]any) ([]byte, error) {
	body, err := json.Marshal(map[string]any{"tool": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	// The server takes unsafe methods only from its own origin.
	req.Header.Set("Origin", c.origin)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		var p struct {
			Detail string `json:"detail"`
			Title  string `json:"title"`
		}
		if json.Unmarshal(raw, &p) == nil {
			if p.Detail != "" {
				return nil, errors.New(p.Detail)
			}
			if p.Title != "" {
				return nil, errors.New(p.Title)
			}
		}
		return nil, fmt.Errorf("server answered %s", res.Status)
	}
	return raw, nil
}
