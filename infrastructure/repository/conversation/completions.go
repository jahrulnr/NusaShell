package repoconversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// completionsFile parks a room's unread asynchronous finishes beside its record.
// A turn saves the whole conversation when it ends, so a finish reported while a
// turn runs has no owner in the record; and a finish reported between an exit and
// the turn that would read it must outlive the process, or the agent never learns
// that the work it started is done. The file has neither problem.
const completionsFile = "completions.json"

// completionBook is the file's shape: the records still waiting, and the keys
// ever recorded — one exit is one record, even across a restart.
type completionBook struct {
	Seen    []completionKey          `json:"seen,omitempty"`
	Pending []valueobject.Completion `json:"pending,omitempty"`
}

// completionKey is one value identity in its stored form: a JSON list of
// "source\x00id" strings would hide the NUL, one object per key does not.
type completionKey struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

// SaveCompletions appends the finishes not yet known to the room's book, in
// arrival order, and answers whether anything was new.
func (r *ConversationRepository) SaveCompletions(ctx context.Context, convID string, list []valueobject.Completion) (bool, error) {
	if err := validID(convID); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return false, err
	}
	defer unlock()

	dir := r.convDir(convID)
	if _, err := os.Stat(filepath.Join(dir, metaFile)); errors.Is(err, os.ErrNotExist) {
		return false, apperror.NewNotFoundError("conversation "+convID, err)
	} else if err != nil {
		return false, err
	}
	book, _, err := readCompletions(dir)
	if err != nil {
		return false, err
	}
	known := make(map[string]bool, len(book.Seen))
	for _, k := range book.Seen {
		known[k.Source+"\x00"+k.ID] = true
	}
	added := false
	for _, c := range list {
		if c.Source == "" || c.ID == "" {
			continue
		}
		key := c.Key()
		if known[key] {
			continue
		}
		known[key] = true
		book.Seen = append(book.Seen, completionKey{Source: c.Source, ID: c.ID})
		if c.At.IsZero() {
			c.At = time.Now().UTC()
		}
		book.Pending = append(book.Pending, c)
		added = true
	}
	if !added {
		return false, nil
	}
	raw, err := json.Marshal(book)
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(filepath.Join(dir, completionsFile), raw, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// PendingCompletions reads the room's unread finishes, oldest first.
func (r *ConversationRepository) PendingCompletions(ctx context.Context, convID string) ([]valueobject.Completion, error) {
	if err := validID(convID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := r.convDir(convID)
	if _, err := os.Stat(filepath.Join(dir, metaFile)); errors.Is(err, os.ErrNotExist) {
		return nil, apperror.NewNotFoundError("conversation "+convID, err)
	} else if err != nil {
		return nil, err
	}
	book, ok, err := readCompletions(dir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return book.Pending, nil
}

// ClearCompletions drops the rendered records and keeps their keys: an exit the
// room has already read must not be parked again by a replay.
func (r *ConversationRepository) ClearCompletions(ctx context.Context, convID string, keys []string) error {
	if err := validID(convID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return err
	}
	defer unlock()

	dir := r.convDir(convID)
	book, ok, err := readCompletions(dir)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	kept := book.Pending[:0]
	for _, c := range book.Pending {
		if !drop[c.Key()] {
			kept = append(kept, c)
		}
	}
	if len(kept) == len(book.Pending) {
		// Nothing rendered here: the book stays as it was, byte for byte.
		return nil
	}
	book.Pending = kept
	// The keys stay: one exit is one record, ever, so a producer replaying an
	// exit the room already read must not park it again. The file goes with the
	// room, not with the last record it rendered.
	raw, err := json.Marshal(book)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, completionsFile), raw, 0o600)
}

// DropCompletions removes the book with the room. It is idempotent: a room that
// never reported a finish has no file to remove.
func (r *ConversationRepository) DropCompletions(ctx context.Context, convID string) error {
	if err := validID(convID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.Remove(filepath.Join(r.convDir(convID), completionsFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// readCompletions decodes one room directory's book. A missing file is not an
// error; a corrupt one is — a finish nobody can read must be said out loud,
// never silently dropped.
func readCompletions(dir string) (completionBook, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, completionsFile))
	if errors.Is(err, os.ErrNotExist) {
		return completionBook{}, false, nil
	}
	if err != nil {
		return completionBook{}, false, err
	}
	var book completionBook
	if err := json.Unmarshal(raw, &book); err != nil {
		return completionBook{}, false, fmt.Errorf("pending completions corrupt: %w", err)
	}
	return book, true, nil
}
