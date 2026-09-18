package jsonstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OperationStore persists the net unified diff of one agent turn's committed
// file_* mutations as a single git-appliable patch:
//
//	<dir>/conversations/<conversationID>/operation/<runID>.patch
//
// One file per turn keeps concurrent turns from rewriting shared state, and an
// atomic write (temp file + rename) means a crash mid-write can only damage the
// patch being written. Turns without file mutations leave no file behind.
type OperationStore struct {
	dir string
}

// NewOperationStore creates a per-conversation turn-patch store rooted at dir.
// Directories are created on first write.
func NewOperationStore(dir string) *OperationStore {
	return &OperationStore{dir: dir}
}

// Path resolves the file a turn's patch is (or will be) stored at. Returns ""
// for unsafe or unresolvable IDs.
func (s *OperationStore) Path(conversationID, runID string) string {
	path, err := s.patchPath(conversationID, runID)
	if err != nil {
		return ""
	}
	return path
}

func (s *OperationStore) patchPath(conversationID, runID string) (string, error) {
	dir, err := conversationOperationDir(filepath.Join(s.dir, conversationsDirName), conversationID)
	if err != nil {
		return "", err
	}
	if err := safeSegment(runID); err != nil {
		return "", fmt.Errorf("operation store: run %w", err)
	}
	return filepath.Join(dir, runID+".patch"), nil
}

// Save writes the patch atomically, replacing any previous file for the same
// turn. An empty or whitespace-only patch is a no-op: a turn without file
// mutations has no operation to record.
func (s *OperationStore) Save(conversationID, runID, patch string) error {
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	path, err := s.patchPath(conversationID, runID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWrite(path, []byte(patch))
}
