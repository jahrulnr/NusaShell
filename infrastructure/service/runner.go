package service

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runTimeout bounds every service-manager call: systemctl/launchctl/schtasks
// occasionally wedge and a hung call must not hang the CLI forever.
const runTimeout = 30 * time.Second

type execRunner struct{}

// Run executes name with args, returning combined stdout+stderr. Errors carry
// the invocation and the captured output so failures stay actionable.
func (execRunner) Run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(out.String())
		if detail != "" {
			err = fmt.Errorf("%w: %s", err, detail)
		}
		return out.String(), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out.String(), nil
}
