// Command nusashell loads configuration, wires repositories, service adapters,
// tools, and applications, then serves the local WebUI.
// Admin subcommands are routes on the CLI delivery boundary — this file
// is the mux, not the handler.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/cli/admin"
	deliverymcp "github.com/jahrulnr/nusashell/infrastructure/delivery/mcp"
	repofiletree "github.com/jahrulnr/nusashell/infrastructure/repository/filetree"
	"github.com/jahrulnr/nusashell/infrastructure/system"

	"go.uber.org/automaxprocs/maxprocs"
	// Automation schedules name IANA zones; embed the database so hosts
	// without one (Windows, slim containers) still resolve them.
	_ "time/tzdata"
)

// version is the release version, set when the binary is linked
// (-ldflags "-X main.version=..."). A source build without it keeps "dev",
// and "dev" switches the update check off.
var version = "dev"

func main() {
	// automaxprocs sizes GOMAXPROCS from the container's CPU quota. Its
	// default logger announces the outcome on every start ("Leaving
	// GOMAXPROCS=12: CPU quota undefined"), which is noise for a local
	// harness; the adjustment still applies, the chatter does not.
	_, _ = maxprocs.Set(maxprocs.Logger(func(string, ...any) {}))
	fileTree := repofiletree.NewLocal()
	args := os.Args[1:]
	// People repeat the invocation target via habit — the binary name, or
	// a `go run` source path typed at the built binary — and `go run
	// ./cmd/nusashell` itself substitutes a temp binary for argv[0], so the
	// invoked name is matched rather than assumed. Strip a leading token
	// while it is the program name or a file that exists on disk; a token
	// that merely ends in .go is an argument, not noise. "core" is still
	// stripped for old scripts.
	for len(args) > 0 {
		tok := args[0]
		isFile := false
		if fi, err := fileTree.Stat(tok); err == nil && !fi.IsDir() {
			isFile = true
		}
		if tok != admin.CLIName && tok != "core" && tok != filepath.Base(os.Args[0]) && !isFile {
			break
		}
		args = args[1:]
	}
	if len(args) > 0 {
		switch args[0] {
		case "python":
			os.Exit(runPython(args[1:]))
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		case "onboard":
			os.Exit(admin.Onboard(os.Stdin, os.Stdout, os.Stderr))
		case "provider":
			os.Exit(admin.Provider(args[1:], os.Stdin, os.Stdout, os.Stderr))
		case "agent":
			os.Exit(admin.Agent(args[1:], os.Stdout, os.Stderr))
		case "browser":
			os.Exit(admin.Browser(args[1:], os.Stdout, os.Stderr))
		case "channel":
			os.Exit(admin.Channel(args[1:], os.Stdin, os.Stdout, os.Stderr))
		case "automation":
			os.Exit(admin.Automation(args[1:], os.Stdout, os.Stderr))
		case "model":
			os.Exit(admin.Model(args[1:], os.Stdout, os.Stderr))
		case "conversations":
			os.Exit(admin.Conversations(args[1:], os.Stdout, os.Stderr))
		case "password":
			os.Exit(admin.Password(args[1:], os.Stdin, os.Stdout, os.Stderr))
		case "config":
			os.Exit(admin.Config(args[1:], os.Stdout, os.Stderr))
		case "conversation":
			if len(args) > 1 && args[1] == "repair" {
				os.Exit(admin.ConversationRepair(args[2:], os.Stdout, os.Stderr))
			}
			fmt.Fprintln(os.Stderr, "usage: nusashell conversation repair <id> --discard-duplicate|--commit|--finish-rewrite")
			os.Exit(2)
		case "mcp":
			// `mcp run` is what an agent starts to reach the server's
			// built-in tool servers; it speaks MCP on stdio.
			if len(args) < 2 || args[1] != "run" {
				os.Exit(admin.MCP(args[1:], os.Stdout, os.Stderr))
			}
			if len(args) != 3 {
				fmt.Fprintln(os.Stderr, "usage: nusashell mcp run <conversation|browser> (started by NusaShell for its agents)")
				os.Exit(2)
			}
			if err := deliverymcp.Relay(context.Background(), os.Stdin, os.Stdout, os.Getenv(deliverymcp.EnvURL), os.Getenv(deliverymcp.EnvToken), args[2]); err != nil {
				fmt.Fprintln(os.Stderr, admin.CLIName+":", err)
				os.Exit(1)
			}
			return
		case "serve":
			if len(args) > 1 && admin.IsServeAdmin(args[1]) {
				os.Exit(admin.ServeAdmin(args[1:], os.Stdout, os.Stderr, system.New()))
			}
			if err := serve(args[1:], system.New()); err != nil {
				fmt.Fprintln(os.Stderr, admin.CLIName+":", err)
				os.Exit(1)
			}
			return
		case "version":
			os.Exit(admin.Version(args[1:], os.Stdout, os.Stderr, version))
		case "uninstall":
			os.Exit(admin.Uninstall(args[1:], os.Stdout, os.Stderr, system.New()))
		default:
			fmt.Fprintf(os.Stderr, "%s: unknown argument %q — try --help\n", admin.CLIName, args[0])
			os.Exit(2)
		}
	}
	if err := serve(nil, system.New()); err != nil {
		fmt.Fprintln(os.Stderr, admin.CLIName+":", err)
		os.Exit(1)
	}
}

var usage = admin.ProductName + ` — local agent workspace

Usage:
  ` + admin.CLIName + `                  local WebUI (same as serve)
  ` + admin.CLIName + ` onboard          guided provider setup wizard
  ` + admin.CLIName + ` provider <...>   manage API providers — list/show/add/set/delete
  ` + admin.CLIName + ` agent <...>      manage external ACP agents — list/show/detect/add/set/remove
  ` + admin.CLIName + ` password         set the owner password for WebUI logins
  ` + admin.CLIName + ` config <...>     inspect and patch the config file — show/path/set/unset/validate
  ` + admin.CLIName + ` python [args...] run Python; reuse a suitable host interpreter or download a private standalone runtime
  ` + admin.CLIName + ` conversation repair <id> --discard-duplicate|--commit|--finish-rewrite (offline recovery)
  ` + admin.CLIName + ` serve [--port N] WebUI on server.bind:server.port; login per server.auth
  ` + admin.CLIName + ` serve install|start|stop|status
                               install the WebUI for the current user; manage the host service
  ` + admin.CLIName + ` mcp <...>        manage MCP servers — list/show/add/set/remove
  ` + admin.CLIName + ` browser <...>    find or install the Chrome the threads' browser runs — status/install
  ` + admin.CLIName + ` channel <...>    WhatsApp/Telegram through the running server — list/show/set/send/search/contacts/groups/user
  ` + admin.CLIName + ` automation <...> scheduled prompts through the running server — list/get/add/set/pause/resume/remove/history/run
  ` + admin.CLIName + ` model <thread> [sel]  a thread's runtime binding — agent:<id>[/<model>] or <provider>/<model>
  ` + admin.CLIName + ` conversations <...>
                               work threads through the MCP endpoint — list/projects/search/export/read/wait/send/stop/spawn/report
  ` + admin.CLIName + ` uninstall        remove the binary and the WebUI service; config and data stay
  ` + admin.CLIName + ` version          print the build version
  ` + admin.CLIName + ` --help           this text

Environment:
  CONFIG_PATH   JSON config path. The installed binary defaults to
                ~/.config/nusashell/config.json. A source build still takes
                CONFIG_PATH from a .env file in the working directory.
                An exported CONFIG_PATH always wins. A missing file is
                generated once at startup

Provider setup (once per provider):
  ` + admin.CLIName + ` provider add openai --style completion \
      --endpoint https://api.openai.com/v1 --models gpt-5 --key-stdin
`
