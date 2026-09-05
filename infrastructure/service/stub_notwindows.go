//go:build !windows

package service

// newWindowsManager keeps the platform dispatch compiling on non-Windows GOOS.
func newWindowsManager(opts Options, run Runner) Manager {
	return unsupportedManager{}
}
