//go:build !windows

package system

// Restart performs the restart for mode. The "process" path does not return on
// success: the process it replaces is this one.
func (r Restarter) Restart(mode string) error {
	if mode == "service" {
		return r.svc.Restart(r.name)
	}
	return r.reexec(r.path, r.argv)
}
