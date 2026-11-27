// Package repoupload stores owner uploads as files: <root>/<id>/<name>
// plus <root>/<id>/meta.json holding the name, media type and sha256.
package repoupload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

type Store struct{ root string }

func New(root string) (drepo.Uploads, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Store{root: abs}, nil
}

type meta struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	SHA256    string `json:"sha256"`
	File      string `json:"file"`
}

func (s *Store) Save(ctx context.Context, name, mediaType string, data []byte) (string, valueobject.Attachment, error) {
	if err := ctx.Err(); err != nil {
		return "", valueobject.Attachment{}, err
	}
	id, err := valueobject.NewID("u", "")
	if err != nil {
		return "", valueobject.Attachment{}, err
	}
	dir := filepath.Join(s.root, id.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", valueobject.Attachment{}, err
	}
	// The stored name is a plain base name; anything path-shaped is cut.
	file := filepath.Base(filepath.Clean("/" + strings.ReplaceAll(name, `\`, "/")))
	if file == "/" || file == "." || file == "meta.json" {
		file = "upload"
	}
	sum := sha256.Sum256(data)
	m := meta{Name: name, MediaType: mediaType, SHA256: hex.EncodeToString(sum[:]), File: file}
	if err := os.WriteFile(filepath.Join(dir, file), data, 0o600); err != nil {
		return "", valueobject.Attachment{}, err
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o600); err != nil {
		return "", valueobject.Attachment{}, err
	}
	a, err := s.attachment(dir, m)
	return id.String(), a, err
}

func (s *Store) Get(ctx context.Context, id string) (valueobject.Attachment, error) {
	if err := ctx.Err(); err != nil {
		return valueobject.Attachment{}, err
	}
	if valueobject.ValidID(id) != nil || !strings.HasPrefix(id, "u_") {
		return valueobject.Attachment{}, apperror.NewNotFoundError("upload "+id, nil)
	}
	dir := filepath.Join(s.root, id)
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return valueobject.Attachment{}, apperror.NewNotFoundError("upload "+id, err)
	}
	if err != nil {
		return valueobject.Attachment{}, err
	}
	var m meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return valueobject.Attachment{}, fmt.Errorf("upload %s: %w", id, err)
	}
	return s.attachment(dir, m)
}

// Read returns an upload's stored bytes and media type.
func (s *Store) Read(ctx context.Context, id string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if valueobject.ValidID(id) != nil || !strings.HasPrefix(id, "u_") {
		return nil, "", apperror.NewNotFoundError("upload "+id, nil)
	}
	dir := filepath.Join(s.root, id)
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", apperror.NewNotFoundError("upload "+id, err)
	}
	if err != nil {
		return nil, "", err
	}
	var m meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, "", fmt.Errorf("upload %s: %w", id, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, m.File))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", apperror.NewNotFoundError("upload "+id, err)
		}
		return nil, "", err
	}
	return data, m.MediaType, nil
}

func (s *Store) attachment(dir string, m meta) (valueobject.Attachment, error) {
	a, err := valueobject.NewAttachment("file://"+filepath.Join(dir, m.File), m.MediaType)
	if err != nil {
		return a, err
	}
	a.SetName(m.Name)
	a.SetSHA256(m.SHA256)
	return a, nil
}

// Delete removes one upload and everything stored under its id. A valid id that
// no longer exists is already the requested state and answers nil, so cleanup
// stays idempotent; an id that is not an upload id is a 404.
//
// The id is the whole address on purpose: this store is not room-scoped, and it
// does not need to be. Every id is minted here and handed to exactly one caller,
// which either stores the attachment on its message or takes its own copy back
// after a refusal — so a caller only ever holds ids this store gave it, and the
// one bulk path is Purge, which runs once the rooms that referenced the files
// are gone. What such an id must never be is caller-shaped: the id check keeps
// Delete inside the root, which is why a room id, a path, or an absolute path
// answers not-found instead of naming a directory to remove.
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if valueobject.ValidID(id) != nil || !strings.HasPrefix(id, "u_") {
		return apperror.NewNotFoundError("upload "+id, nil)
	}
	err := os.RemoveAll(filepath.Join(s.root, id))
	if err != nil {
		return err
	}
	return nil
}

// Purge removes every stored upload and leaves an empty root.
func (s *Store) Purge(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.RemoveAll(s.root); err != nil {
		return err
	}
	return os.MkdirAll(s.root, 0o700)
}
