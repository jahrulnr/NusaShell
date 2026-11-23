//go:build windows

package repobinary

import "os"

// stagedSuffix is the file the Windows restart helper swaps into place after
// this process exits. The running image is locked against replacement, so
// Commit stages beside it instead of renaming over it.
const stagedSuffix = ".new"

// commitStaged leaves the new binary at <path>.new. The restart helper in
// infrastructure/system waits for this process to exit, then moves it over
// the installed executable and starts the installed task or process.
func commitStaged(staged, path string) error {
	target := path + stagedSuffix
	// A staged file left by an earlier failed attempt is not running; drop
	// it before the rename.
	_ = os.Remove(target)
	return os.Rename(staged, target)
}
