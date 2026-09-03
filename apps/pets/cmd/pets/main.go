//go:build sdl2

// Command pets is the NusaShell desktop pet overlay. It creates an alpha-shaped,
// always-on-top SDL2 window on X11, renders the configured WebP atlas, and
// switches its activity state over a WebSocket feed from the NusaShell backend.
//
// Build: go build -tags sdl2 -o bin/pets ./cmd/pets
// Run:   ./bin/pets --assets assets/pets
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v2"
	"github.com/veandco/go-sdl2/sdl"

	"nusashell-pets/internal/app"
	"nusashell-pets/internal/bubble"
	"nusashell-pets/internal/char"
	"nusashell-pets/internal/config"
	"nusashell-pets/internal/direction"
	"nusashell-pets/internal/events"
	"nusashell-pets/internal/interaction"
	"nusashell-pets/internal/platform"
	"nusashell-pets/internal/renderer"
	"nusashell-pets/internal/shape"
	"nusashell-pets/internal/state"
	"nusashell-pets/internal/ws"
)

func main() {
	// SDL video and shaped-window calls stay on the process' initial OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "pets: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return newApp().Run(args)
}

func newApp() *cli.App {
	return &cli.App{
		Name:  "pets",
		Usage: "NusaShell desktop pet overlay (Linux X11, SDL2)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "assets", Value: "assets/pets", Usage: "path to the pet assets folder"},
			&cli.StringFlag{Name: "config", Value: "", Usage: "path to config.json (default <assets>/config.json)"},
			&cli.StringFlag{Name: "image", Value: "", Usage: "static image path (overrides config; relative paths use --assets)"},
			&cli.StringFlag{Name: "spritesheet", Value: "", Usage: "hatch-pet v2 WebP atlas path (overrides config; relative paths use --assets)"},
			&cli.StringFlag{Name: "ws-url", Value: "", Usage: "WebSocket URL (overrides config)"},
			&cli.StringFlag{Name: "electron-path", Value: "", Usage: "Electron executable to launch on click (overrides config)"},
			&cli.BoolFlag{Name: "click-through", Value: false, Usage: "enable whole-window click-through (SIGUSR1 toggles it back)"},
		},
		Action: start,
	}
}

func start(c *cli.Context) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	assetsPath := c.String("assets")
	configPath := c.String("config")
	if configPath == "" {
		configPath = filepath.Join(assetsPath, "config.json")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if v := c.String("ws-url"); v != "" {
		cfg.WSURL = v
	}
	if v := c.String("electron-path"); v != "" {
		cfg.ElectronPath = v
	}
	clickThrough := cfg.ClickThrough
	if c.IsSet("click-through") {
		clickThrough = c.Bool("click-through")
	}
	imagePath := c.String("image")
	if imagePath == "" {
		imagePath = cfg.Image
	}
	spriteSheetPath := c.String("spritesheet")
	if spriteSheetPath == "" {
		spriteSheetPath = cfg.SpriteSheet
	}
	log.Info("pets: config loaded", "name", cfg.Name, "ws", cfg.WSURL,
		"image", imagePath, "spritesheet", spriteSheetPath, "click_through", clickThrough, "states", stateNames(cfg.States))

	// Native Wayland cannot provide the X11 shaped input behavior this phase
	// relies on. XWayland remains supported because DISPLAY is present.
	if platform.IsWaylandSession() {
		return platform.WaylandCaveat
	}

	var pack *char.CharPack
	if strings.TrimSpace(spriteSheetPath) != "" {
		spriteSheetPath = resolveAssetPath(assetsPath, spriteSheetPath)
		pack, err = char.LoadAtlas(spriteSheetPath, cfg.Name, cfg.MaxWidth, cfg.MaxHeight, char.DefaultAtlasLayout())
	} else if strings.TrimSpace(imagePath) != "" {
		imagePath = resolveAssetPath(assetsPath, imagePath)
		pack, err = char.LoadStaticImage(imagePath, cfg.Name, cfg.MaxWidth, cfg.MaxHeight)
	} else {
		return fmt.Errorf("pets: spritesheet is required (set spritesheet in config or use --spritesheet)")
	}
	if err != nil {
		return fmt.Errorf("load pet artwork: %w", err)
	}
	log.Info("pets: pack loaded", "name", pack.Name, "scale", pack.Scale, "states", stateNames(pack.States))

	ww, wh := windowSize(pack)
	wh += bubble.AreaHeight
	log.Info("pets: window size", "w", ww, "h", wh)

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		return fmt.Errorf("sdl init: %w", err)
	}
	defer sdl.Quit()

	win, err := app.NewWindow(app.WindowConfig{
		Width: ww, Height: wh, Title: cfg.Name, AlwaysOnTop: true,
	}, log)
	if err != nil {
		return err
	}
	defer win.Destroy()

	ren := renderer.New(win.Renderer, log)
	defer ren.Destroy()
	if err := ren.Load(pack); err != nil {
		return fmt.Errorf("renderer load: %w", err)
	}
	if cfg.Bubbles() {
		fontPath := cfg.BubbleFont
		if fontPath != "" {
			fontPath = resolveAssetPath(assetsPath, fontPath)
		} else {
			fontPath = bubble.FindFont("")
		}
		if fontPath == "" {
			log.Warn("pets: bubble disabled, no TTF font found")
		} else if err := ren.LoadBubbleFonts(fontPath); err != nil {
			log.Warn("pets: bubble disabled", "err", err)
		} else {
			log.Info("pets: bubble font", "path", fontPath)
		}
	}
	frame, ok := pack.FirstFrame()
	if !ok {
		return fmt.Errorf("pets: pack has no renderable frame")
	}
	mask, err := shape.FromImage(ren.WindowImage(frame), cfg.ShapeAlphaCutoff)
	if err != nil {
		return fmt.Errorf("build window shape: %w", err)
	}
	dpy, xwin, err := win.X11Handles()
	if err != nil {
		return fmt.Errorf("pets: X11 is required: %w", err)
	}
	if err := platform.SetBoundingShape(dpy, xwin, mask); err != nil {
		return fmt.Errorf("set window shape: %w", err)
	}
	setInputMode := func(next bool) error {
		return platform.SetInputMode(dpy, xwin, next)
	}
	if err := setInputMode(clickThrough); err != nil {
		return err
	}
	setBoundingShape := func(next *shape.Mask) error {
		return platform.SetBoundingShape(dpy, xwin, next)
	}
	win.CenterOnScreen()
	// The bubble strip shifts the pet down inside the taller window; nudge the
	// window up by half the strip so the pet keeps its old screen position.
	if wx, wy := win.Position(); wy >= bubble.AreaHeight/2 {
		win.MoveTo(wx, wy-bubble.AreaHeight/2)
	}
	_ = ren.Render()
	win.Show()

	available := availableStates(pack)
	// The renderer sync happens inside eventLoop's applyState so the drag-run
	// overlay can suppress it while the pet is held and restore the machine
	// state on release; the machine itself carries no listener.
	machine := state.NewMachine(state.StateIdle, available, nil)
	ren.SetState(string(machine.Current()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stateEvents := make(chan state.Event, 32)
	// WebSocket callbacks run on the network goroutine. They only decode and
	// enqueue events; SDL and the state machine remain owned by the main loop.
	handler := ws.HandlerFunc(func(data []byte) {
		ev, relevant, err := events.Decode(data)
		if err != nil {
			log.Warn("pets: parse event", "err", err, "raw", string(data))
			return
		}
		if !relevant {
			return
		}
		select {
		case stateEvents <- ev:
		case <-ctx.Done():
		}
	})

	wsClient := ws.NewClient(ws.NewGorillaDialer(), cfg.WSURL, handler, log)
	wsDone := make(chan struct{})
	go func() {
		defer close(wsDone)
		if err := wsClient.Run(ctx); err != nil && err != context.Canceled {
			log.Warn("pets: ws run ended", "err", err)
		}
	}()
	defer func() {
		wsClient.Close()
		cancel()
		<-wsDone
	}()

	// SIGUSR1 toggles click-through because an entirely click-through window
	// cannot receive the right-click shortcut used in interactive mode.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGUSR1)
	toggleClickThrough := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig := <-sigCh:
				switch sig {
				case syscall.SIGINT, syscall.SIGTERM:
					log.Info("pets: signal received, shutting down", "signal", sig.String())
					wsClient.Close()
					cancel()
				case syscall.SIGUSR1:
					select {
					case toggleClickThrough <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	defer signal.Stop(sigCh)

	return eventLoop(ctx, win, ren, cfg, machine, stateEvents, toggleClickThrough,
		clickThrough, setInputMode, setBoundingShape, dpy, log)
}

// eventLoop pumps SDL events, advances the animation on its own clock, and
// handles click and drag gestures until the window is closed or ctx is
// canceled. While a left-button hold drags the pet, the pointer is polled at
// the display refresh cadence but the animation advances only on its authored
// delay, so dragging never speeds playback up.
func eventLoop(ctx context.Context, win *app.Window, ren *renderer.Renderer, cfg *config.Config,
	machine *state.Machine, stateEvents <-chan state.Event, toggleEvents <-chan struct{},
	clickThrough bool, setInputMode func(bool) error, setBoundingShape func(*shape.Mask) error,
	dpy uintptr, log *slog.Logger) error {
	controller := interaction.NewController(5)
	run := interaction.NewRunner(16)
	const lookDeadzone = 16.0
	leftHeld := false
	runDir := 0            // settled drag-run direction: -1 left, 0 none, +1 right
	runActive := false     // drag-run overlay currently rendered
	var nextAnim time.Time // zero = render on the next loop iteration
	lastShapeKey := ""
	lastDragX := int32(0)
	activity := bubble.Activity{}
	applyRenderedShape := func(rendered renderer.FrameResult) {
		if rendered.Image == nil || setBoundingShape == nil {
			return
		}
		key := fmt.Sprintf("%s:%d:%d:%d", rendered.State, rendered.Frame, rendered.LookIndex, ren.BubbleVersion())
		if key == lastShapeKey {
			return
		}
		mask, err := shape.FromImage(ren.WindowImage(rendered.Image), cfg.ShapeAlphaCutoff)
		if err != nil {
			log.Warn("pets: build frame shape failed", "err", err, "state", rendered.State, "frame", rendered.Frame)
			return
		}
		if err := setBoundingShape(mask); err != nil {
			log.Warn("pets: update frame shape failed", "err", err, "state", rendered.State, "frame", rendered.Frame)
			return
		}
		lastShapeKey = key
	}
	// applyState feeds a websocket event into the machine and mirrors the new
	// state onto the renderer. The drag-run overlay suppresses that mirror
	// while the pet is held; the correct pose resumes on release.
	applyState := func(ev state.Event) {
		before := machine.Current()
		next, changed := machine.Transition(ev)
		if !changed {
			log.Debug("pets: event no state change", "state", ev.State)
		} else {
			log.Info("pets: state transition", "old", before, "new", next, "msg", ev.Message)
			if !runActive {
				ren.SetState(string(next))
				nextAnim = time.Time{}
			}
		}
		// Visual copy has its own dwell clock; animation states still update
		// immediately. Same-state tool events replace pending copy as well.
		ev.State = next
		activity.Update(ev, time.Now())
	}

	// endRun clears the drag-run overlay and restores whatever state the
	// machine is in (which may have changed over WebSocket during the drag).
	endRun := func() {
		if !runActive {
			return
		}
		runActive = false
		ren.SetRunDirection(0)
		ren.SetState(string(machine.Current()))
	}

	applyToggle := func() {
		next := !clickThrough
		if err := setInputMode(next); err != nil {
			log.Warn("pets: toggle click-through failed", "err", err)
			return
		}
		clickThrough = next
		controller.Cancel()
		leftHeld = false
		endRun()
		ren.SetLookDirection(direction.Neutral)
		nextAnim = time.Time{}
		log.Info("pets: click-through changed", "enabled", clickThrough)
	}

	for {
		now := time.Now()
		for ev := sdl.PollEvent(); ev != nil; ev = sdl.PollEvent() {
			switch e := ev.(type) {
			case *sdl.QuitEvent:
				log.Info("pets: quit event")
				controller.Cancel()
				leftHeld = false
				return nil
			case *sdl.WindowEvent:
				if e.Event == uint8(sdl.WINDOWEVENT_CLOSE) {
					controller.Cancel()
					leftHeld = false
					return nil
				}
				if e.Event == uint8(sdl.WINDOWEVENT_LEAVE) {
					ren.SetLookDirection(direction.Neutral)
				}
			case *sdl.MouseButtonEvent:
				switch {
				case e.Type == sdl.MOUSEBUTTONDOWN && e.Button == sdl.BUTTON_RIGHT && !clickThrough:
					applyToggle()
				case e.Type == sdl.MOUSEBUTTONDOWN && e.Button == sdl.BUTTON_LEFT && !clickThrough:
					leftHeld = true
					run.Reset()
					ren.SetLookDirection(direction.IndexFromPoint(e.X, e.Y, int32(win.Width), int32(win.Height), lookDeadzone))
					x, y := win.Position()
					cursor := interaction.Point{X: int32(x) + e.X, Y: int32(y) + e.Y}
					if pointer, err := platform.QueryPointer(dpy); err == nil {
						cursor = interaction.Point{X: pointer.X, Y: pointer.Y}
					} else {
						log.Warn("pets: read pointer on press failed", "err", err)
					}
					controller.Press(cursor, interaction.Point{X: int32(x), Y: int32(y)})
					lastDragX = cursor.X
				case e.Type == sdl.MOUSEBUTTONUP && e.Button == sdl.BUTTON_LEFT:
					leftHeld = false
					endRun()
					if controller.Release(interaction.Point{X: e.X, Y: e.Y}) && !clickThrough && cfg.ElectronPath != "" {
						log.Info("pets: launching electron", "path", cfg.ElectronPath)
						launchElectron(cfg.ElectronPath, log)
					}
				}
			case *sdl.MouseMotionEvent:
				if !leftHeld && !clickThrough {
					ren.SetLookDirection(direction.IndexFromPoint(e.X, e.Y, int32(win.Width), int32(win.Height), lookDeadzone))
				}
			}
		}
		if leftHeld {
			pointer, err := platform.QueryPointer(dpy)
			if err != nil {
				log.Warn("pets: read pointer during drag failed", "err", err)
				controller.Cancel()
				leftHeld = false
				endRun()
			} else {
				origin, dragged := controller.MoveWhileHeld(interaction.Point{X: pointer.X, Y: pointer.Y}, pointer.LeftButtonHeld)
				if !pointer.LeftButtonHeld {
					leftHeld = false
					endRun()
				} else {
					if dragged {
						win.MoveTo(int(origin.X), int(origin.Y))
						if dir := run.Update(pointer.X - lastDragX); dir != runDir {
							runDir = dir
							if dir != 0 {
								ren.SetRunDirection(dir)
								runActive = true
								nextAnim = time.Time{}
							}
						}
					}
					lastDragX = pointer.X
				}
			}
		}

		// Advance the animation only when its authored delay has elapsed.
		// Pointer polling during a drag happens at refresh cadence below, but
		// never moves the animation clock faster than the asset timing.
		if nextAnim.IsZero() || !now.Before(nextAnim) {
			ren.SetBubble(activity.Text(now))
			rendered := ren.Render()
			applyRenderedShape(rendered)
			if rendered.Finished && (machine.Current() == state.StateDone || machine.Current() == state.StateError) {
				applyState(state.Event{State: state.StateIdle})
				rendered.Delay = 0
			}
			frameDelay := rendered.Delay
			if frameDelay <= 0 {
				frameDelay = 10 * time.Millisecond
			}
			nextAnim = now.Add(frameDelay)
		}
		wait := time.Until(nextAnim)
		if leftHeld {
			pollInterval := interaction.PollInterval(currentDisplayRefreshRate(win))
			if pollInterval < wait {
				wait = pollInterval
			}
		}
		if wait <= 0 {
			wait = 10 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			controller.Cancel()
			leftHeld = false
			return nil
		case ev, ok := <-stateEvents:
			timer.Stop()
			if ok {
				applyState(ev)
			}
		case <-toggleEvents:
			timer.Stop()
			applyToggle()
		case <-timer.C:
		}
	}
}

// currentDisplayRefreshRate reports the refresh rate of the display containing
// the window's center. SDL returns zero for an unspecified refresh rate.
// Sources: https://wiki.libsdl.org/SDL2/SDL_GetWindowDisplayIndex and
// https://wiki.libsdl.org/SDL2/SDL_GetCurrentDisplayMode
func currentDisplayRefreshRate(win *app.Window) int {
	if win == nil || win.Window == nil {
		return 0
	}
	displayIndex, err := win.Window.GetDisplayIndex()
	if err != nil {
		return 0
	}
	mode, err := sdl.GetCurrentDisplayMode(displayIndex)
	if err != nil {
		return 0
	}
	return int(mode.RefreshRate)
}

func resolveAssetPath(assetsPath, configured string) string {
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(assetsPath, configured)
}

// launchElectron starts the configured Electron executable detached.
func launchElectron(path string, log *slog.Logger) {
	cmd := exec.Command(path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Warn("pets: launch electron failed", "err", err)
	}
}

// windowSize returns the overlay window size from the largest state frame,
// clamped to the pack's max box.
func windowSize(pack *char.CharPack) (int, int) {
	if pack != nil && pack.FrameWidth > 0 && pack.FrameHeight > 0 {
		return pack.FrameWidth, pack.FrameHeight
	}
	maxW, maxH := 0, 0
	for _, anim := range pack.States {
		if anim == nil {
			continue
		}
		for _, frame := range anim.Frames {
			if frame == nil {
				continue
			}
			b := frame.Bounds()
			if b.Dx() > maxW {
				maxW = b.Dx()
			}
			if b.Dy() > maxH {
				maxH = b.Dy()
			}
		}
	}
	if maxW == 0 {
		maxW = pack.MaxWidth
	}
	if maxH == 0 {
		maxH = pack.MaxHeight
	}
	return maxW, maxH
}

// availableStates returns the pet states that have a decoded animation.
func availableStates(pack *char.CharPack) []state.PetState {
	var out []state.PetState
	for name, anim := range pack.States {
		if anim == nil || len(anim.Frames) == 0 {
			continue
		}
		if s, err := state.ParseState(name); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// stateNames returns sorted state names for logging.
func stateNames(states any) []string {
	switch s := states.(type) {
	case map[string]config.StateConfig:
		out := make([]string, 0, len(s))
		for k := range s {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	case map[string]*char.Anim:
		out := make([]string, 0, len(s))
		for k := range s {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	return nil
}
