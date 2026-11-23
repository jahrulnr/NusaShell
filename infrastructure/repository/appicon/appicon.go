// Package repoappicon keeps the NusaShell icon as a file. A desktop
// notification names its icon by path, and the daemon or the shell that draws
// it reads that file itself, so the embedded PNG has to exist on disk.
package repoappicon

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed nusashell.png
var icon []byte

// File is the icon's name inside the directory it is written to.
const File = "nusashell.png"

// Write puts the icon in dir and returns its path. A file already holding the
// icon is left alone; a different or damaged one is replaced through a staged
// rename, so a reader never meets a half-written image.
func Write(dir string) (string, error) {
	path := filepath.Join(dir, File)
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, icon) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, File+".tmp-*")
	if err != nil {
		return "", err
	}
	_, err = tmp.Write(icon)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		// Notification daemons run as the same user but may be a different
		// process tree, so the file stays readable beyond its creator.
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}
