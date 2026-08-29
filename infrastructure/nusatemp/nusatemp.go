// Package nusatemp is the single platform-temp root for NusaShell runtime
// scratch: tool overflow spills, installer staging, and whisper work dirs.
//
// Path is filepath.Join(os.TempDir(), "nusashell") — %TEMP%\nusashell on
// Windows. Callers must not dump files at the temp root.
package nusatemp

import (
	"os"
	"path/filepath"
)

// DirName is the folder under the platform temp directory.
const DirName = "nusashell"

// Path is the absolute runtime temp directory. It does not create it.
func Path() string {
	return filepath.Join(os.TempDir(), DirName)
}

// Dir returns Path after ensuring it exists with 0700.
func Dir() (string, error) {
	dir := Path()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// MkdirTemp creates a unique directory under Dir().
func MkdirTemp(pattern string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, pattern)
}

// CreateTemp creates a unique file under Dir().
func CreateTemp(pattern string) (*os.File, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, pattern)
}
