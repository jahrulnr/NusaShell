package admin

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	servicebrowser "github.com/jahrulnr/nusashell/infrastructure/service/browser"
)

const browserUsage = `usage: ` + CLIName + ` browser <status|install> [method]

The Chrome or Chromium the threads' browser runs: the one thing the binary
cannot carry. Works without a running server.
  status            which executable resolves, or how to get one
  install [method]  run an install method (default: the first offered)
`

// Browser finds or installs the browser executable.
func Browser(args []string, stdout, stderr io.Writer) int {
	op := "status"
	if len(args) > 0 {
		op = args[0]
	}
	if op == "-h" || op == "--help" || op == "help" {
		fmt.Fprint(stdout, browserUsage)
		return 0
	}
	if (op != "status" && op != "install") || len(args) > 2 || (op == "status" && len(args) > 1) {
		fmt.Fprint(stderr, browserUsage)
		return 2
	}
	cfg, _, err := LoadConfig(false)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	m := servicebrowser.New(servicebrowser.Options{ExecPath: cfg.Browser.Exec, InstallDir: filepath.Join(cfg.Storage.Dir, "chrome")})
	defer m.Shutdown()
	offer := m.InstallOffer()
	if !offer.Missing {
		fmt.Fprintln(stdout, "Browser executable:", offer.ExecPath)
		return 0
	}
	if op == "status" {
		fmt.Fprintln(stdout, "No Chrome or Chromium found. Searched:", offer.Searched)
		if len(offer.Methods) == 0 {
			fmt.Fprintln(stdout, "\nNo install method works on this host. Install Chrome or Chromium, then put it on PATH or set browser.exec.")
			return 0
		}
		fmt.Fprintln(stdout, "\nInstall options:")
		for _, me := range offer.Methods {
			root := ""
			if me.NeedsRoot {
				root = " (needs root)"
			}
			fmt.Fprintf(stdout, "  %s — %s%s\n      %s\n", me.ID, me.Label, root, me.Detail)
		}
		fmt.Fprintf(stdout, "\nRun `%s browser install` for the first, or `%s browser install <id>`.\n", CLIName, CLIName)
		if offer.Proot {
			fmt.Fprintln(stdout, "proot: only a package manager works here (there is no linux/arm64 Chrome build), and the browser runs without its sandbox.")
		}
		return 0
	}
	if len(offer.Methods) == 0 {
		fmt.Fprintln(stderr, "nusashell: no install method works on this host; install Chrome or Chromium, then put it on PATH or set browser.exec")
		return 1
	}
	method := offer.Methods[0].ID
	if len(args) == 2 {
		method = args[1]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	p, err := m.Install(ctx, method, stdout)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Done. Threads use", p, "from their next browser session.")
	return 0
}
