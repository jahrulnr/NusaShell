//go:build !darwin

package service

// newDarwinManager keeps the platform dispatch compiling on non-macOS GOOS.
func newDarwinManager(opts Options, run Runner) Manager {
	return unsupportedManager{}
}
