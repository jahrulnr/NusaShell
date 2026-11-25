package system

import "os"

// Restarter brings the running WebUI onto a freshly installed executable. A
// process the host service manager owns is restarted through the manager. A
// foreground run has no manager, so it replaces itself in place with the
// installed file.
type Restarter struct {
	svc    Service
	name   string
	path   string
	argv   []string
	reexec func(path string, argv []string) error
}

// NewRestarter binds the restart to the registration name, the installed
// executable, and the argv a replacement starts with.
func NewRestarter(svc Service, name, path string, argv []string) Restarter {
	return Restarter{svc: svc, name: name, path: path, argv: argv, reexec: Reexec}
}

// Mode names the path a restart takes: "service" when this process IS the
// unit's main process, "process" for anything else. An active unit is not the
// same fact — a child the service started (an agent running `nusashell serve`
// from a temp dir) inherits its invocation id and is active too. Asking the
// manager for MainPID is what tells them apart; getting it wrong restarts the
// real service and replaces its installed binary from a test instance.
func (r Restarter) Mode() string {
	if r.svc.MainPID(r.name) == os.Getpid() {
		return "service"
	}
	return "process"
}

// Restart performs the restart for mode. Platform files own the mechanics:
// Unix asks the manager or execs in place; Windows leaves the staged
// <path>.new for a detached helper to swap after this process exits.
