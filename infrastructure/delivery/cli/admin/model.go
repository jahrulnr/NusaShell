package admin

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const modelUsage = `usage: ` + CLIName + ` model <thread> [selector] [--addr HOST:PORT] [--json]
       ` + CLIName + ` model list (--agent <id> | --provider <id>) [--addr HOST:PORT] [--json]

A thread's runtime binding, through the running server. With no selector
it prints the current one. <thread> is the thread's id — ` + CLIName + ` conversations list
prints them (@labels resolve there, not here).

` + CLIName + ` model list prints the catalog a runtime can use. Pass exactly one of
--agent and --provider. It asks the running server and does not start an
agent. --json prints one ModelCatalog object.

Selectors:
  agent:<id>           run the thread on this agent
  agent:<id>/<model>   …and the model the agent reports
  <provider>/<model>   run on this provider's model — the split is at the
                       first "/", so ids like openrouter/auto work
`

// Model is the `nusashell model` handler.
func Model(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			fmt.Fprint(stdout, modelUsage)
			return 0
		case "list":
			return modelList(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("model", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, modelUsage) }
	addr := fs.String("addr", "", "the server's HOST:PORT")
	rawOut := fs.Bool("json", false, "print the raw response")
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return 2
	}
	if len(pos) < 1 || len(pos) > 2 {
		fmt.Fprint(stderr, modelUsage)
		return 2
	}
	id := pos[0]

	// The selector decides the PATCH body; a bad one fails before a server
	// is dialed.
	var body []byte
	if len(pos) == 2 {
		doc := map[string]any{}
		switch sel := pos[1]; {
		case strings.HasPrefix(sel, "agent:"):
			agent, model, hasModel := strings.Cut(strings.TrimPrefix(sel, "agent:"), "/")
			if agent == "" || hasModel && model == "" {
				fmt.Fprintf(stderr, "%s: bad selector %q\n", CLIName, sel)
				fmt.Fprint(stderr, modelUsage)
				return 2
			}
			doc["agentId"] = agent
			if hasModel {
				doc["modelId"] = model
			}
		default:
			provider, model, ok := strings.Cut(sel, "/")
			if !ok || provider == "" || model == "" {
				fmt.Fprintf(stderr, "%s: bad selector %q — want agent:<id>[/<model>] or <provider>/<model>\n", CLIName, sel)
				fmt.Fprint(stderr, modelUsage)
				return 2
			}
			doc["providerId"], doc["modelId"] = provider, model
		}
		body, _ = json.Marshal(doc)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv, err := dialServer(ctx, *addr)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer srv.close()

	var out []byte
	path := "/api/conversations/" + url.PathEscape(id)
	if body == nil {
		err = srv.call(ctx, http.MethodGet, path, nil, nil, &out)
	} else {
		err = srv.call(ctx, http.MethodPatch, path, nil, body, &out)
	}
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	if *rawOut {
		printJSON(stdout, out, true)
		return 0
	}
	var conv struct {
		Runtime    string `json:"runtime"`
		AgentID    string `json:"agentId"`
		ProviderID string `json:"providerId"`
		ModelID    string `json:"modelId"`
	}
	if err := json.Unmarshal(out, &conv); err != nil {
		printErr(stderr, err)
		return 1
	}
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	if conv.Runtime == "agent" {
		fmt.Fprintf(stdout, "agent:    %s\nmodel:    %s\n", conv.AgentID, dash(conv.ModelID))
	} else {
		fmt.Fprintf(stdout, "provider: %s\nmodel:    %s\n", dash(conv.ProviderID), dash(conv.ModelID))
	}
	return 0
}
