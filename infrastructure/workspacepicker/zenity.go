// Package workspacepicker contains host-native implementations of the
// workspace folder selection port.
package workspacepicker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Zenity opens the system folder picker used by the Linux desktop build. A
// cancelled picker is represented by context.Canceled so the application can
// leave the active conversation unchanged.
type Zenity struct{}

func (Zenity) Choose(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, "zenity", "--file-selection", "--directory", "--title=Choose NusaShell workspace folder")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", context.Canceled
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("workspace folder picker is unavailable: install zenity")
		}
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return "", fmt.Errorf("choose workspace folder: %s", message)
		}
		return "", fmt.Errorf("choose workspace folder: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
