//go:build !windows

package repobinary

import "os"

// commitStaged replaces the installed binary atomically. A running process
// keeps the old inode, so the next launch runs the new file.
func commitStaged(staged, path string) error {
	return os.Rename(staged, path)
}
