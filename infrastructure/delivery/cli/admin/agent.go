package admin

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	appagent "github.com/jahrulnr/nusashell/application/agent"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
	serviceagent "github.com/jahrulnr/nusashell/infrastructure/service/agent"
)

const agentUsage = `Usage: nusashell agent <subcommand> [flags]

External agents are CLIs that speak the Agent Client Protocol over stdio.
A running server picks up changes on the agent's next turn.

Subcommands:
  list                         configured agents
  show <id>                    one agent (environment values stay hidden)
  detect [--add]               known agent CLIs found on PATH; --add registers the new ones
  add <id> --command C [flags] register an agent
  set <id> [flags]             change an agent; unspecified fields keep their value
  remove <id>                  delete an agent

Flags for add/set:
  --name N          display name
  --command C       executable to launch
  --arg A           launch argument (repeat; replaces all arguments on set)
  --clear-args      set only: drop every argument
  --env NAME=VALUE  launch variable stored as a secret (repeat); VALUE may be env:VAR
  --env-rm NAME     set only: remove a launch variable (repeat)
  --policy P        ask | auto_safe | auto_all (default ask)
  --auto-start      keep the agent's process running from the server's start
  --no-auto-start   set only: let the idle reaper stop it again
  --default ID=V    default value of a session option, for rooms that pick none (repeat;
                    replaces all defaults on set)
  --clear-defaults  set only: drop every default
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// Agent is the `nusashell agent <sub>` handler.
func Agent(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, agentUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, agentUsage)
		return 0
	}
	b, err := LoadBoot(false)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer func() { _ = b.Close() }()
	app := appagent.New(repoagent.New(filepath.Join(b.cfg.Storage.Dir, "agents"), b.secrets), nil, serviceagent.Detector{})
	ctx := context.Background()

	switch args[0] {
	case "list":
		views, err := app.List(ctx)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		if len(views) == 0 {
			fmt.Fprintln(stdout, "no agents configured — try: "+CLIName+" agent detect --add")
			return 0
		}
		for _, v := range views {
			fmt.Fprintf(stdout, "%-14s %-18s %-9s %s\n", v.ID, v.Name, v.Policy, strings.Join(append([]string{v.Command}, v.Args...), " "))
		}
	case "show":
		if len(args) != 2 {
			fmt.Fprint(stderr, agentUsage)
			return 2
		}
		v, err := app.Get(ctx, args[1])
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		printAgent(stdout, v)
	case "detect":
		fs := flag.NewFlagSet("agent detect", flag.ContinueOnError)
		fs.SetOutput(stderr)
		add := fs.Bool("add", false, "register detected agents that are not configured yet")
		if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
			return 2
		}
		if *add {
			added, err := app.ImportDetected(ctx)
			for _, v := range added {
				fmt.Fprintf(stdout, "added agent %s (%s)\n", v.ID, v.Command)
			}
			if err != nil {
				printErr(stderr, err)
				return 1
			}
			if len(added) == 0 {
				fmt.Fprintln(stdout, "no new agents to add")
			}
			return 0
		}
		found, err := app.Detect(ctx)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		if len(found) == 0 {
			fmt.Fprintln(stdout, "no known agent CLIs found on PATH")
		}
		for _, c := range found {
			state := "new"
			if c.Registered {
				state = "configured"
			}
			fmt.Fprintf(stdout, "%-10s %-11s %s\n", c.ID, state, strings.Join(append([]string{c.Command}, c.Args...), " "))
		}
	case "add", "set":
		return agentWrite(ctx, app, args[0] == "set", args[1:], stdout, stderr)
	case "remove":
		if len(args) != 2 {
			fmt.Fprint(stderr, agentUsage)
			return 2
		}
		if err := app.Delete(ctx, args[1]); err != nil {
			printErr(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "removed agent %s\n", args[1])
	default:
		fmt.Fprintf(stderr, "%s: unknown agent subcommand %q\n", CLIName, args[0])
		fmt.Fprint(stderr, agentUsage)
		return 2
	}
	return 0
}

func printAgent(w io.Writer, v appagent.View) {
	fmt.Fprintf(w, "id:      %s\nname:    %s\ncommand: %s\n", v.ID, v.Name, v.Command)
	if len(v.Args) > 0 {
		fmt.Fprintf(w, "args:    %s\n", strings.Join(v.Args, " "))
	}
	fmt.Fprintf(w, "policy:  %s\n", v.Policy)
	if v.AutoStart {
		fmt.Fprintf(w, "start:   with the server\n")
	}
	for _, k := range slices.Sorted(maps.Keys(v.Defaults)) {
		fmt.Fprintf(w, "default: %s=%s\n", k, v.Defaults[k])
	}
	for _, e := range v.Env {
		fmt.Fprintf(w, "env:     %s (%s)\n", e.Name, e.Ref)
	}
}

func agentWrite(ctx context.Context, app *appagent.AgentApp, update bool, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(stderr, agentUsage)
		return 2
	}
	id := args[0]
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "")
	command := fs.String("command", "", "")
	policy := fs.String("policy", "", "")
	clearArgs := fs.Bool("clear-args", false, "")
	autoStart := fs.Bool("auto-start", false, "")
	noAutoStart := fs.Bool("no-auto-start", false, "")
	clearDefaults := fs.Bool("clear-defaults", false, "")
	var argList, envSet, envRm, defaultSet multiFlag
	fs.Var(&defaultSet, "default", "")
	fs.Var(&argList, "arg", "")
	fs.Var(&envSet, "env", "")
	fs.Var(&envRm, "env-rm", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return 2
	}
	in := appagent.Input{ID: id, Name: *name, Command: *command, Args: argList, Policy: *policy, Env: map[string]string{},
		AutoStart: *autoStart, Defaults: map[string]string{}}
	if *autoStart && *noAutoStart {
		fmt.Fprintln(stderr, CLIName+": --auto-start and --no-auto-start contradict each other")
		return 2
	}
	for _, kv := range defaultSet {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || v == "" {
			fmt.Fprintf(stderr, "%s: --default wants ID=VALUE, got %q\n", CLIName, kv)
			return 2
		}
		in.Defaults[k] = v
	}
	if update {
		cur, err := app.Get(ctx, id)
		if err != nil {
			printErr(stderr, err)
			return 1
		}
		if in.Name == "" {
			in.Name = cur.Name
		}
		if in.Command == "" {
			in.Command = cur.Command
		}
		if len(argList) == 0 && !*clearArgs {
			in.Args = cur.Args
		}
		if in.Policy == "" {
			in.Policy = cur.Policy
		}
		in.AutoStart = (cur.AutoStart || *autoStart) && !*noAutoStart
		if len(defaultSet) == 0 && !*clearDefaults {
			in.Defaults = cur.Defaults
		}
		// Stored refs ride back unchanged; the repository keeps them.
		for _, e := range cur.Env {
			if e.Ref != "" {
				in.Env[e.Name] = e.Ref
			}
		}
	} else if *clearArgs || len(envRm) > 0 || *noAutoStart || *clearDefaults {
		fmt.Fprintln(stderr, CLIName+": --clear-args, --env-rm, --no-auto-start and --clear-defaults are set-only flags")
		return 2
	}
	for _, kv := range envSet {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			fmt.Fprintf(stderr, "%s: --env wants NAME=VALUE, got %q\n", CLIName, kv)
			return 2
		}
		in.Env[k] = v
	}
	for _, k := range envRm {
		delete(in.Env, k)
	}
	v, err := app.Upsert(ctx, in)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	printAgent(stdout, v)
	return 0
}
