// Package workspacepicker contains host-native implementations of the
// workspace folder selection port.
package workspacepicker

import (
	"context"
	"errors"

	"github.com/ncruces/zenity"
)

// Zenity opens the system folder picker on supported desktop platforms. A
// cancelled picker is represented by context.Canceled so the application can
// leave the active conversation unchanged.
type Zenity struct {
	selectDirectory func(context.Context) (string, error)
}

func (z Zenity) Choose(ctx context.Context) (string, error) {
	selectDirectory := z.selectDirectory
	if selectDirectory == nil {
		selectDirectory = func(ctx context.Context) (string, error) {
			return zenity.SelectFile(
				zenity.Context(ctx),
				zenity.Directory(),
				zenity.Title("Choose NusaShell workspace folder"),
			)
		}
	}

	directory, err := selectDirectory(ctx)
	if errors.Is(err, zenity.ErrCanceled) {
		return "", context.Canceled
	}
	return directory, err
}
