// Package attachmentfs saves image and file attachments to disk so
// file-based tools (shell, python, etc.) can access them by absolute path.
package attachmentfs

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

// Store implements application.AttachmentStore using the filesystem.
// Files are saved under <root>/<conversationID>/<name>.
type Store struct {
	root string
}

// New creates a Store rooted at dir. The directory is created if missing.
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("attachmentfs: root directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("attachmentfs: create root: %w", err)
	}
	return &Store{root: dir}, nil
}

// Save writes the attachment data to disk and returns the absolute path.
// Text attachments are skipped (returns ""). Image and file attachments
// are decoded from their DataURL and written as binary files.
func (s *Store) Save(conversationID string, att domain.Attachment) (string, error) {
	if att.Type == "text" {
		return "", nil
	}
	if att.DataURL == "" {
		return "", nil
	}
	data, err := decodeDataURL(att.DataURL)
	if err != nil {
		return "", fmt.Errorf("attachmentfs: decode %s: %w", att.Name, err)
	}

	dir := filepath.Join(s.root, conversationID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("attachmentfs: create dir: %w", err)
	}

	path := filepath.Join(dir, att.Name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("attachmentfs: write %s: %w", att.Name, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}

// WriteBytes writes raw bytes under <root>/<conversationID>/<name> using a
// temp-file rename so a retry of the same name is atomic.
func (s *Store) WriteBytes(conversationID, name string, data []byte) (string, error) {
	if conversationID == "" {
		return "", fmt.Errorf("attachmentfs: conversation id is required")
	}
	if name == "" || strings.ContainsRune(name, filepath.Separator) || strings.Contains(name, "..") {
		return "", fmt.Errorf("attachmentfs: invalid name %q", name)
	}
	dir := filepath.Join(s.root, conversationID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("attachmentfs: create dir: %w", err)
	}
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("attachmentfs: write %s: %w", name, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("attachmentfs: rename %s: %w", name, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, nil
	}
	return abs, nil
}

// ReadFile returns the bytes of a previously saved attachment. The path must
// resolve under the store root.
func (s *Store) ReadFile(absPath string) ([]byte, error) {
	clean, err := filepath.Abs(absPath)
	if err != nil {
		return nil, fmt.Errorf("attachmentfs: resolve path: %w", err)
	}
	root, err := filepath.Abs(s.root)
	if err != nil {
		return nil, fmt.Errorf("attachmentfs: resolve root: %w", err)
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("attachmentfs: path is outside the attachments store")
	}
	data, err := os.ReadFile(clean)
	if err != nil {
		return nil, fmt.Errorf("attachmentfs: read: %w", err)
	}
	return data, nil
}

func decodeDataURL(dataURL string) ([]byte, error) {
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return nil, fmt.Errorf("invalid data URL")
	}
	return base64.StdEncoding.DecodeString(data)
}
