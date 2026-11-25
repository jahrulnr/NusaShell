package system

import (
	"os"
	"path/filepath"
)

// SelfPath resolves the running executable, symlinks followed: an install
// reached through a PATH shim must compare equal to the file it points at.
func SelfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// SameBinary reports whether two paths name the same file, symlinks and
// relative forms resolved. A path that cannot be resolved is not the same
// file — an unknown answer must never allow a replace.
func SameBinary(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}
	fa, err := os.Stat(ra)
	if err != nil {
		return false
	}
	fb, err := os.Stat(rb)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}
