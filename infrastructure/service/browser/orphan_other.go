//go:build !linux

package browser

// reapOrphan is a no-op off Linux — the /proc check that proves a lock's
// holder runs our profile dir is Linux-specific, and killing a pid by
// name alone could hit a user's own browser.
func (m *Manager) reapOrphan(string, string) {}
