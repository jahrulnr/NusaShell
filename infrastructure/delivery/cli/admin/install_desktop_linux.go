//go:build linux

package admin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	repoappicon "github.com/jahrulnr/nusashell/infrastructure/repository/appicon"
)

// installShortcut drops the WebUI into the applications menu: a
// .desktop entry under the user's applications dir plus the icon the
// menu resolves by name, then a best-effort icon-cache refresh.
func installShortcut(url string) error {
	share, err := userShareDir()
	if err != nil {
		return err
	}
	desktop := filepath.Join(share, "applications", "nusashell.desktop")
	if err := os.MkdirAll(filepath.Dir(desktop), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(desktop, []byte(desktopEntry(url)), 0o644); err != nil {
		return err
	}
	iconTheme := filepath.Join(share, "icons", "hicolor")
	if _, err := repoappicon.Write(filepath.Join(iconTheme, "256x256", "apps")); err != nil {
		return err
	}
	refreshIconCache(iconTheme)
	return nil
}

// refreshIconCache asks GTK to rebuild the theme cache — best-effort,
// absent on headless or non-GTK desktops.
func refreshIconCache(theme string) {
	if path, err := exec.LookPath("gtk-update-icon-cache"); err == nil {
		_ = exec.Command(path, "-f", "-t", theme).Run()
	}
}

// removeShortcut deletes the menu entry and the icon it resolves.
// Missing files are not an error.
func removeShortcut() error {
	share, err := userShareDir()
	if err != nil {
		return err
	}
	var first error
	for _, path := range []string{
		filepath.Join(share, "applications", "nusashell.desktop"),
		filepath.Join(share, "icons", "hicolor", "256x256", "apps", "nusashell.png"),
	} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	refreshIconCache(filepath.Join(share, "icons", "hicolor"))
	return first
}
