//go:build !linux

package service

// newLinuxManager keeps the platform dispatch compiling on non-Linux GOOS.
func newLinuxManager(opts Options, run Runner) Manager {
	return unsupportedManager{}
}
