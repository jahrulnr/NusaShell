// Package appbrowser is the browser use case: each thread drives its own
// browser tab — navigate, read the page as a snapshot with element refs, and
// act on those refs — while the owner can watch it live.
package appbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// maxPause caps browser_wait_for's fixed pause.
const maxPause = 30 * time.Second

// Secrets loads the values a thread's [[env:NAME]] references resolve to:
// its agent's saved environment.
type Secrets func(ctx context.Context, convID string) (map[string]string, error)

// App drives the threads' browser sessions.
type App struct {
	browser    dservice.Browser
	secrets    Secrets
	logins     drepo.BrowserLogin
	recordings drepo.Recordings
	uploads    drepo.Uploads
	picks      drepo.Picks
	installer  dservice.BrowserInstaller
	history    drepo.BrowserHistory

	mu      sync.Mutex
	live    map[string]drepo.RecordingWriter // thread → its live recording
	masks   map[string]map[string]string     // thread → typed secret → its tag
	pending map[string]*pendingLogin         // thread → login waiting for the owner
}

// New wires the use case. secrets may be nil (references are refused);
// logins and recordings may be nil (no vault, no recordings).
func New(b dservice.Browser, secrets Secrets, logins drepo.BrowserLogin, recordings drepo.Recordings) *App {
	a := &App{browser: b, secrets: secrets, logins: logins, recordings: recordings, masks: map[string]map[string]string{},
		pending: map[string]*pendingLogin{}, live: map[string]drepo.RecordingWriter{}}
	if b != nil {
		b.OnLogin(a.CaptureLogin)
	}
	return a
}

// Result is what an action reports: where the page is now, plus text or a
// screenshot when the action produces one.
type Result struct {
	Page      dservice.BrowserPage
	Text      string
	Image     []byte
	ImageType string // "" is image/jpeg
}

func (a *App) session(ctx context.Context, convID string) (dservice.BrowserSession, error) {
	if convID == "" {
		return nil, apperror.New(400, "the browser needs a thread", nil)
	}
	return a.browser.Open(ctx, convID)
}

// done reports the page after an action.
func done(s dservice.BrowserSession, text string) (Result, error) {
	return Result{Page: s.Page(), Text: text}, nil
}

// withShot reports the page and a screenshot.
func withShot(ctx context.Context, s dservice.BrowserSession) (Result, error) {
	shot, err := s.Screenshot(ctx)
	if err != nil {
		return Result{}, err
	}
	return Result{Page: s.Page(), Image: shot}, nil
}

// Navigate opens url; a bare host gets https://.
func (a *App) Navigate(ctx context.Context, convID, raw string) (Result, error) {
	target, err := normalizeURL(raw)
	if err != nil {
		return Result{}, err
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	if err := s.Navigate(ctx, target); err != nil {
		return Result{}, err
	}
	return withShot(ctx, s)
}

// Start opens the thread's browser for its owner, who may want to drive it
// before any agent does. It goes to url when one is given, and otherwise
// leaves the new tab blank.
func (a *App) Start(ctx context.Context, convID, raw string) error {
	target := ""
	if strings.TrimSpace(raw) != "" {
		var err error
		if target, err = normalizeURL(raw); err != nil {
			return err
		}
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return err
	}
	if target == "" {
		return nil
	}
	return s.Navigate(ctx, target)
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", apperror.New(400, "url is required", nil)
	}
	if raw == "about:blank" {
		return raw, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", apperror.New(400, "url must be an http or https address", err)
	}
	return u.String(), nil
}

// History goes back (-1) or forward (+1).
func (a *App) History(ctx context.Context, convID string, step int) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	if err := s.History(ctx, step); err != nil {
		return Result{}, err
	}
	return done(s, "")
}

// Snapshot returns the page tree with element refs.
func (a *App) Snapshot(ctx context.Context, convID string) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	tree, err := s.Snapshot(ctx)
	if err != nil {
		return Result{}, err
	}
	return done(s, tree)
}

// Screenshot returns the viewport as a JPEG.
func (a *App) Screenshot(ctx context.Context, convID string) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	return withShot(ctx, s)
}

// Point is a spot in the viewport: an element ref, or x/y as fractions
// from 0 to 1 (or CSS pixels when larger).
type Point struct {
	Ref string   `json:"ref"`
	X   *float64 `json:"x"`
	Y   *float64 `json:"y"`
}

func (p Point) resolve(ctx context.Context, s dservice.BrowserSession) (float64, float64, error) {
	if p.Ref != "" {
		return s.Point(ctx, p.Ref)
	}
	if p.X == nil || p.Y == nil {
		return 0, 0, apperror.New(400, "ref or x and y are required", nil)
	}
	x, y := *p.X, *p.Y
	if x >= 0 && x <= 1 && y >= 0 && y <= 1 {
		pg := s.Page()
		x, y = x*float64(pg.Width), y*float64(pg.Height)
	}
	return x, y, nil
}

// ClickInput is a click.
type ClickInput struct {
	Point
	Button      string `json:"button"`
	DoubleClick bool   `json:"doubleClick"`
}

// Click clicks an element or a spot.
func (a *App) Click(ctx context.Context, convID string, in ClickInput) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	x, y, err := in.resolve(ctx, s)
	if err != nil {
		return Result{}, err
	}
	count := 1
	if in.DoubleClick {
		count = 2
	}
	s.ShowAgentPointer(x, y, "click")
	if err := s.Click(ctx, x, y, in.Button, count); err != nil {
		return Result{}, err
	}
	return done(s, "")
}

// Hover moves the pointer onto an element or a spot.
func (a *App) Hover(ctx context.Context, convID string, p Point) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	x, y, err := p.resolve(ctx, s)
	if err != nil {
		return Result{}, err
	}
	s.ShowAgentPointer(x, y, "hover")
	if err := s.Move(ctx, x, y, 0); err != nil {
		return Result{}, err
	}
	return done(s, "")
}

// TypeInput types into an element (by ref) or the focused one.
type TypeInput struct {
	Ref    string `json:"ref"`
	Text   string `json:"text"`
	Submit bool   `json:"submit"`
}

// Type focuses the ref'd element — clicking it when focus does not land —
// inserts the text, and presses Enter when asked.
func (a *App) Type(ctx context.Context, convID string, in TypeInput) (Result, error) {
	if in.Text == "" && !in.Submit {
		return Result{}, apperror.New(400, "text is required", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	values, err := a.secretValues(ctx, convID, in.Text)
	if err != nil {
		return Result{}, err
	}
	text, bindings, err := resolveSecrets(in.Text, values)
	a.remember(convID, bindings)
	if err != nil {
		return Result{}, err
	}
	if len(bindings) > 0 {
		// A secret goes through real text input into the element itself.
		if err := s.TypeSecret(ctx, in.Ref, text, false); err != nil {
			return Result{}, err
		}
		if in.Submit {
			if err := s.Key(ctx, "Enter", 0); err != nil {
				return Result{}, err
			}
		}
		return done(s, "")
	}
	in.Text = text
	if in.Ref != "" {
		focused, err := s.Focus(ctx, in.Ref)
		if err != nil {
			return Result{}, err
		}
		if !focused {
			x, y, err := s.Point(ctx, in.Ref)
			if err != nil {
				return Result{}, err
			}
			s.ShowAgentPointer(x, y, "click")
			if err := s.Click(ctx, x, y, "", 1); err != nil {
				return Result{}, err
			}
		}
	}
	if in.Text != "" {
		if err := s.Type(ctx, in.Text); err != nil {
			return Result{}, err
		}
	}
	if in.Submit {
		if err := s.Key(ctx, "Enter", 0); err != nil {
			return Result{}, err
		}
	}
	return done(s, "")
}

// Key presses one key.
func (a *App) Key(ctx context.Context, convID, key string) (Result, error) {
	if strings.TrimSpace(key) == "" {
		return Result{}, apperror.New(400, "key is required", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	if err := s.Key(ctx, key, 0); err != nil {
		return Result{}, err
	}
	return done(s, "")
}

// ScrollInput turns the wheel at a spot (default: the viewport center).
type ScrollInput struct {
	X      *float64 `json:"x"`
	Y      *float64 `json:"y"`
	DeltaX float64  `json:"deltaX"`
	DeltaY float64  `json:"deltaY"`
}

// Scroll scrolls the page.
func (a *App) Scroll(ctx context.Context, convID string, in ScrollInput) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	half := 0.5
	p := Point{X: in.X, Y: in.Y}
	if p.X == nil {
		p.X = &half
	}
	if p.Y == nil {
		p.Y = &half
	}
	x, y, err := p.resolve(ctx, s)
	if err != nil {
		return Result{}, err
	}
	s.ShowAgentPointer(x, y, "scroll")
	if err := s.Scroll(ctx, x, y, in.DeltaX, in.DeltaY); err != nil {
		return Result{}, err
	}
	return done(s, "")
}

// Select picks options of a <select>.
func (a *App) Select(ctx context.Context, convID, ref string, values []string) (Result, error) {
	if ref == "" || len(values) == 0 {
		return Result{}, apperror.New(400, "ref and values are required", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	detail, err := s.Select(ctx, ref, values)
	if err != nil {
		return Result{}, err
	}
	return done(s, detail)
}

// Fill sets several form fields at once.
func (a *App) Fill(ctx context.Context, convID string, fields []dservice.BrowserField) (Result, error) {
	if len(fields) == 0 {
		return Result{}, apperror.New(400, "fields are required", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	texts := make([]string, len(fields))
	for i, f := range fields {
		texts[i] = f.Value
	}
	values, err := a.secretValues(ctx, convID, texts...)
	if err != nil {
		return Result{}, err
	}
	detail, err := a.fillSecrets(ctx, convID, s, fields, values)
	if err != nil {
		return Result{}, err
	}
	return done(s, detail)
}

// WaitInput is exactly one of a pause in seconds, text to appear, or text to
// disappear.
type WaitInput struct {
	Time     float64 `json:"time"`
	Text     string  `json:"text"`
	TextGone string  `json:"textGone"`
}

// WaitFor waits on the page.
func (a *App) WaitFor(ctx context.Context, convID string, in WaitInput) (Result, error) {
	set := 0
	for _, on := range []bool{in.Time > 0, in.Text != "", in.TextGone != ""} {
		if on {
			set++
		}
	}
	if set != 1 {
		return Result{}, apperror.New(400, "give exactly one of time, text, or textGone", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	switch {
	case in.Time > 0:
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-time.After(min(time.Duration(in.Time*float64(time.Second)), maxPause)):
		}
	case in.Text != "":
		err = s.WaitText(ctx, in.Text, false)
	default:
		err = s.WaitText(ctx, in.TextGone, true)
	}
	if err != nil {
		return Result{}, err
	}
	return done(s, "")
}

// Evaluate runs JavaScript: an expression, or a function called with no
// arguments.
func (a *App) Evaluate(ctx context.Context, convID, expression, function string) (Result, error) {
	expr := strings.TrimSpace(expression)
	if fn := strings.TrimSpace(function); fn != "" {
		expr = "(" + fn + ")()"
	}
	if expr == "" {
		return Result{}, apperror.New(400, "expression or function is required", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	raw, err := s.Eval(ctx, expr)
	if err != nil {
		return Result{}, err
	}
	return done(s, "result: "+string(raw))
}

// Console returns the page's console messages.
func (a *App) Console(ctx context.Context, convID string) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	raw, err := s.Console(ctx)
	if err != nil {
		return Result{}, err
	}
	var msgs []struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	}
	_ = json.Unmarshal(raw, &msgs)
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s] %s\n", m.Level, m.Text)
	}
	if b.Len() == 0 {
		b.WriteString("no console messages")
	}
	return done(s, strings.TrimSpace(b.String()))
}

// Resize sets the viewport size, keeping the rest of the device.
func (a *App) Resize(ctx context.Context, convID string, width, height int) (Result, error) {
	if width <= 0 || height <= 0 {
		return Result{}, apperror.New(400, "width and height are required", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	if err := s.UpdateDevice(ctx, func(d dservice.BrowserDevice) (dservice.BrowserDevice, error) {
		d.Preset, d.Width, d.Height = "custom", width, height
		return d, nil
	}); err != nil {
		return Result{}, err
	}
	return withShot(ctx, s)
}

// Presets are the named devices: desktop restores a plain viewport with
// the real user agent; the others are touch devices with a matching agent.
var Presets = map[string]dservice.BrowserDevice{
	"desktop": {Preset: "desktop", Width: 1280, Height: 800, DPR: 1},
	"mobile": {Preset: "mobile", Width: 390, Height: 844, DPR: 3, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
	"tablet": {Preset: "tablet", Width: 768, Height: 1024, DPR: 2, Mobile: true, Touch: true,
		UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"},
}

// DevicePatch changes part of a device; nil keeps a field.
type DevicePatch struct {
	Preset    string   `json:"preset"`
	Width     *int     `json:"width"`
	Height    *int     `json:"height"`
	DPR       *float64 `json:"dpr"`
	Mobile    *bool    `json:"mobile"`
	UserAgent *string  `json:"ua"`
	Touch     *bool    `json:"touch"`
}

// applyDevicePatch runs the patch against the session's device in one
// transaction: preset first, then the field overrides, then the custom
// marker when the result matches no preset.
func applyDevicePatch(ctx context.Context, s dservice.BrowserSession, p DevicePatch) error {
	return s.UpdateDevice(ctx, func(d dservice.BrowserDevice) (dservice.BrowserDevice, error) {
		switch p.Preset {
		case "responsive":
			if p.DPR != nil || p.Mobile != nil || p.UserAgent != nil || p.Touch != nil {
				return d, apperror.New(400, "responsive only accepts width and height overrides", nil)
			}
			d = dservice.BrowserDevice{Preset: "responsive", Width: d.Width, Height: d.Height, DPR: 1}
		case "":
			if p.Width == nil && p.Height == nil && p.DPR == nil && p.Mobile == nil && p.UserAgent == nil && p.Touch == nil {
				return d, apperror.New(400, "give a preset or device fields", nil)
			}
			d.Preset = "custom"
		default:
			base, ok := Presets[p.Preset]
			if !ok {
				return d, apperror.New(400, "preset must be responsive, desktop, mobile, or tablet", nil)
			}
			d = base
		}
		base := d
		if p.Width != nil {
			d.Width = *p.Width
		}
		if p.Height != nil {
			d.Height = *p.Height
		}
		if p.DPR != nil {
			d.DPR = *p.DPR
		}
		if p.Mobile != nil {
			d.Mobile = *p.Mobile
		}
		if p.UserAgent != nil {
			d.UserAgent = *p.UserAgent
		}
		if p.Touch != nil {
			d.Touch = *p.Touch
		} else if p.Mobile != nil {
			d.Touch = d.Mobile
		}
		if d.Preset != "responsive" && d != base {
			d.Preset = "custom"
		}
		return d, nil
	})
}

// Emulate applies a preset and/or changes to the current device. Turning
// mobile on or off without saying touch moves touch with it.
func (a *App) Emulate(ctx context.Context, convID string, p DevicePatch) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	if err := applyDevicePatch(ctx, s, p); err != nil {
		return Result{}, err
	}
	return withShot(ctx, s)
}

// Profile moves the thread's tab to a profile ("" or default for the
// default one); the page reloads there.
func (a *App) Profile(ctx context.Context, convID, profile string) (Result, error) {
	if _, err := a.session(ctx, convID); err != nil {
		return Result{}, err
	}
	s, err := a.browser.SwitchProfile(ctx, convID, profile)
	if err != nil {
		return Result{}, err
	}
	return done(s, "profile: "+s.Profile())
}

// Profiles lists the browser profiles.
func (a *App) Profiles() []string { return a.browser.Profiles() }

// Lock takes (on) or releases the input lease for the agent: while held,
// viewers watch but cannot drive, until one takes over.
func (a *App) Lock(ctx context.Context, convID string, on bool) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	s.SetAgentLock(on)
	state := "released: viewers can take over"
	if on {
		state = "locked: viewers cannot drive until one takes over"
	}
	return done(s, "input "+state)
}

// Input is one thing a viewer does in the live view. Coordinates are CSS
// pixels in the viewport.
type Input struct {
	Type       string  `json:"type"` // takeover, release, move, down, up, click, wheel, touch, key, text, navigate, back, forward, emulate, fit
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Button     string  `json:"button"`
	ClickCount int     `json:"clickCount"`
	Buttons    int     `json:"buttons"`
	DeltaX     float64 `json:"deltaX"`
	DeltaY     float64 `json:"deltaY"`
	Phase      string  `json:"phase"`
	Key        string  `json:"key"`
	Modifiers  int     `json:"modifiers"`
	Text       string  `json:"text"`
	URL        string  `json:"url"`
	Preset     string  `json:"preset"`
	// Width and Height are the viewport a "fit" asks for: the viewer's pane.
	Width  int `json:"width"`
	Height int `json:"height"`
	// A login offer's answer, or the saved login to fill.
	ID       string  `json:"id"`
	Save     bool    `json:"save"`
	Username *string `json:"username"`
	Password *string `json:"password"`
}

// The sides a viewer's pane may ask the viewport to have.
const (
	minViewport = 200
	maxViewport = 4096
)

// ErrNotDriving refuses input from a viewer that does not hold the lease.
var ErrNotDriving = apperror.New(409, "take over the browser first", nil)

// ViewerDrive applies a viewer's input to the session its watch opened —
// the same tab the frames came from, even after a profile switch replaced
// the thread's session.
type ViewerDrive func(context.Context, string, Input) (bool, error)

// drive applies a viewer's input on s. Takeover, release, and the other
// pre-lease cases need no lease — fit only sizes a responsive tab and the
// driver, if any, owns the size; everything else needs this viewer to hold
// the lease, with the agent not locked. It reports whether the focused
// element takes text, for touch keyboards.
func (a *App) drive(ctx context.Context, convID string, s dservice.BrowserSession, viewer string, in Input) (bool, error) {
	switch in.Type {
	case "fit":
		// Sizing a Responsive tab is not taking over its input. The driver, if any, owns the size.
		return false, s.UpdateDevice(ctx, func(d dservice.BrowserDevice) (dservice.BrowserDevice, error) {
			if l := s.Lease(); d.Preset != "responsive" || (l.Controller != "" && l.Controller != viewer) {
				return d, nil // stale size report or another viewer is driving
			}
			if in.Width < minViewport || in.Width > maxViewport || in.Height < minViewport || in.Height > maxViewport {
				return d, apperror.New(400, fmt.Sprintf("a viewport is %d to %d px on each side", minViewport, maxViewport), nil)
			}
			d.Width, d.Height = in.Width, in.Height
			return d, nil
		})
	case "takeover":
		s.Takeover(viewer)
		return false, nil
	case "release":
		s.Release(viewer)
		return false, nil
	case "login":
		// Answering the save question is the owner's, lease or not.
		return false, a.DecideLogin(ctx, convID, in.ID, in.Save, in.Username, in.Password)
	case "record":
		_, err := a.StartRecording(ctx, convID, "")
		return false, err
	case "pause_recording":
		return false, a.PauseRecording(convID)
	case "resume_recording":
		return false, a.ResumeRecording(convID)
	case "stop_recording":
		_, err := a.StopRecording(convID)
		return false, err
	}
	if l := s.Lease(); l.AgentLock || l.Controller != viewer {
		return false, ErrNotDriving
	}
	var err error
	switch in.Type {
	case "move":
		err = s.Move(ctx, in.X, in.Y, in.Buttons)
	case "down", "up":
		err = s.Press(ctx, in.X, in.Y, in.Button, in.ClickCount, in.Type == "down")
	case "click":
		err = s.Click(ctx, in.X, in.Y, in.Button, in.ClickCount)
	case "wheel":
		err = s.Scroll(ctx, in.X, in.Y, in.DeltaX, in.DeltaY)
	case "touch":
		err = s.Touch(ctx, in.Phase, in.X, in.Y)
		if err == nil && in.Phase == "touchEnd" {
			return s.FocusedEditable(ctx)
		}
	case "key":
		err = s.Key(ctx, in.Key, in.Modifiers)
	case "text":
		err = s.Type(ctx, in.Text)
	case "navigate":
		var target string
		if target, err = normalizeURL(in.URL); err == nil {
			err = s.Navigate(ctx, target)
		}
	case "back", "forward":
		step := -1
		if in.Type == "forward" {
			step = 1
		}
		err = s.History(ctx, step)
	case "emulate":
		err = applyDevicePatch(ctx, s, DevicePatch{Preset: in.Preset})
	case "autofill":
		var filled bool
		if filled, err = a.Autofill(ctx, convID, in.ID); err == nil && !filled {
			err = apperror.New(409, "the page has no login form to fill", nil)
		}
	default:
		err = apperror.New(400, "unknown input "+in.Type, nil)
	}
	return false, err
}

// Status is the thread's browser, if one runs.
func (a *App) Status(convID string) (dservice.BrowserState, bool) {
	s, ok := a.browser.Session(convID)
	if !ok {
		return dservice.BrowserState{}, false
	}
	return s.State(), true
}

// Watch streams the thread's live browser view and hands back the drive
// bound to that very session, so viewer input reaches the tab the watcher
// sees. It needs a running session; watching never starts one.
func (a *App) Watch(ctx context.Context, convID string) (<-chan dservice.BrowserFrame, ViewerDrive, error) {
	s, ok := a.browser.Session(convID)
	if !ok {
		return nil, nil, apperror.NewNotFoundError("browser session", nil)
	}
	return s.Watch(ctx), func(ctx context.Context, viewer string, in Input) (bool, error) {
		return a.drive(ctx, convID, s, viewer, in)
	}, nil
}

// Close ends the thread's browser session, finishing its recording, and
// forgets its secrets.
func (a *App) Close(convID string) {
	if _, err := a.liveRecording(convID); err == nil {
		_, _ = a.StopRecording(convID)
	}
	a.browser.Close(convID)
	a.mu.Lock()
	delete(a.masks, convID)
	delete(a.pending, convID)
	a.mu.Unlock()
}
