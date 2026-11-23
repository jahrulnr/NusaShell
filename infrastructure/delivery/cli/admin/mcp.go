package admin

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	appmcp "github.com/jahrulnr/nusashell/application/mcp"
	repomcpserver "github.com/jahrulnr/nusashell/infrastructure/repository/mcpserver"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

const mcpUsage = `usage: ` + CLIName + ` mcp <list|show|add|set|remove|run> [flags]

MCP servers every agent session and the model runtime get, beside the
built-in conversation and browser servers.
  list [--json]
  show <name|id> [--json]
  add --name NAME [--transport stdio|http|sse] [--command CMD] [--arg ARG]...
      [--env KEY=VALUE]... [--url URL] [--header KEY=VALUE]... [--enabled=false]
  set <name|id> [--name NAME] [--transport T] [--command CMD] [--arg ARG]...
      [--env KEY=VALUE]... [--env-rm KEY]... [--url URL]
      [--header KEY=VALUE]... [--header-rm KEY]... [--enabled=BOOL]
  remove <name|id>
  run <conversation|browser>   started by NusaShell for its agents
Values of variables and headers are kept in the secret store and never
printed. A running server picks changes up within half a minute.
`

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

// MCP manages the owner's MCP servers.
func MCP(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, mcpUsage)
		return 2
	}
	op := args[0]
	switch op {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, mcpUsage)
		return 0
	case "list", "show", "add", "set", "remove":
	default:
		fmt.Fprintf(stderr, "%s: unknown mcp command %q\n", CLIName, op)
		fmt.Fprint(stderr, mcpUsage)
		return 2
	}
	fs := flag.NewFlagSet("mcp "+op, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, mcpUsage) }
	asJSON := fs.Bool("json", false, "JSON output")
	name := fs.String("name", "", "server name")
	transport := fs.String("transport", "", "stdio, http, or sse")
	command := fs.String("command", "", "command to start")
	url := fs.String("url", "", "server URL")
	enabled := fs.Bool("enabled", true, "offer the server")
	var argv, env, envRm, headers, headersRm repeated
	fs.Var(&argv, "arg", "command argument (repeat)")
	fs.Var(&env, "env", "environment KEY=VALUE (repeat)")
	fs.Var(&envRm, "env-rm", "remove an environment variable (repeat)")
	fs.Var(&headers, "header", "header KEY=VALUE (repeat)")
	fs.Var(&headersRm, "header-rm", "remove a header (repeat)")
	// Flags may follow the positional name.
	var pos []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	wantPos := 1
	if op == "list" || op == "add" {
		wantPos = 0
	}
	if len(pos) != wantPos {
		fmt.Fprint(stderr, mcpUsage)
		return 2
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	b, err := LoadBoot(false)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer func() { _ = b.Close() }()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(b.cfg.Storage.Dir, "nusashell.db"), migration.Files)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer db.Close()
	app := appmcp.New(repomcpserver.New(db, b.secrets), nil, nil)

	show := func(v appmcp.View) {
		if *asJSON {
			raw, _ := json.MarshalIndent(v, "", "  ")
			fmt.Fprintln(stdout, string(raw))
			return
		}
		state := "enabled"
		if !v.Enabled {
			state = "disabled"
		}
		target := v.URL
		if v.Transport == "stdio" {
			target = strings.Join(append([]string{v.Command}, v.Args...), " ")
		}
		fmt.Fprintf(stdout, "%-16s %-6s %-8s %s\n", v.Name, v.Transport, state, target)
		if len(v.Env) > 0 {
			fmt.Fprintf(stdout, "  env: %s\n", strings.Join(v.Env, ", "))
		}
		if len(v.Headers) > 0 {
			fmt.Fprintf(stdout, "  headers: %s\n", strings.Join(v.Headers, ", "))
		}
	}
	parsePairs := func(list []string) ([]appmcp.Pair, error) {
		var out []appmcp.Pair
		for _, kv := range list {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || strings.TrimSpace(k) == "" || v == "" {
				return nil, fmt.Errorf("%q: use KEY=VALUE with a value", kv)
			}
			out = append(out, appmcp.Pair{Name: strings.TrimSpace(k), Value: v})
		}
		return out, nil
	}
	addEnv, err := parsePairs(env)
	if err != nil {
		printErr(stderr, err)
		return 2
	}
	addHeaders, err := parsePairs(headers)
	if err != nil {
		printErr(stderr, err)
		return 2
	}
	switch op {
	case "list":
		list, err := app.List(ctx)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		if *asJSON {
			raw, _ := json.MarshalIndent(list, "", "  ")
			fmt.Fprintln(stdout, string(raw))
			return 0
		}
		if len(list) == 0 {
			fmt.Fprintln(stdout, "no MCP servers — add one with: "+CLIName+" mcp add --name NAME --command CMD")
		}
		for _, v := range list {
			show(v)
		}
	case "show":
		v, err := app.Find(ctx, pos[0])
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		show(v)
	case "remove":
		v, err := app.Find(ctx, pos[0])
		if err == nil {
			err = app.Delete(ctx, v.ID)
		}
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "removed MCP server %s\n", v.Name)
	case "add":
		v, err := app.Create(ctx, appmcp.Input{Name: *name, Transport: *transport, Command: *command, Args: argv,
			Env: addEnv, URL: *url, Headers: addHeaders, Enabled: enabled})
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "added MCP server %s (%s)\n", v.Name, v.ID)
	case "set":
		cur, err := app.Find(ctx, pos[0])
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		in := appmcp.Input{Name: cur.Name, Transport: cur.Transport, Command: cur.Command, Args: cur.Args, URL: cur.URL}
		if set["name"] {
			in.Name = *name
		}
		if set["transport"] {
			in.Transport = *transport
		}
		if set["command"] {
			in.Command = *command
		}
		if set["arg"] {
			in.Args = argv
		}
		if set["url"] {
			in.URL = *url
		}
		if set["enabled"] {
			in.Enabled = enabled
		}
		// A kept variable carries no value, so the stored one stays.
		merge := func(have []string, add []appmcp.Pair, drop []string) []appmcp.Pair {
			var out []appmcp.Pair
			for _, k := range have {
				if !slices.Contains(drop, k) && !slices.ContainsFunc(add, func(p appmcp.Pair) bool { return p.Name == k }) {
					out = append(out, appmcp.Pair{Name: k})
				}
			}
			return append(out, add...)
		}
		in.Env = merge(cur.Env, addEnv, envRm)
		in.Headers = merge(cur.Headers, addHeaders, headersRm)
		readAt, _ := time.Parse(time.RFC3339Nano, cur.UpdatedAt)
		v, err := app.Update(ctx, cur.ID, in, readAt)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "saved MCP server %s\n", v.Name)
	}
	return 0
}
