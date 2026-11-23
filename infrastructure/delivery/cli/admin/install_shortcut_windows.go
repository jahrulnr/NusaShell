//go:build windows

package admin

import (
	"errors"
	"os"
	"path/filepath"
)

// startMenuShortcut is the .url entry under the user's Start Menu.
func startMenuShortcut() (string, error) {
	roaming, err := roamingAppData()
	if err != nil {
		return "", err
	}
	return filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", ProductName+".url"), nil
}

// installShortcut writes a Start Menu entry that opens the WebUI in
// the default browser.
func installShortcut(url string) error {
	link, err := startMenuShortcut()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	return os.WriteFile(link, []byte(internetShortcut(url)), 0o644)
}

// removeShortcut deletes the Start Menu entry. A missing file is not
// an error.
func removeShortcut() error {
	link, err := startMenuShortcut()
	if err != nil {
		return err
	}
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
