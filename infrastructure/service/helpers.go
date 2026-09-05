package service

import (
	"fmt"
	"os"
)

// assertNotSymlink refuses to rewrite a symlinked managed file: the link
// target would be owned by something else (openclaw guard).
func assertNotSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to rewrite symlinked managed service file: %s", path)
	}
	return nil
}

// backupExisting renames an existing definition to <path>.bak before a
// rewrite so previous contents survive upgrade reruns.
func backupExisting(path string) error {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Rename(path, path+".bak")
}

// atomicWrite writes via a temp file in the same directory then renames, so
// a crash never leaves a truncated definition behind.
func atomicWrite(path string, content []byte, perm os.FileMode) error {
	tmp := path + ".nusashell-tmp"
	if err := os.WriteFile(tmp, content, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// readDefinition snapshots an existing file (nil when absent) so failed
// activations can restore the prior state.
func readDefinition(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return content, nil
}

// ensureDataDir creates the supervised data directory with the same
// ownership contract as the server (0700).
func ensureDataDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}
