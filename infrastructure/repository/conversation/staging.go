package repoconversation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// The store keeps one entry of its own beside the room directories: the
// staging area. A room that is being built — a cleared room, a fork — lives
// there until it is complete, and is published with a single rename. Two
// things follow from that: no reader ever sees a directory the store is still
// writing, and a crash leaves either the room as it was or a complete copy
// waiting to be recovered.
//
// The area is a plain hidden directory one level above the rooms, not inside
// them: List reads every directory it finds as a room, and this level holds no
// meta.json, so it is skipped exactly like any other non-room entry.
const stagingRoot = ".building"

// Staging names carry the room id they belong to and one of two markers:
//
//   - staged: a complete or partial room waiting for its name.
//   - parked: the live room a swap moved aside, waiting for the publish or
//     for the rollback a crash interrupted.
const (
	stagedMark = ".staged-"
	parkedMark = ".parked-"
)

// stagingPath returns the staging area, creating it on first use. Callers hold
// the store lock.
func (r *ConversationRepository) stagingPath() (string, error) {
	dir := filepath.Join(r.path, stagingRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// stageRoom makes an empty directory under the room id's name in the staging
// area. The caller writes the record and files into it and publishes it with
// one rename; a stage that never publishes is dropped by recoverRoom.
// Callers hold the store lock.
func (r *ConversationRepository) stageRoom(id string) (string, error) {
	root, err := r.stagingPath()
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(root, id+stagedMark)
}

// parkedLabelsLocked reserves a handle that still belongs to a room waiting
// in the staging area. Recovery puts that room back under its old name, so a
// new room must not be given the same handle while the copy is parked. A
// parked copy whose live room is already present does not count: recovery
// keeps the live room and drops the copy.
func (r *ConversationRepository) parkedLabelsLocked(exceptID string, used map[string]string) error {
	entries, err := os.ReadDir(filepath.Join(r.path, stagingRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.Contains(entry.Name(), parkedMark) {
			continue
		}
		id, ok := stagedRoomID(entry.Name())
		if !ok || id == exceptID {
			continue
		}
		if _, err := os.Lstat(r.convDir(id)); err == nil {
			continue
		}
		meta, err := readMetaDir(filepath.Join(r.path, stagingRoot, entry.Name()))
		if err != nil || meta.Label == "" {
			continue
		}
		claimLabel(used, meta.Label, id)
	}
	return nil
}

// parkedName names the place a swap parks the live room. The suffix orders
// copies of one room, so a recovery that finds more than one restores the
// newest.
func (r *ConversationRepository) parkedName(id string) (string, error) {
	root, err := r.stagingPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, fmt.Sprintf("%s%s%d", id, parkedMark, time.Now().UnixNano())), nil
}

// namedNonDir reports whether a room's name exists as something other than a
// directory — a regular file, a symlink to one, a socket. Reading a record
// through such a name fails with ENOTDIR rather than "not exist", and the
// repair has to see that shape as an occupied name, not a free one.
func namedNonDir(dir string) bool {
	info, err := os.Lstat(dir)
	return err == nil && !info.IsDir()
}

// recoverRoom finishes a swap a crash interrupted. Clear parks the live room
// and publishes the cleared one under the same name, so a crash between the
// two renames leaves the name empty and the room's history parked. Restoring
// the parked copy is what keeps an interrupted clear leaving the room
// readable instead of missing; a stage is always debris — its room either
// still owns the name (the clear failed before the swap) or has just been
// restored — so it goes.
//
// The name decides what the parked copy is, and a name held by something that
// is not a readable record does not count as "the room was replaced": the
// store cannot know whether that thing is meant to stand in for the history
// it would be discarding, so it keeps the parked copy and refuses instead.
//
// Callers hold the store lock.
func (r *ConversationRepository) recoverRoom(id string) error {
	root := filepath.Join(r.path, stagingRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var parked []string
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, id+parkedMark):
			parked = append(parked, filepath.Join(root, name))
		case strings.HasPrefix(name, id+stagedMark):
			if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
				return fmt.Errorf("recover %s: staged copy: %w", id, err)
			}
		}
	}
	if len(parked) == 0 {
		return nil
	}
	sort.Slice(parked, func(i, j int) bool { return plantedOrder(parked[i]) > plantedOrder(parked[j]) })
	dir := r.convDir(id)
	_, liveErr := os.Lstat(dir)
	switch {
	case liveErr == nil:
		// The name is taken. It only counts as "the room was replaced" when it
		// holds this room's readable record; anything else may be all that is
		// left of the room, so the parked copy stays and the store refuses.
		if _, err := r.readMetaUnvalidated(id); err != nil {
			return apperror.New(500,
				"conversation "+id+" needs attention: its name is held by something that is not a conversation record, and the room's contents are kept in the store's staging area",
				fmt.Errorf("recover %s: name occupied by a non-room (%v)", id, err))
		}
		for _, p := range parked {
			if err := os.RemoveAll(p); err != nil {
				return fmt.Errorf("recover %s: old copy: %w", id, err)
			}
		}
		return nil
	case !errors.Is(liveErr, os.ErrNotExist):
		return liveErr
	}
	// The name is free and the room is parked: put it back as it was.
	if err := renamePath(parked[0], dir); err != nil {
		return fmt.Errorf("recover %s: %w", id, err)
	}
	for _, leftover := range parked[1:] {
		if err := os.RemoveAll(leftover); err != nil {
			return fmt.Errorf("recover %s: old copy: %w", id, err)
		}
	}
	return nil
}

// plantedOrder reads the sequence number a parked name ends with, so a
// recovery restores the newest copy it finds. An unreadable suffix sorts
// last.
func plantedOrder(p string) int64 {
	i := strings.LastIndex(p, parkedMark)
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(p[i+len(parkedMark):], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// recoverStaging repairs every swap a crash interrupted, before a listing
// reads the store: a room whose clear crashed between its two renames is
// listed again rather than silently missing. It takes the store lock itself
// because the repair renames directories and must not race a write.
func (r *ConversationRepository) recoverStaging() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return err
	}
	defer unlock()

	entries, err := os.ReadDir(filepath.Join(r.path, stagingRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		id, ok := stagedRoomID(entry.Name())
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		if err := r.recoverRoom(id); err != nil {
			return err
		}
	}
	return nil
}

// stagedRoomID is the room id a staging entry belongs to: the name up to the
// last marker the store appends.
func stagedRoomID(name string) (string, bool) {
	cut := -1
	for _, mark := range []string{stagedMark, parkedMark} {
		if i := strings.LastIndex(name, mark); i > cut {
			cut = i
		}
	}
	if cut <= 0 {
		return "", false
	}
	return name[:cut], true
}

// dropStaging forgets everything the store staged or parked for a room that is
// being deleted: a delete must not leave a copy a later recovery could bring
// back. Callers hold the store lock.
func (r *ConversationRepository) dropStaging(id string) error {
	root := filepath.Join(r.path, stagingRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, id+stagedMark) && !strings.HasPrefix(name, id+parkedMark) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("delete %s: staged copy: %w", id, err)
		}
	}
	return nil
}
