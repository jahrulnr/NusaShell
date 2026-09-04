package application

import (
	"os"
	"path/filepath"
	"strings"
)

// RemoveOrphanJournalSidecars deletes leftover conversations/*.journal
// directories from the retired ChangeJournal. Best-effort and non-fatal.
func RemoveOrphanJournalSidecars(dataDir string) int {
	if strings.TrimSpace(dataDir) == "" {
		return 0
	}
	matches, err := filepath.Glob(filepath.Join(dataDir, "conversations", "*.journal"))
	if err != nil {
		return 0
	}
	removed := 0
	for _, p := range matches {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			continue
		}
		removed++
	}
	return removed
}
