package job

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// checkLabel normalizes and validates a requested label and refuses one
// another room owns (409). This is the early answer: the store re-checks the
// handle under its write lock, which is what makes two renames racing for one
// name leave exactly one owner.
func (d Deps) checkLabel(ctx context.Context, convID, raw string) (string, error) {
	label := valueobject.NormalizeLabel(raw)
	if label == "" {
		return "", nil
	}
	if !valueobject.ValidLabel(label) {
		return "", apperror.New(400, "a label is 1–24 of a–z, 0–9 and inner hyphens", nil)
	}
	infos, err := d.Convs.List(ctx)
	if err != nil {
		return "", err
	}
	for _, info := range infos {
		if info.ID != convID && info.Label == label {
			return "", apperror.New(409, "another thread already uses @"+label, nil)
		}
	}
	return label, nil
}
