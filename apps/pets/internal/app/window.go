//go:build sdl2

// Package app creates the SDL2 window and owns its native lifetime. The X11
// platform adapter applies the alpha-derived bounding/input shapes after the
// window has been created.
package app

import (
	"fmt"
	"log/slog"

	"github.com/veandco/go-sdl2/sdl"
)

// Window wraps the SDL window and renderer for the pet overlay.
type Window struct {
	Window   *sdl.Window
	Renderer *sdl.Renderer
	Width    int
	Height   int
}

// WindowConfig describes the overlay window.
type WindowConfig struct {
	Width       int
	Height      int
	Title       string
	AlwaysOnTop bool
}

// NewWindow creates a hidden, borderless overlay window. The caller must
// apply the native shape, render an initial frame, call Show, and then call
// Destroy when done. SDL must be initialized before calling this.
func NewWindow(cfg WindowConfig, log *slog.Logger) (*Window, error) {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("app: invalid window size %dx%d", cfg.Width, cfg.Height)
	}
	flags := uint32(sdl.WINDOW_BORDERLESS | sdl.WINDOW_HIDDEN)
	title := cfg.Title
	if title == "" {
		title = "NusaShell Pet"
	}
	// The window-type hint is applied before creation so an X11 window manager
	// can classify the overlay without a visible transition.
	_ = sdl.SetHint(sdl.HINT_X11_WINDOW_TYPE, "_NET_WM_WINDOW_TYPE_DOCK")
	w, err := sdl.CreateWindow(title, sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		int32(cfg.Width), int32(cfg.Height), flags)
	if err != nil {
		return nil, fmt.Errorf("app: create window: %w", err)
	}
	if cfg.AlwaysOnTop {
		w.SetAlwaysOnTop(true)
	}

	// Accelerated renderer with vsync; fallback to software if no compositor
	// or hardware renderer is available.
	r, err := sdl.CreateRenderer(w, -1, sdl.RENDERER_ACCELERATED|sdl.RENDERER_PRESENTVSYNC)
	if err != nil {
		log.Warn("app: accelerated renderer unavailable, falling back to software", "err", err)
		r, err = sdl.CreateRenderer(w, -1, sdl.RENDERER_SOFTWARE)
		if err != nil {
			w.Destroy()
			return nil, fmt.Errorf("app: create renderer: %w", err)
		}
	}
	_ = r.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	_ = r.SetDrawColor(0, 0, 0, 0)

	return &Window{
		Window:   w,
		Renderer: r,
		Width:    cfg.Width,
		Height:   cfg.Height,
	}, nil
}

// Show makes the fully configured overlay visible.
func (w *Window) Show() {
	if w != nil && w.Window != nil {
		w.Window.Show()
	}
}

// Position returns the current window position in desktop coordinates.
func (w *Window) Position() (x, y int) {
	if w == nil || w.Window == nil {
		return 0, 0
	}
	px, py := w.Window.GetPosition()
	return int(px), int(py)
}

// MoveTo positions the window at (x, y) in desktop coordinates.
func (w *Window) MoveTo(x, y int) {
	if w != nil && w.Window != nil {
		w.Window.SetPosition(int32(x), int32(y))
	}
}

// CenterOnScreen centers the window on the current display.
func (w *Window) CenterOnScreen() {
	if w != nil && w.Window != nil {
		w.Window.SetPosition(sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED)
	}
}

// Destroy releases the SDL resources owned by the overlay.
func (w *Window) Destroy() {
	if w == nil {
		return
	}
	if w.Renderer != nil {
		w.Renderer.Destroy()
		w.Renderer = nil
	}
	if w.Window != nil {
		w.Window.Destroy()
		w.Window = nil
	}
}

// X11Handles returns the X11 Display and Window handles via SDL_GetWindowWMInfo.
// Returns (0, 0, error) if SDL is not using X11. The Display handle must NOT be
// closed by the caller (SDL owns it).
func (w *Window) X11Handles() (display, win uintptr, err error) {
	if w == nil || w.Window == nil {
		return 0, 0, fmt.Errorf("app: window is nil")
	}
	info, err := w.Window.GetWMInfo()
	if err != nil {
		return 0, 0, fmt.Errorf("app: GetWMInfo: %w", err)
	}
	if info.Subsystem != sdl.SYSWM_X11 {
		return 0, 0, fmt.Errorf("app: not an X11 window (subsystem %v)", info.Subsystem)
	}
	x11 := info.GetX11Info()
	return uintptr(x11.Display), uintptr(x11.Window), nil
}
