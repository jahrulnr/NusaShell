package repoconversation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// List reads every conversation's meta and returns infos newest activity
// first. Directories without a meta are skipped; corrupt records surface an
// error so callers do not mistake missing rooms for an empty store. An
// interrupted clear is repaired first, so a room whose swap a crash cut short
// is listed again instead of silently missing.
func (r *ConversationRepository) List(ctx context.Context) ([]drepo.ConversationInfo, error) {
	if err := r.recoverStaging(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []drepo.ConversationInfo
	for _, e := range entries {
		if !e.IsDir() || e.Name() == stagingRoot {
			// The store's own staging area is not a room, even while a build
			// sits inside it.
			continue
		}
		info, err := r.Info(ctx, e.Name())
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("conversation %q: %w", e.Name(), err)
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}
