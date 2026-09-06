package pets

import (
	"context"
	"runtime"
)

// AutoLaunch spawns the desktop pet overlay when the user opted into
// auto-start. The caller (App.StartPetAutoLaunch) is responsible for
// single-flight across the process lifetime.
func (s *Service) AutoLaunch(_ context.Context) {
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
	path, err := s.installer.Launch()
	if err != nil {
		s.write("warn", "auto-start launch failed: %v", err)
		return
	}
	s.write("info", "auto-started pet overlay: %s", path)
}
