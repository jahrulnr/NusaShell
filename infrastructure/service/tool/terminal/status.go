package toolexec

import (
	"context"
	"errors"
	"fmt"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// The result vocabulary the process tools share: how a wait reads, and who
// stopped a run. exec, exec_session and write_stdin all end with one of these
// lines, so a stopped process says the same thing wherever it was reported.

// waitText renders a wait the way the model reads it — "120s", "200ms" —
// instead of Go's "2m0s".
func waitText(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return d.String()
}

// stopLine names who stopped a run and how long it lasted. It answers "" when
// nobody on our side decided: a deadline is the caller's own cap and an
// external kill has no cause at all.
func stopLine(cause error, elapsed time.Duration) string {
	switch {
	case errors.Is(cause, dservice.ErrInterruptedByUser):
		return fmt.Sprintf("(interrupted by user after %.1fs — partial output above)", elapsed.Seconds())
	case errors.Is(cause, dservice.ErrInterruptedByAgent):
		return fmt.Sprintf("(interrupted by agent after %.1fs — partial output above)", elapsed.Seconds())
	case errors.Is(cause, dservice.ErrServerRestarted):
		return fmt.Sprintf("(nusashell restarted after %.1fs — partial output above)", elapsed.Seconds())
	case errors.Is(cause, context.Canceled):
		// The caller went away — a closed tab, a dropped socket. Nobody
		// decided this, so no actor is named.
		return fmt.Sprintf("(canceled after %.1fs — partial output above)", elapsed.Seconds())
	}
	return ""
}
