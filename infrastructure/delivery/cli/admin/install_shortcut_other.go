//go:build !linux && !windows

package admin

// No menu integration on this platform yet — install still succeeds.
func installShortcut(string) error { return nil }
func removeShortcut() error        { return nil }
