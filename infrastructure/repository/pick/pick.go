// Package repopick stores live-page picks as files: <root>/<id>.json,
// written once through a temp file and a rename.
package repopick

import (
	"context"
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

func New(root string) (drepo.Picks, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Store{root: abs}, nil
}

type record struct {
	ID             string                           `json:"id"`
	ConversationID string                           `json:"conversationId"`
	Marker         int                              `json:"marker"`
	Upload         string                           `json:"upload"`
	Descriptor     valueobject.AnnotationDescriptor `json:"descriptor"`
}

func (s *Store) Save(ctx context.Context, p valueobject.Pick) (valueobject.Pick, error) {
	if err := ctx.Err(); err != nil {
		return p, err
	}
	id, err := valueobject.NewID("pk", "")
	if err != nil {
		return p, err
	}
	p.ID = id.String()
	raw, err := json.Marshal(record{ID: p.ID, ConversationID: p.ConversationID, Marker: p.Marker, Upload: p.Upload, Descriptor: p.Descriptor})
	if err != nil {
		return p, err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return p, err
	}
	tmp, err := os.CreateTemp(s.root, ".pick-*")
	if err != nil {
		return p, err
	}
	_, werr := tmp.Write(raw)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), filepath.Join(s.root, p.ID+".json"))
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return p, werr
	}
	return p, nil
}

func (s *Store) Get(ctx context.Context, id string) (valueobject.Pick, error) {
	if err := ctx.Err(); err != nil {
		return valueobject.Pick{}, err
	}
	if valueobject.ValidID(id) != nil || !strings.HasPrefix(id, "pk_") {
		return valueobject.Pick{}, apperror.NewNotFoundError("pick "+id, nil)
	}
	raw, err := os.ReadFile(filepath.Join(s.root, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return valueobject.Pick{}, apperror.NewNotFoundError("pick "+id, err)
	}
	if err != nil {
		return valueobject.Pick{}, err
	}
	var r record
	if err := json.Unmarshal(raw, &r); err != nil {
		return valueobject.Pick{}, fmt.Errorf("pick %s: %w", id, err)
	}
	return valueobject.Pick{ID: r.ID, ConversationID: r.ConversationID, Marker: r.Marker, Upload: r.Upload, Descriptor: r.Descriptor}, nil
}

// Purge removes every stored pick and leaves an empty root.
func (s *Store) Purge(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.RemoveAll(s.root); err != nil {
		return err
	}
	return os.MkdirAll(s.root, 0o700)
}
