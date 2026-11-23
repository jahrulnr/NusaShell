package browser

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

const (
	callWait      = 15 * time.Second
	navWait       = 20 * time.Second
	textWait      = 10 * time.Second
	castQuality   = 60
	shotQuality   = 75
	watcherBuffer = 4
)

// ErrUnavailable is a browser that could not do what was asked.
var ErrUnavailable = errors.New("the browser is unavailable")

// ErrRefNotFound is a stale or unknown element ref.
var ErrRefNotFound = errors.New("element ref not found — run browser_snapshot again")

// Session is one tab, bound to a thread.
type Session struct {
	id      string
	profile string
	ctx     context.Context // the tab
	cancel  context.CancelFunc
	log     *slog.Logger

	emuMu sync.Mutex // one device change at a time
	// annotating is a mark in flight; marks run one at a time.
	annotating atomic.Bool
	mu         sync.Mutex
	page       dservice.BrowserPage
	device     dservice.BrowserDevice
	lease      dservice.BrowserLease
	offer      *dservice.BrowserLoginOffer
	recState   *dservice.BrowserRecording
	pointer    *dservice.BrowserPointer
	used       time.Time
	watchers   map[chan dservice.BrowserFrame]struct{}
	castStop   context.CancelFunc

	// routeMu guards the request routes and the Fetch domain's state;
	// routeWatching means a dispatcher already runs.
	routeMu       sync.Mutex
	routes        []*routeRule
	routeArmed    bool
	routeWatching bool
	// initMu guards the document-start scripts: init scripts and the
	// WebSocket route shim.
	initMu      sync.Mutex
	initScripts []*initScript
	wsRoutes    []*wsRoute
	wsShimID    page.ScriptIdentifier
	// recCap is the live recording's capture, nil while not recording.
	recCap *recCapture
	rec    dservice.RecordingSink
	// netMu guards the network log.
	netMu      sync.Mutex
	netLog     []*NetEntry
	netPending map[network.RequestID]*NetEntry
	netSeq     int64
}

var _ dservice.BrowserSession = (*Session)(nil)

func newSession(id, profile string, tabCtx context.Context, cancel context.CancelFunc, log *slog.Logger, onLoad func() func(dservice.BrowserLoaded)) *Session {
	s := &Session{id: id, profile: profile, ctx: tabCtx, cancel: cancel, log: log.With("conversation", id),
		page: dservice.BrowserPage{URL: "about:blank"}, used: time.Now(),
		watchers: map[chan dservice.BrowserFrame]struct{}{}, netPending: map[network.RequestID]*NetEntry{}}
	go s.followNavigation(onLoad)
	s.watchNetLog()
	return s
}

func (s *Session) tab() *chromedp.Target { return chromedp.FromContext(s.ctx).Target }

func newID(prefix string) string { return prefix + "_" + strings.ToLower(rand.Text()[:10]) }

// reload reloads the page and waits for it to load.
func (s *Session) reload(ctx context.Context) error {
	c, cancel := s.call(ctx, navWait+5*time.Second)
	defer cancel()
	sub, unsub := context.WithTimeout(c, navWait)
	defer unsub()
	loaded := chromedp.Events(sub, page.LoadEventFired)
	if _, err := chromedp.Call(c, page.Reload, page.ReloadParams{}); err != nil {
		return s.fail(err)
	}
	for range loaded {
		break
	}
	s.refresh()
	return nil
}

// inherit carries the routes, WebSocket routes, and init scripts of the
// session this one replaces (a profile switch) and installs them here.
func (s *Session) inherit(old *Session) {
	old.routeMu.Lock()
	routes := old.routes
	old.routeMu.Unlock()
	old.initMu.Lock()
	scripts, ws := old.initScripts, old.wsRoutes
	old.initMu.Unlock()
	s.routeMu.Lock()
	s.routes = routes
	s.routeMu.Unlock()
	s.initMu.Lock()
	s.initScripts, s.wsRoutes = scripts, ws
	for _, sc := range s.initScripts {
		sc.identifier = ""
	}
	s.initMu.Unlock()
	if err := s.applyDocumentScripts(); err != nil {
		s.log.Warn("document scripts not carried to the new profile", "error", err)
	}
	if err := s.armRoutes(); err != nil {
		s.log.Warn("browser routes not carried to the new profile", "error", err)
	}
}

type NetEntry = dservice.BrowserNetEntry

const (
	netLogCap     = 300
	netPendingCap = 256
)

// watchNetLog fills the network log for the tab's whole life. One ordered
// listener sees a request before its response or failure, and has seen
// an event before the command that follows it returns.
func (s *Session) watchNetLog() {
	err := chromedp.Listen(s.ctx, func(ev any) {
		switch ev := ev.(type) {
		case *network.EventRequestWillBeSent:
			e := &NetEntry{TS: time.Now().UnixMilli(), Type: string(ev.Type)}
			if ev.Request != nil {
				e.Method, e.URL = ev.Request.Method, ev.Request.URL
			}
			s.netMu.Lock()
			if len(s.netPending) >= netPendingCap {
				for id := range s.netPending {
					delete(s.netPending, id)
					break
				}
			}
			s.netPending[ev.RequestID] = e
			s.netMu.Unlock()
		case *network.EventResponseReceived:
			s.netMu.Lock()
			e := s.takePendingLocked(ev.RequestID)
			if ev.Response != nil {
				e.Status, e.URL, e.Type = ev.Response.Status, ev.Response.URL, string(ev.Type)
			}
			s.pushNetEntry(e)
			s.netMu.Unlock()
		case *network.EventLoadingFailed:
			if ev.Canceled {
				return
			}
			s.netMu.Lock()
			e := s.takePendingLocked(ev.RequestID)
			e.Error = ev.ErrorText
			s.pushNetEntry(e)
			s.netMu.Unlock()
		}
	})
	if err != nil {
		s.log.Warn("browser network log unavailable", "error", err)
	}
}

// takePendingLocked removes and returns the request's entry, or a fresh
// one for a request seen only by its outcome. Callers hold netMu.
func (s *Session) takePendingLocked(id network.RequestID) *NetEntry {
	if e := s.netPending[id]; e != nil {
		delete(s.netPending, id)
		return e
	}
	return &NetEntry{TS: time.Now().UnixMilli()}
}

func (s *Session) pushNetEntry(e *NetEntry) {
	s.netSeq++
	e.Seq = s.netSeq
	s.netLog = append(s.netLog, e)
	if len(s.netLog) > netLogCap {
		s.netLog = s.netLog[len(s.netLog)-netLogCap:]
	}
}

// NetLog is the network log, oldest first.
func (s *Session) NetLog() []NetEntry {
	s.netMu.Lock()
	defer s.netMu.Unlock()
	out := make([]NetEntry, len(s.netLog))
	for i, e := range s.netLog {
		out[i] = *e
	}
	return out
}

// Profile is the profile the tab runs in.
func (s *Session) Profile() string { return s.profile }

// Device is the current device profile.
func (s *Session) Device() dservice.BrowserDevice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.device
}

// State is what a viewer shows around the picture.
func (s *Session) State() dservice.BrowserState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

func (s *Session) stateLocked() dservice.BrowserState {
	return dservice.BrowserState{Page: s.page, Device: s.device, Lease: s.lease, Profile: s.profile, LoginOffer: s.offer, Recording: s.recState, AgentPointer: s.pointer}
}

// ShowAgentPointer tells viewers where the agent acts next.
func (s *Session) ShowAgentPointer(x, y float64, action string) {
	s.mu.Lock()
	var seq int64 = 1
	if s.pointer != nil {
		seq = s.pointer.Seq + 1
	}
	s.pointer = &dservice.BrowserPointer{X: x, Y: y, Action: action, Seq: seq}
	s.mu.Unlock()
	s.changed()
}

// changed tells viewers the state moved. Capturing and enqueueing the
// snapshot hold s.mu so two concurrent updates cannot enqueue an old
// snapshot last.
func (s *Session) changed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked()
	s.broadcastLocked(dservice.BrowserFrame{State: &st})
}

// UpdateDevice reads the current device, applies change, and writes the
// result in one transaction under the device lock.
func (s *Session) UpdateDevice(ctx context.Context, change func(dservice.BrowserDevice) (dservice.BrowserDevice, error)) error {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.ended() {
		return s.fail(s.ctx.Err())
	}
	prev := s.Device()
	d, err := change(prev)
	if err != nil {
		return err
	}
	d.Width, d.Height = min(max(d.Width, 200), 4096), min(max(d.Height, 200), 4096)
	if d.DPR <= 0 {
		d.DPR = 1
	}
	d.DPR = min(d.DPR, 5)
	if d == prev {
		return nil
	}
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	if _, err := chromedp.Call(c, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
		Width: int64(d.Width), Height: int64(d.Height), DeviceScaleFactor: d.DPR, Mobile: d.Mobile}); err != nil {
		return s.fail(err)
	}
	if d.UserAgent != prev.UserAgent {
		if _, err := chromedp.Call(c, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{UserAgent: d.UserAgent}); err != nil {
			return s.fail(err)
		}
	}
	if d.Touch != prev.Touch {
		if _, err := chromedp.Call(c, emulation.SetTouchEmulationEnabled, emulation.SetTouchEmulationEnabledParams{Enabled: d.Touch, MaxTouchPoints: 5}); err != nil {
			return s.fail(err)
		}
	}
	s.mu.Lock()
	s.device = d
	s.page.Width, s.page.Height = d.Width, d.Height
	s.mu.Unlock()
	s.changed()
	return nil
}

// Lease is who drives input now.
func (s *Session) Lease() dservice.BrowserLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lease
}

// Takeover gives viewer the input, ending the agent's lock.
func (s *Session) Takeover(viewer string) {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()
	s.mu.Lock()
	s.lease = dservice.BrowserLease{Controller: viewer}
	s.mu.Unlock()
	s.changed()
}

// Release hands the input back.
func (s *Session) Release(viewer string) {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()
	s.mu.Lock()
	if s.lease.Controller != viewer {
		s.mu.Unlock()
		return
	}
	s.lease.Controller = ""
	s.mu.Unlock()
	s.changed()
}

// SetAgentLock keeps viewers out while on.
func (s *Session) SetAgentLock(on bool) {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()
	s.mu.Lock()
	s.lease.AgentLock = on
	if on {
		s.lease.Controller = ""
	}
	s.mu.Unlock()
	s.changed()
}

// Done is closed when the tab is gone.
func (s *Session) Done() <-chan struct{} { return s.ctx.Done() }

func (s *Session) ended() bool { return s.ctx.Err() != nil }

func (s *Session) close() { s.cancel() }

func (s *Session) touch() {
	s.mu.Lock()
	s.used = time.Now()
	s.mu.Unlock()
}

// idleFor reports a session no tool used for d and nobody watches.
func (s *Session) idleFor(d time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.watchers) == 0 && time.Since(s.used) > d
}

// call is a tab context that also ends with the caller's ctx.
func (s *Session) call(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if err := s.ctx.Err(); err != nil {
		c, cancel := context.WithCancel(context.Background())
		cancel()
		return c, cancel
	}
	s.touch()
	c, cancel := context.WithTimeout(s.ctx, d)
	stop := context.AfterFunc(ctx, cancel)
	return c, func() { stop(); cancel() }
}

// fail maps a CDP error: a closed tab is the session ending.
func (s *Session) fail(err error) error {
	if err == nil {
		return nil
	}
	if s.ctx.Err() != nil {
		return apperror.New(410, "the browser session ended", err)
	}
	return err
}

// Page is the current page and viewport.
func (s *Session) Page() dservice.BrowserPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.page
}

// followNavigation keeps Page current when the page moves on its own (a
// click that navigates) and tells viewers; each finished load is reported
// to onLoad's handler.
func (s *Session) followNavigation(onLoad func() func(dservice.BrowserLoaded)) {
	nav := chromedp.Events(s.ctx, page.FrameNavigated)
	load := chromedp.Events(s.ctx, page.LoadEventFired)
	go func() {
		for _, err := range load {
			if err != nil {
				return
			}
			if !s.refresh() {
				continue
			}
			if fn := onLoad(); fn != nil {
				p := s.Page()
				fn(dservice.BrowserLoaded{ConversationID: s.id, Profile: s.Profile(), URL: p.URL, Title: p.Title})
			}
		}
	}()
	for ev, err := range nav {
		if err != nil {
			return
		}
		if ev.Frame != nil && ev.Frame.ParentID == "" {
			s.mu.Lock()
			s.page.URL = ev.Frame.URL
			s.mu.Unlock()
			s.note("browser", "navigate", map[string]any{"url": ev.Frame.URL})
			go s.refresh()
		}
	}
}

// refresh reads the page's url and title and tells viewers; false when
// the page did not answer.
func (s *Session) refresh() bool {
	c, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	res, err := chromedp.Call(c, cdpruntime.Evaluate, cdpruntime.EvaluateParams{
		Expression: `({url: location.href, title: document.title})`, ReturnByValue: new(true)})
	if err != nil || res.Result == nil {
		return false
	}
	var p struct{ URL, Title string }
	if json.Unmarshal(res.Result.Value, &p) != nil {
		return false
	}
	s.mu.Lock()
	s.page.URL, s.page.Title = p.URL, p.Title
	s.mu.Unlock()
	s.changed()
	return true
}

// Navigate loads url and waits for its load event (a slow page resolves at
// the timeout).
func (s *Session) Navigate(ctx context.Context, url string) error {
	c, cancel := s.call(ctx, navWait+5*time.Second)
	defer cancel()
	// Subscribe before navigating so the load event cannot be missed; a
	// slow page resolves when the wait runs out.
	subCtx, unsub := context.WithTimeout(c, navWait)
	defer unsub()
	loaded := chromedp.Events(subCtx, page.LoadEventFired)
	res, err := chromedp.Call(c, page.Navigate, page.NavigateParams{URL: url})
	if err != nil {
		return s.fail(err)
	}
	if res.ErrorText != "" {
		return fmt.Errorf("navigation failed: %s", res.ErrorText)
	}
	if res.LoaderID != "" {
		for range loaded {
			break
		}
	}
	s.refresh()
	return nil
}

// History moves back or forward one entry.
func (s *Session) History(ctx context.Context, step int) error {
	c, cancel := s.call(ctx, navWait+5*time.Second)
	defer cancel()
	hist, err := chromedp.Call(c, page.GetNavigationHistory, cdp.Empty{})
	if err != nil {
		return s.fail(err)
	}
	i := int(hist.CurrentIndex) + step
	if i < 0 || i >= len(hist.Entries) {
		return nil
	}
	subCtx, unsub := context.WithTimeout(c, navWait)
	defer unsub()
	committed := chromedp.Events(subCtx, page.FrameNavigated)
	if _, err := chromedp.Call(c, page.NavigateToHistoryEntry, page.NavigateToHistoryEntryParams{EntryID: hist.Entries[i].ID}); err != nil {
		return s.fail(err)
	}
	for ev, err := range committed {
		if err != nil || ev.Frame == nil || ev.Frame.ParentID == "" {
			break
		}
	}
	s.refresh()
	return nil
}

// Eval runs expression in the page and returns its JSON value. Promises are
// awaited; values JSON cannot hold (NaN, 10n) come back as strings.
func (s *Session) Eval(ctx context.Context, expression string) (json.RawMessage, error) {
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	res, err := chromedp.Call(c, cdpruntime.Evaluate, cdpruntime.EvaluateParams{
		Expression: expression, AwaitPromise: new(true), ReturnByValue: new(true)})
	if err != nil {
		return nil, s.fail(err)
	}
	if ex := res.ExceptionDetails; ex != nil {
		desc := ex.Text
		if ex.Exception != nil && ex.Exception.Description != "" {
			desc = ex.Exception.Description
		}
		return nil, apperror.New(422, "javascript error: "+desc, nil)
	}
	if res.Result == nil {
		return json.RawMessage("null"), nil
	}
	if u := string(res.Result.UnserializableValue); u != "" {
		if u == "-0" {
			return json.RawMessage("-0"), nil
		}
		return json.Marshal(u)
	}
	if len(res.Result.Value) == 0 {
		return json.RawMessage("null"), nil
	}
	return res.Result.Value, nil
}

// Snapshot is the page as an indented tree with element refs.
func (s *Session) Snapshot(ctx context.Context) (string, error) {
	raw, err := s.Eval(ctx, snapshotJS)
	if err != nil {
		return "", err
	}
	var tree string
	if err := json.Unmarshal(raw, &tree); err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	return tree, nil
}

// Screenshot is a JPEG of the viewport.
func (s *Session) Screenshot(ctx context.Context) ([]byte, error) {
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	q := int64(shotQuality)
	res, err := chromedp.Call(c, page.CaptureScreenshot, page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormatJpeg, Quality: &q})
	if err != nil {
		return nil, s.fail(err)
	}
	return res.Data, nil
}

// Point resolves a ref to the center of its element.
func (s *Session) Point(ctx context.Context, ref string) (float64, float64, error) {
	raw, err := s.Eval(ctx, actionPointExpr(ref))
	if err != nil {
		return 0, 0, err
	}
	var p *struct {
		Status string  `json:"status"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		By     string  `json:"by"`
	}
	if json.Unmarshal(raw, &p) != nil || p == nil {
		return 0, 0, ErrRefNotFound
	}
	switch p.Status {
	case "ok":
		return p.X, p.Y, nil
	case "missing":
		return 0, 0, ErrRefNotFound
	case "covered":
		by := p.By
		if by == "" {
			by = "another element"
		}
		return 0, 0, fmt.Errorf("%s is covered by %s", ref, by)
	case "pointer-events-none":
		return 0, 0, fmt.Errorf("%s does not take pointer events", ref)
	}
	return 0, 0, fmt.Errorf("%s is %s", ref, strings.ReplaceAll(p.Status, "-", " "))
}

// Focus focuses a ref's element.
func (s *Session) Focus(ctx context.Context, ref string) (bool, error) {
	raw, err := s.Eval(ctx, focusExpr(ref))
	if err != nil {
		return false, err
	}
	var res *struct {
		Status  string `json:"status"`
		Focused bool   `json:"focused"`
	}
	if json.Unmarshal(raw, &res) != nil || res == nil || res.Status == "missing" {
		return false, ErrRefNotFound
	}
	if res.Status != "ok" {
		return false, fmt.Errorf("%s is %s", ref, res.Status)
	}
	return res.Focused, nil
}

// Select picks options of a <select> by value or label.
func (s *Session) Select(ctx context.Context, ref string, values []string) (string, error) {
	raw, err := s.Eval(ctx, selectExpr(ref, values))
	if err != nil {
		return "", err
	}
	var res *struct {
		Status string `json:"status"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &res) != nil || res == nil || res.Status == "missing" {
		return "", ErrRefNotFound
	}
	if res.Status != "ok" {
		return "", errors.New(res.Detail)
	}
	return res.Detail, nil
}

// Fill sets several fields through their native setters.
func (s *Session) Fill(ctx context.Context, fields []dservice.BrowserField) (string, error) {
	in := make([]FieldValue, len(fields))
	for i, f := range fields {
		in[i] = FieldValue{Ref: f.Ref, Name: f.Name, Type: f.Type, Value: f.Value}
	}
	raw, err := s.Eval(ctx, fillExpr(in))
	if err != nil {
		return "", err
	}
	var res struct {
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("fill: %w", err)
	}
	if !res.OK {
		if res.Reason == "ref not found" {
			return "", fmt.Errorf("%w: %s", ErrRefNotFound, res.Detail)
		}
		return "", errors.New(res.Detail)
	}
	return res.Detail, nil
}

// WaitText polls the page text until text appears or, gone, disappears.
func (s *Session) WaitText(ctx context.Context, text string, gone bool) error {
	needle, _ := json.Marshal(text)
	expr := fmt.Sprintf(`(document.body ? document.body.innerText : "").includes(%s)`, needle)
	deadline := time.Now().Add(textWait)
	for {
		raw, err := s.Eval(ctx, expr)
		if err != nil && s.ctx.Err() != nil {
			return s.fail(err)
		}
		var found bool
		if err == nil && json.Unmarshal(raw, &found) == nil && found != gone {
			return nil
		}
		if time.Now().After(deadline) {
			want := "appear"
			if gone {
				want = "disappear"
			}
			return fmt.Errorf("timed out waiting for %q to %s", text, want)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Console is the page's captured console.
func (s *Session) Console(ctx context.Context) (json.RawMessage, error) {
	return s.Eval(ctx, `window.__nusaConsole || []`)
}

// Move moves the pointer; buttons is the held-buttons mask of a drag.
func (s *Session) Move(ctx context.Context, x, y float64, buttons int) error {
	if s.Device().Touch && buttons > 0 {
		return s.Touch(ctx, "touchMove", x, y)
	}
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	_, err := chromedp.Call(c, input.DispatchMouseEvent, input.DispatchMouseEventParams{Type: input.DispatchMouseEventTypeMouseMoved, X: x, Y: y, Buttons: int64(buttons)})
	return s.fail(err)
}

// Click presses and releases a mouse button at a point; on a touch device
// it taps.
func (s *Session) Click(ctx context.Context, x, y float64, button string, count int) error {
	if s.Device().Touch {
		if err := s.Touch(ctx, "touchStart", x, y); err != nil {
			return err
		}
		return s.Touch(ctx, "touchEnd", x, y)
	}
	if err := s.Move(ctx, x, y, 0); err != nil {
		return err
	}
	if err := s.Press(ctx, x, y, button, count, true); err != nil {
		return err
	}
	return s.Press(ctx, x, y, button, count, false)
}

// Press is one half of a click, for drags.
func (s *Session) Press(ctx context.Context, x, y float64, button string, count int, down bool) error {
	if s.Device().Touch {
		phase := "touchEnd"
		if down {
			phase = "touchStart"
		}
		return s.Touch(ctx, phase, x, y)
	}
	btn := input.MouseButton(button)
	if btn == "" {
		btn = input.MouseButtonLeft
	}
	t := input.DispatchMouseEventTypeMouseReleased
	if down {
		t = input.DispatchMouseEventTypeMousePressed
	}
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	_, err := chromedp.Call(c, input.DispatchMouseEvent,
		input.DispatchMouseEventParams{Type: t, X: x, Y: y, Button: btn, ClickCount: int64(max(count, 1))})
	return s.fail(err)
}

// Touch is one touch phase at a point.
func (s *Session) Touch(ctx context.Context, phase string, x, y float64) error {
	t := input.DispatchTouchEventType(phase)
	points := []*input.TouchPoint{}
	switch t {
	case input.DispatchTouchEventTypeTouchStart, input.DispatchTouchEventTypeTouchMove:
		points = append(points, &input.TouchPoint{X: x, Y: y})
	case input.DispatchTouchEventTypeTouchEnd, input.DispatchTouchEventTypeTouchCancel:
	default:
		return fmt.Errorf("invalid touch phase %q", phase)
	}
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	_, err := chromedp.Call(c, input.DispatchTouchEvent, input.DispatchTouchEventParams{Type: t, TouchPoints: points})
	return s.fail(err)
}

// FocusedEditable reports whether the focused element takes text.
func (s *Session) FocusedEditable(ctx context.Context) (bool, error) {
	raw, err := s.Eval(ctx, `(() => { let e = document.activeElement; while (e && e.shadowRoot && e.shadowRoot.activeElement) e = e.shadowRoot.activeElement; if (!e) return false; if (e.isContentEditable) return true; if (e.disabled || e.readOnly) return false; if (e.tagName === 'TEXTAREA') return true; return e.tagName === 'INPUT' && !['button','checkbox','color','file','hidden','image','radio','range','reset','submit'].includes(e.type); })()`)
	if err != nil {
		return false, err
	}
	var editable bool
	err = json.Unmarshal(raw, &editable)
	return editable, err
}

// Type inserts text into the focused element.
func (s *Session) Type(ctx context.Context, text string) error {
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	_, err := chromedp.Call(c, input.InsertText, input.InsertTextParams{Text: text})
	return s.fail(err)
}

var domKeyRunes = func() map[string]rune {
	m := map[string]rune{}
	for r, k := range kb.Keys {
		if _, ok := m[k.Key]; !ok {
			m[k.Key] = r
		}
	}
	return m
}()

// Key presses one key by its DOM name, with a CDP modifier mask.
func (s *Session) Key(ctx context.Context, name string, modifiers int) error {
	var r rune
	if rs := []rune(name); len(rs) == 1 {
		r = rs[0]
	} else if k, ok := domKeyRunes[name]; ok {
		r = k
	} else {
		return fmt.Errorf("unknown key %q", name)
	}
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	for _, p := range kb.Encode(r) {
		p.Modifiers |= int64(modifiers)
		if _, err := chromedp.Call(c, input.DispatchKeyEvent, *p); err != nil {
			return s.fail(err)
		}
	}
	return nil
}

// Scroll turns the wheel at a point.
func (s *Session) Scroll(ctx context.Context, x, y, dx, dy float64) error {
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	// Chromium wants both deltas on every wheel event, and the typed
	// params drop zero values — so the raw call carries them.
	params := struct {
		Type   input.DispatchMouseEventType `json:"type"`
		X      float64                      `json:"x"`
		Y      float64                      `json:"y"`
		DeltaX float64                      `json:"deltaX"`
		DeltaY float64                      `json:"deltaY"`
	}{Type: input.DispatchMouseEventTypeMouseWheel, X: x, Y: y, DeltaX: dx, DeltaY: dy}
	return s.fail(chromedp.FromContext(c).Target.Call(c, "Input.dispatchMouseEvent", params, nil))
}

// Watch streams frames to one viewer. The screencast runs while anyone
// watches.
func (s *Session) Watch(ctx context.Context) <-chan dservice.BrowserFrame {
	ch := make(chan dservice.BrowserFrame, watcherBuffer)
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		close(ch)
		return ch
	}
	s.watchers[ch] = struct{}{}
	first := len(s.watchers) == 1
	st := s.stateLocked()
	ch <- dservice.BrowserFrame{State: &st}
	s.mu.Unlock()
	// A still page sends no screencast frame, so every viewer starts from
	// a screenshot.
	if shot, err := s.Screenshot(ctx); err == nil {
		s.mu.Lock()
		select {
		case ch <- dservice.BrowserFrame{JPEG: shot}:
		default:
		}
		s.mu.Unlock()
	}
	if first {
		s.startCast()
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-s.ctx.Done():
		}
		s.mu.Lock()
		delete(s.watchers, ch)
		last := len(s.watchers) == 0
		stop := s.castStop
		if last {
			s.castStop = nil
		}
		s.mu.Unlock()
		if last && stop != nil {
			stop()
		}
		close(ch)
	}()
	return ch
}

func (s *Session) broadcast(f dservice.BrowserFrame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastLocked(f)
}

// broadcastLocked sends f to every watcher; the caller holds s.mu. A full
// state queue drops its oldest frame for the new state — viewers never
// need a stale snapshot — while a full JPEG queue just skips a frame.
func (s *Session) broadcastLocked(f dservice.BrowserFrame) {
	for ch := range s.watchers {
		select {
		case ch <- f:
		default:
			if f.State == nil {
				continue // a slow viewer skips JPEGs
			}
			// Retain the new state by making room if the reader hasn't
			// already done so.
			select {
			case <-ch:
			default:
			}
			ch <- f
		}
	}
}

// startCast runs the screencast until castStop.
func (s *Session) startCast() {
	ctx, stop := context.WithCancel(s.ctx)
	s.mu.Lock()
	s.castStop = stop
	s.mu.Unlock()
	frames := chromedp.Events(ctx, page.ScreencastFrame)
	q := int64(castQuality)
	if _, err := chromedp.Call(ctx, page.StartScreencast, page.StartScreencastParams{
		Format: page.StartScreencastFormat("jpeg"), Quality: &q, EveryNthFrame: 1}); err != nil {
		s.log.Warn("browser screencast did not start", "error", err)
		stop()
		return
	}
	go func() {
		defer func() {
			c, cancel := context.WithTimeout(s.ctx, 2*time.Second)
			_, _ = chromedp.Call(c, page.StopScreencast, cdp.Empty{})
			cancel()
		}()
		for ev, err := range frames {
			if err != nil || ctx.Err() != nil {
				return
			}
			_, _ = chromedp.Call(ctx, page.ScreencastFrameAck, page.ScreencastFrameAckParams{SessionID: ev.SessionID})
			s.broadcast(dservice.BrowserFrame{JPEG: ev.Data})
		}
	}()
}

// loginBinding is the CDP binding the login tap reports to.
const loginBinding = "__nusaReportLogin"

// watchLogins hands logins the page submits to the handler. Focus pings
// from the tap carry no login and are dropped.
func (s *Session) watchLogins(handler func() func(dservice.BrowserLoginCapture)) {
	for ev, err := range chromedp.Events(s.ctx, cdpruntime.BindingCalled) {
		if err != nil {
			return
		}
		if ev.Name != loginBinding {
			continue
		}
		var p struct {
			Kind     string `json:"kind"`
			Origin   string `json:"origin"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if json.Unmarshal([]byte(ev.Payload), &p) != nil || p.Kind != "" || p.Password == "" {
			continue
		}
		if fn := handler(); fn != nil {
			fn(dservice.BrowserLoginCapture{ConversationID: s.id, Profile: s.profile, Origin: p.Origin, Username: p.Username, Password: p.Password})
		}
	}
}

// SetLoginOffer shows or clears the save-login question.
func (s *Session) SetLoginOffer(offer *dservice.BrowserLoginOffer) {
	s.mu.Lock()
	s.offer = offer
	s.mu.Unlock()
	s.changed()
}

// fillLoginJS runs on document with the login as arguments, so the
// password never appears in script source.
const fillLoginJS = `function(origin, user, pass, replace) {
  if (location.origin !== origin) return 'other-origin';
  const target = window.__nusaLoginTarget;
  const scope = target && target.isConnected ? (target.form || document) : document;
  const pw = [...scope.querySelectorAll('input[type=password]')].find(i => !i.disabled && !i.readOnly);
  if (!pw) return 'no-field';
  if (!replace && pw.value && pw.value !== pass) return 'no-field';
  const put = (el, v) => { const d = Object.getOwnPropertyDescriptor(el.constructor.prototype, 'value'); d.set.call(el, v); el.dispatchEvent(new Event('input', {bubbles: true})); el.dispatchEvent(new Event('change', {bubbles: true})); };
  const form = pw.form || scope;
  const u = form.querySelector('input[type=email],input[name*=user i],input[name*=email i],input[name*=login i],input[autocomplete*=username i]')
    || [...form.querySelectorAll('input[type=text],input:not([type])')].find(i => !i.disabled && !i.readOnly);
  if (!replace && u && u.value && u.value !== user) return 'no-field';
  if (u && (replace || !u.value)) put(u, user);
  if (replace || !pw.value) put(pw, pass);
  return 'filled';
}`

// FillLogin fills the login form for origin.
func (s *Session) FillLogin(ctx context.Context, origin, username, password string, replace bool) (bool, error) {
	c, cancel := s.call(ctx, callWait)
	defer cancel()
	doc, err := chromedp.Call(c, cdpruntime.Evaluate, cdpruntime.EvaluateParams{Expression: "document"})
	if err != nil {
		return false, s.fail(err)
	}
	if doc.Result == nil || doc.Result.ObjectID == "" {
		return false, errors.New("the page has no document")
	}
	defer func() {
		_, _ = chromedp.Call(c, cdpruntime.ReleaseObject, cdpruntime.ReleaseObjectParams{ObjectID: doc.Result.ObjectID})
	}()
	var args []*cdpruntime.CallArgument
	for _, v := range []any{origin, username, password, replace} {
		raw, _ := json.Marshal(v)
		args = append(args, &cdpruntime.CallArgument{Value: raw})
	}
	res, err := chromedp.Call(c, cdpruntime.CallFunctionOn, cdpruntime.CallFunctionOnParams{
		FunctionDeclaration: fillLoginJS, ObjectID: doc.Result.ObjectID, Arguments: args, ReturnByValue: new(true)})
	if err != nil {
		return false, s.fail(err)
	}
	if res.Result == nil || res.ExceptionDetails != nil {
		return false, errors.New("the login form could not be filled")
	}
	return string(res.Result.Value) == `"filled"`, nil
}

// Record feeds the tab's events into sink until it is done.
func (s *Session) Record(sink dservice.RecordingSink) {
	s.mu.Lock()
	s.rec = sink
	s.mu.Unlock()
	s.startRecordingCapture(sink)
	go func() {
		<-sink.Done()
		s.mu.Lock()
		if s.rec == sink {
			s.rec = nil
		}
		s.mu.Unlock()
	}()
	d := s.Device()
	sink.Append("system", "viewport", map[string]any{"width": d.Width, "height": d.Height, "dpr": d.DPR, "mobile": d.Mobile})
	pg := s.Page()
	sink.Append("browser", "navigate", map[string]any{"url": pg.URL, "title": pg.Title})
}

// note appends to the live recording, if any.
func (s *Session) note(origin, typ string, data any) {
	s.mu.Lock()
	r := s.rec
	s.mu.Unlock()
	if r != nil {
		r.Append(origin, typ, data)
	}
}

// SetRecording shows or clears the live recording.
func (s *Session) SetRecording(rec *dservice.BrowserRecording) {
	s.mu.Lock()
	s.recState = rec
	s.mu.Unlock()
	s.changed()
}
