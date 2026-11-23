//go:build !windows

package admin

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jahrulnr/nusashell/infrastructure/system"
)

// installedBinHint is where install puts the binary, for help text.
const installedBinHint = "~/.local/bin/" + CLIName

func userBinPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(home, ".local", "bin", CLIName))
}

func userConfigPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Abs(filepath.Join(dir, "nusashell", "config.json"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(home, ".config", "nusashell", "config.json"))
}

// userShareDir is the XDG data root the data dir and the desktop
// integration share — ~/.local/share unless XDG_DATA_HOME moves it.
func userShareDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(home, ".local", "share"))
}

func userDataDir() (string, error) {
	share, err := userShareDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(share, "nusashell"), nil
}

// pathAdvice is the shell line a user pastes to reach the installed
// binary in the current terminal.
func pathAdvice(dir string) string {
	return fmt.Sprintf("export PATH=%q", dir+":$PATH")
}

// samePath compares PATH entries — case-sensitive where filesystems are.
func samePath(a, b string) bool { return a == b }

// prepareBinaryInstall is a no-op where a running binary keeps its old
// inode through a rename — only Windows needs the image freed first.
func prepareBinaryInstall(system.Service, string, io.Writer) error {
	return nil
}

// servicePath is the PATH the unit bakes in for the daemon and its
// children: stable user bin dirs first, then the installer's own PATH.
func servicePath(home string) string {
	return home + "/.local/bin:" + home + "/bin:" + home + "/go/bin:" +
		cmp.Or(os.Getenv("PATH"), "/usr/local/bin:/usr/bin:/bin")
}

func removeInstalledBinary(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file; left in place", path)
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
