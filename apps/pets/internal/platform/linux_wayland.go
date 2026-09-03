//go:build sdl2

package platform

import (
	"errors"
	"os"
)

// WaylandCaveat is returned when the pet overlay detects a native Wayland
// session. X11 hints (always-on-top, per-pixel click-through) are not supported
// under native Wayland; run under XWayland (GDK_BACKEND=x11) instead.
var WaylandCaveat = errors.New("platform: Wayland native session detected; " +
	"always-on-top and click-through require X11/XWayland (set GDK_BACKEND=x11)")

// IsWaylandSession reports whether the current session is native Wayland (no
// X11). It inspects WAYLAND_DISPLAY and XDG_SESSION_TYPE. The caller rejects
// this mode because the current runtime depends on X11 Shape.
func IsWaylandSession() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("DISPLAY") == "" {
		return true
	}
	return os.Getenv("XDG_SESSION_TYPE") == "wayland" && os.Getenv("DISPLAY") == ""
}
