package main

import (
	"flag"
	"fmt"
	"os"

	"nusashell/infrastructure/service"
)

const serviceUsage = `Usage: nusashell service <action> [--binary PATH]

Install, inspect, or control the per-user NusaShell core service
(systemd user unit on Linux, LaunchAgent on macOS, Scheduled Task on
Windows). The service starts the server at login and restarts it if it
exits. Everything runs unprivileged in your session.

Actions:
  install     Create the service definition and start it
  uninstall   Stop the service and remove its definition
  status      Show installed/loaded/running state and definition drift
  start       Start the service
  stop        Stop the service
  restart     Restart the service

Options:
  --binary PATH   Path to the nusashell binary to supervise
                  (default: the current install of this binary)

Environment:
  NUSASHELL_DATA_DIR, NUSASHELL_HOST, NUSASHELL_PORT, and
  NUSASHELL_ALLOW_REMOTE are baked into the service at install time.
`

// serviceCmd handles the `nusashell service <action>` dispatch. It is a
// process-level workflow: service definitions live outside the running
// server and cannot be managed through RPC.
func serviceCmd(args []string) error {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, serviceUsage) }
	binary := fs.String("binary", "", "path to the nusashell binary to supervise")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("service requires exactly one action\n%s", serviceUsage)
	}
	action := fs.Arg(0)
	opts, err := resolveServiceOptions(*binary)
	if err != nil {
		return err
	}
	mgr := service.New(opts)
	switch action {
	case service.ActionInstall:
		if err := mgr.Install(); err != nil {
			return err
		}
		fmt.Println("NusaShell service installed and started.")
	case service.ActionUninstall:
		if err := mgr.Uninstall(); err != nil {
			return err
		}
		fmt.Println("NusaShell service uninstalled.")
	case service.ActionStatus:
		st, err := mgr.Status()
		if err != nil {
			return err
		}
		printServiceStatus(st)
	case service.ActionStart:
		err = mgr.Start()
	case service.ActionStop:
		err = mgr.Stop()
	case service.ActionRestart:
		err = mgr.Restart()
	default:
		return fmt.Errorf("unknown service action %q\n%s", action, serviceUsage)
	}
	return err
}

func resolveServiceOptions(binaryFlag string) (service.Options, error) {
	binary := binaryFlag
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return service.Options{}, fmt.Errorf("resolve current binary: %w", err)
		}
		binary = service.StableBinaryPath(exe)
	}
	return service.Options{
		BinaryPath:  binary,
		DataDir:     envOr("NUSASHELL_DATA_DIR", defaultDataDir()),
		Host:        os.Getenv("NUSASHELL_HOST"),
		Port:        os.Getenv("NUSASHELL_PORT"),
		AllowRemote: os.Getenv("NUSASHELL_ALLOW_REMOTE") == "1",
	}, nil
}

func printServiceStatus(st service.Status) {
	fmt.Printf("definition: %s\n", st.DefinitionPath)
	fmt.Printf("installed:  %t\n", st.Installed)
	fmt.Printf("loaded:     %t\n", st.Loaded)
	fmt.Printf("running:    %t\n", st.Running)
	if st.Drifted {
		fmt.Println("drift:      definition differs from the current install; run `nusashell service install` to refresh")
	}
}
