package journal

import (
	"fmt"
	"os"
	"path/filepath"
)

type blobStore struct {
	dir string
}

func newBlobStore(sidecarDir string) *blobStore {
	return &blobStore{dir: filepath.Join(sidecarDir, "blobs")}
}

func (b *blobStore) path(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(b.dir, hash)
	}
	return filepath.Join(b.dir, hash[:2], hash)
}

func (b *blobStore) put(hash string, data []byte) error {
	if hash == "" {
		return nil
	}
	final := b.path(hash)
	if _, err := os.Stat(final); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func (b *blobStore) get(hash string) ([]byte, error) {
	if hash == "" {
		return nil, nil
	}
	return os.ReadFile(b.path(hash))
}

func (b *blobStore) exists(hash string) bool {
	if hash == "" {
		return false
	}
	_, err := os.Stat(b.path(hash))
	return err == nil
}

func sidecarPath(dataDir, conversationID string) (string, error) {
	if err := safeSegment(conversationID); err != nil {
		return "", fmt.Errorf("journal: conversation %w", err)
	}
	return filepath.Join(dataDir, "conversations", conversationID+".journal"), nil
}

func ensureSidecarDir(dataDir, conversationID string) (string, error) {
	dir, err := sidecarPath(dataDir, conversationID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
