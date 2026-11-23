//go:build !linux && !windows

package system

// No supported manager yet on this platform — launchd.go for macOS is
// the next candidate. An empty list makes New report the platform as
// unavailable while keeping non-service commands (install
// --no-service, uninstall, serve) working.
var candidates []candidate
