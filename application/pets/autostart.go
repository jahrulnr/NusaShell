package pets

import (
	"context"
	"runtime"
	"time"
)

// autoLaunchBackoff is the pause between failed auto-start attempts while
// waiting for DISPLAY (login race). Overridable in tests.
var autoLaunchBackoff = time.Second

// autoLaunchAttempts bounds how long AutoLaunch keeps retrying after login
// before giving up for this process lifetime.
const autoLaunchAttempts = 90

// AutoLaunch spawns the desktop pet overlay when the user opted into
// auto-start. The caller (App.StartPetAutoLaunch) is responsible for
// single-flight across the process lifetime. On Linux, the Go login
// service can start before the desktop publishes DISPLAY; Launch then
// fails the settle check, so AutoLaunch retries until the pet stays up
// or the attempt budget is exhausted.
func (s *Service) AutoLaunch(ctx context.Context) {
	if s.installer == nil {
		return
	}
	if s.settings == nil {
		return
	}
	settings := s.settings.Get()
	if !settings.PetsAutoStart {
		s.write("info", "auto-start off; pet stays managed by user")
		return
	}
	if runtime.GOOS != "linux" {
		s.write("info", "auto-start skipped (platform=%s)", runtime.GOOS)
		return
	}
	status := s.installer.Status()
	if !status.Supported {
		s.write("info", "auto-start skipped (installer reports unsupported)")
		return
	}
	if !status.Installed || status.Path == "" {
		s.write("info", "auto-start skipped (binary not installed yet)")
		return
	}

	backoff := autoLaunchBackoff
	if backoff <= 0 {
		backoff = time.Second
	}
	for attempt := 1; attempt <= autoLaunchAttempts; attempt++ {
		if ctx != nil {
			select {
			case <-ctx.Done():
				s.write("info", "auto-start canceled: %v", ctx.Err())
				return
			default:
			}
		}
		status = s.installer.Status()
		if status.Running {
			if attempt == 1 {
				s.write("info", "auto-start skipped (already running)")
			} else {
				s.write("info", "auto-start confirmed running after retry")
			}
			return
		}
		path, err := s.installer.Launch()
		if err == nil && s.installer.Status().Running {
			s.write("info", "auto-started pet overlay: %s", path)
			return
		}
		if err == nil {
			err = errPetNotRunning
		}
		s.write("warn", "auto-start attempt %d/%d failed: %v", attempt, autoLaunchAttempts, err)
		if attempt == autoLaunchAttempts {
			s.write("warn", "auto-start gave up after %d attempts", autoLaunchAttempts)
			return
		}
		timer := time.NewTimer(backoff)
		if ctx == nil {
			<-timer.C
			continue
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			s.write("info", "auto-start canceled: %v", ctx.Err())
			return
		case <-timer.C:
		}
	}
}

type petNotRunningError struct{}

func (petNotRunningError) Error() string { return "pet: spawn reported ok but process is not running" }

var errPetNotRunning error = petNotRunningError{}
