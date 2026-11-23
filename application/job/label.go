package job

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// usedLabels collects every room's label except exceptID's.
func (d Deps) usedLabels(ctx context.Context, exceptID string) (map[string]bool, error) {
	infos, err := d.Convs.List(ctx)
	if err != nil {
		return nil, err
	}
	used := make(map[string]bool, len(infos))
	for _, info := range infos {
		if info.ID != exceptID && info.Label != "" {
			used[info.Label] = true
		}
	}
	return used, nil
}

// freeLabel picks the next unused animal handle for a new room.
func (d Deps) freeLabel(ctx context.Context) (string, error) {
	used, err := d.usedLabels(ctx, "")
	if err != nil {
		return "", err
	}
	return valueobject.PickLabel(used), nil
}

// checkLabel normalizes and validates a requested label and refuses one
// another room owns (409).
func (d Deps) checkLabel(ctx context.Context, convID, raw string) (string, error) {
	label := valueobject.NormalizeLabel(raw)
	if label == "" {
		return "", nil
	}
	if !valueobject.ValidLabel(label) {
		return "", apperror.New(400, "a label is 1–24 of a–z, 0–9 and inner hyphens", nil)
	}
	used, err := d.usedLabels(ctx, convID)
	if err != nil {
		return "", err
	}
	if used[label] {
		return "", apperror.New(409, "another thread already uses @"+label, nil)
	}
	return label, nil
}
