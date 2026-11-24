package conversation

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/system/patharg"
)

// localPath turns what a thread links into a filesystem path: the same
// expansion the file and media tools use — a file: URL, a leading ~ for the
// owner's home and, on Windows, %VAR% — then a plain path (relative ones
// resolve against the thread's folder).
func localPath(raw string) (string, error) {
	if raw == "" || strings.ContainsRune(raw, 0) {
		return "", errors.New("a file path is required")
	}
	if !strings.HasPrefix(strings.ToLower(raw), "file:") && strings.Contains(raw, "://") {
		return "", errors.New("expected a local file path")
	}
	if strings.HasPrefix(strings.ToLower(raw), "file:") {
		// A "#" is a filename character in what agents hand over, not a
		// fragment marker; encode it so the URL step keeps it.
		raw = strings.ReplaceAll(raw, "#", "%23")
	}
	path, err := patharg.Expand(raw)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", errors.New("a file path is required")
	}
	return filepath.FromSlash(path), nil
}
