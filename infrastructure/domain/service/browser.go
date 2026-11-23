package service

import (
	"context"
	"encoding/json"
	"io"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// BrowserPage is where a browser session is: its page and viewport.
type BrowserPage struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// BrowserField is one form field for BrowserSession.Fill: a snapshot ref,
// its kind (textbox, checkbox, radio, combobox), and the value to set.
type BrowserField struct {
	Ref   string `json:"ref"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// BrowserDevice is what the tab pretends to be: viewport, pixel ratio, a
// mobile device with touch, and its user agent ("" is the real one).
type BrowserDevice struct {
	// Preset names the device choice: responsive, desktop, mobile, tablet,
	// or custom (a set of fields no preset describes; never selectable).
	Preset    string  `json:"preset"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	DPR       float64 `json:"dpr"`
	Mobile    bool    `json:"mobile"`
	Touch     bool    `json:"touch"`
	UserAgent string  `json:"userAgent,omitempty"`
}

// BrowserLease says who drives the tab's input. The agent always can; a
// viewer can once it took over, unless the agent locked input. A takeover
// ends the agent's lock.
type BrowserLease struct {
	Controller string `json:"controller,omitempty"` // the viewer that took over
	AgentLock  bool   `json:"agentLock"`
}

// BrowserFrame is one live view update: a JPEG of the viewport, or a
// change of page, device, or lease (State set, no image).
type BrowserFrame struct {
	JPEG  []byte
	State *BrowserState
}

// BrowserState is everything a viewer shows around the picture.
type BrowserState struct {
	Page    BrowserPage   `json:"page"`
	Device  BrowserDevice `json:"device"`
	Lease   BrowserLease  `json:"lease"`
	Profile string        `json:"profile"`
	// LoginOffer asks the owner whether to save a login the page just
	// submitted; nil when nothing waits.
	LoginOffer *BrowserLoginOffer `json:"loginOffer,omitempty"`
	// Recording is the live recording; nil when none runs.
	Recording *BrowserRecording `json:"recording,omitempty"`
	// AgentPointer is where the agent last pointed; nil before it has.
	AgentPointer *BrowserPointer `json:"agentPointer,omitempty"`
}

// BrowserPointer is a spot the agent acted on, in viewport CSS pixels.
// Seq grows with every action, so the same spot pointed at twice is
// still news.
type BrowserPointer struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Action string  `json:"action"` // click, hover, scroll, type
	Seq    int64   `json:"seq"`
}

// BrowserRecording is a live recording as viewers see it.
type BrowserRecording struct {
	ID     string `json:"id"`
	Status string `json:"status"` // recording or paused
}

// BrowserLoginOffer is a submitted login waiting for the owner's answer;
// its password stays on the server.
type BrowserLoginOffer struct {
	ID       string `json:"id"`
	Origin   string `json:"origin"`
	Username string `json:"username"`
}

// BrowserLoginCapture is a login a page submitted.
type BrowserLoginCapture struct {
	ConversationID string
	Profile        string
	Origin         string
	Username       string
	Password       string
}

// BrowserLoaded is a page a thread's tab finished loading.
type BrowserLoaded struct {
	ConversationID string
	Profile        string
	URL            string
	Title          string
}

// Browser runs one browser session per thread. Sessions run in named,
// persistent profiles ("default" unless switched), so logins carry over
// between threads of a profile.
type Browser interface {
	// Open returns the thread's session, starting one when none runs.
	Open(ctx context.Context, conversationID string) (BrowserSession, error)
	// SwitchProfile moves the thread's tab to another profile, reopening
	// its page there; it returns the new session.
	SwitchProfile(ctx context.Context, conversationID, profile string) (BrowserSession, error)
	// Profiles lists the profiles that exist, the default one first.
	Profiles() []string
	// OnLogin sets what receives the logins pages submit.
	OnLogin(fn func(BrowserLoginCapture))
	// OnLoad sets what hears of every page a tab finished loading.
	OnLoad(fn func(BrowserLoaded))
	// Session returns the thread's running session.
	Session(conversationID string) (BrowserSession, bool)
	// Close ends the thread's session; a no-op when none runs.
	Close(conversationID string)
	// Shutdown ends every session and the browser.
	Shutdown()
}

// Network tools of a session, carried over from NusaMation.

// RouteRule is one interception rule as accepted from the agent. Fields not
// used by Action are ignored.
type BrowserRouteRule struct {
	// Pattern is a URL glob by default: ** spans '/', * stops at '/', ? is one
	// character. With Regex set it is a Go regular expression.
	Pattern string `json:"pattern"`
	Regex   bool   `json:"regex,omitempty"`
	// Method limits the rule to one HTTP method (case-insensitive).
	Method string `json:"method,omitempty"`
	// ResourceType limits the rule to one CDP resource type, e.g. xhr, fetch,
	// document, script, image, preflight, websocket.
	ResourceType string `json:"resourceType,omitempty"`
	// Action is one of fulfill, abort, delay, continue.
	Action string `json:"action"`
	// Status is the synthetic response status for fulfill (default 200).
	Status int64 `json:"status,omitempty"`
	// Headers are the synthetic response headers for fulfill.
	Headers map[string]string `json:"headers,omitempty"`
	// Body is the synthetic response body for fulfill. A JSON body without an
	// explicit Content-Type is served as application/json.
	Body string `json:"body,omitempty"`
	// BodyBase64 decodes Body as base64 (binary payloads).
	BodyBase64 bool `json:"bodyBase64,omitempty"`
	// DelayMS pauses the request before it continues (action=delay).
	DelayMS int `json:"delayMs,omitempty"`
	// TargetURL rewrites the request URL (action=continue). Absolute and
	// http/https/ws/wss only.
	TargetURL string `json:"targetUrl,omitempty"`
	// ErrorReason fails the request with this network error (action=abort),
	// e.g. failed, aborted, timedout, connectionrefused, internetdisconnected.
	ErrorReason string `json:"errorReason,omitempty"`
	// Times caps how many requests the rule matches; 0 means unlimited.
	Times int `json:"times,omitempty"`
}

// RouteState is a rule as reported back to the agent: the body is replaced by
// its size so a listing cannot flood the tool result.
type BrowserRouteState struct {
	ID           string            `json:"id"`
	Pattern      string            `json:"pattern"`
	Regex        bool              `json:"regex,omitempty"`
	Method       string            `json:"method,omitempty"`
	ResourceType string            `json:"resourceType,omitempty"`
	Action       string            `json:"action"`
	Status       int64             `json:"status,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	BodyBytes    int               `json:"bodyBytes,omitempty"`
	DelayMS      int               `json:"delayMs,omitempty"`
	TargetURL    string            `json:"targetUrl,omitempty"`
	ErrorReason  string            `json:"errorReason,omitempty"`
	Times        int               `json:"times,omitempty"`
	Hits         int64             `json:"hits"`
	Remaining    int               `json:"remaining"` // -1 when unlimited
	LastError    string            `json:"lastError,omitempty"`
}

// WSRouteRule is one WebSocket rewrite rule as accepted from the agent.
type BrowserWSRouteRule struct {
	Pattern string `json:"pattern"`
	Target  string `json:"target"`
}

// WSRouteState is a stored WebSocket rule.
type BrowserWSRouteState struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
	Target  string `json:"target"`
}

// AgentInitScript is one stored init script as reported back to the agent.
type BrowserInitScript struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

// NetEntry is one request in the network log.
type BrowserNetEntry struct {
	Seq    int64  `json:"seq,omitempty"`
	TS     int64  `json:"ts"`
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int64  `json:"status,omitempty"`
	Type   string `json:"type,omitempty"`
	Error  string `json:"error,omitempty"`
}

// BrowserMark asks for one mark on the live page: X/Y is an element's
// point, X/Y/Width/Height a region; Marker is the badge number.
type BrowserMark struct {
	Kind   enum.AnnotationKind
	X, Y   float64
	Width  float64
	Height float64
	Marker int
}

// BrowserMarkPreview is the element under a hover point. Missed means no
// element; Skipped means a mark was in flight and the hover was dropped.
type BrowserMarkPreview struct {
	Missed   bool                        `json:"missed"`
	Skipped  bool                        `json:"skipped,omitempty"`
	Rect     *valueobject.AnnotationRect `json:"rect,omitempty"`
	TagName  string                      `json:"tagName,omitempty"`
	Name     string                      `json:"name,omitempty"`
	Selector string                      `json:"selector,omitempty"`
}

// RecordingSink receives what a recording captures: events, network
// lines, and in-page event batches. Downloads land in DownloadsDir while
// it records; Done closes when the recording ends.
type RecordingSink interface {
	Append(origin, typ string, data any)
	AppendRaw(origin, typ string, ts int64, raw json.RawMessage)
	AppendNet(fields map[string]any)
	AppendDOMBatch(payload []byte)
	DownloadsDir() string
	Done() <-chan struct{}
}

// BrowserSession is one tab. Coordinates are CSS pixels in the viewport.
// Element refs come from the latest Snapshot; a stale ref fails with a hint
// to snapshot again.
type BrowserSession interface {
	Page() BrowserPage
	Profile() string
	Device() BrowserDevice
	// UpdateDevice reads the current device, applies change, and writes the
	// result in one transaction under the device lock.
	UpdateDevice(ctx context.Context, change func(BrowserDevice) (BrowserDevice, error)) error
	// State is the page, device, lease, and profile together.
	State() BrowserState
	Navigate(ctx context.Context, url string) error
	// History moves back (-1) or forward (+1); a missing entry is a no-op.
	History(ctx context.Context, step int) error
	Snapshot(ctx context.Context) (string, error)
	Screenshot(ctx context.Context) ([]byte, error)
	// Annotate marks an element, a drawn region, or the whole page: it
	// reads the page's descriptor of the mark, paints the numbered badge,
	// and returns a full-viewport PNG with the badge on it. One mark at a
	// time; a second one while the first runs is a 409.
	Annotate(ctx context.Context, m BrowserMark) (valueobject.AnnotationDescriptor, []byte, error)
	// PreviewAnnotate is the element under a point, for a hover outline.
	// It never waits for a mark in flight; it reports Skipped instead.
	PreviewAnnotate(ctx context.Context, x, y float64) (BrowserMarkPreview, error)
	// ClearAnnotate removes a painted badge left on the page.
	ClearAnnotate(ctx context.Context)
	// Point is the center of a ref's element, scrolled into view; it fails
	// when the element cannot take a click (hidden, disabled, covered).
	Point(ctx context.Context, ref string) (x, y float64, err error)
	// ShowAgentPointer tells viewers the agent is about to act at x, y.
	ShowAgentPointer(x, y float64, action string)
	// Focus focuses a ref's element; false when focus did not land.
	Focus(ctx context.Context, ref string) (bool, error)
	// Click presses and releases a button (a tap on a touch device).
	Click(ctx context.Context, x, y float64, button string, count int) error
	// Press is one half of a click — down or up — for drags.
	Press(ctx context.Context, x, y float64, button string, count int, down bool) error
	// Move moves the pointer; buttons is the held-buttons mask of a drag.
	Move(ctx context.Context, x, y float64, buttons int) error
	// Touch is one touch phase: touchStart, touchMove, touchEnd, touchCancel.
	Touch(ctx context.Context, phase string, x, y float64) error
	// Type inserts text into the focused element.
	Type(ctx context.Context, text string) error
	// TypeSecret types a secret into a ref's element (or the focused one)
	// through real text input, never through page JavaScript; replace
	// replaces the field's contents.
	TypeSecret(ctx context.Context, ref, text string, replace bool) error
	// Key presses one key by its DOM name (Enter, Tab, ArrowDown, "a");
	// modifiers is the CDP mask (1 Alt, 2 Ctrl, 4 Meta, 8 Shift).
	Key(ctx context.Context, key string, modifiers int) error
	Scroll(ctx context.Context, x, y, dx, dy float64) error
	Select(ctx context.Context, ref string, values []string) (string, error)
	Fill(ctx context.Context, fields []BrowserField) (string, error)
	// WaitText waits until text appears (or, gone, disappears).
	WaitText(ctx context.Context, text string, gone bool) error
	// Eval runs a JavaScript expression, awaiting a promise.
	Eval(ctx context.Context, expression string) (json.RawMessage, error)
	// Console is the page's captured console and errors, newest last.
	Console(ctx context.Context) (json.RawMessage, error)
	// FocusedEditable reports whether the focused element takes text — a
	// touch viewer opens its keyboard then.
	FocusedEditable(ctx context.Context) (bool, error)
	Lease() BrowserLease
	// Takeover gives a viewer the input and ends the agent's lock.
	Takeover(viewer string)
	// Release hands input back when viewer holds it.
	Release(viewer string)
	// SetAgentLock keeps viewers' input out while on.
	SetAgentLock(on bool)
	// SetRecording shows (or, with nil, clears) the live recording.
	SetRecording(rec *BrowserRecording)
	// SetLoginOffer shows (or, with nil, clears) a save-login question.
	SetLoginOffer(offer *BrowserLoginOffer)
	// FillLogin fills the page's login form for origin when the tab is
	// still on it: the username field and the first password field. With
	// replace it overwrites what is there; otherwise it fills only empty or
	// matching fields. It reports whether it filled.
	FillLogin(ctx context.Context, origin, username, password string, replace bool) (bool, error)
	// AddRoute intercepts requests matching a rule; rules match first-wins.
	AddRoute(rule BrowserRouteRule) (BrowserRouteState, error)
	// ClearRoutes removes one rule, or all with "", and reports how many.
	ClearRoutes(id string) int
	Routes() []BrowserRouteState
	// AddWSRoute sends WebSockets the page opens to another endpoint;
	// reload reloads the page so sockets opened at boot are caught.
	AddWSRoute(ctx context.Context, rule BrowserWSRouteRule, reload bool) (BrowserWSRouteState, error)
	ClearWSRoutes(ctx context.Context, reload bool) (int, error)
	WSRoutes() []BrowserWSRouteState
	// AddInitScript runs source before page scripts in every frame; an id
	// replaces that script.
	AddInitScript(ctx context.Context, source, id string, runImmediately bool) (BrowserInitScript, error)
	RemoveInitScript(id string) bool
	InitScripts() []BrowserInitScript
	// NetLog is the requests seen since the tab opened, oldest first.
	NetLog() []BrowserNetEntry
	// Record feeds the tab's events into sink until the sink is done.
	Record(sink RecordingSink)
	// Watch streams live frames until ctx ends or the session closes.
	Watch(ctx context.Context) <-chan BrowserFrame
	// Done is closed when the session ends.
	Done() <-chan struct{}
}

// BrowserInstallMethod is one way this host can get a Chromium: the Chrome
// for Testing download into the data folder (no root), or a package
// manager. ID is what Install takes; Detail is the exact download or
// command, shown before anything runs.
type BrowserInstallMethod struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Detail    string `json:"detail"`
	NeedsRoot bool   `json:"needsRoot,omitempty"`
}

// BrowserInstallOffer says whether a browser executable resolves and, when
// none does, what was searched and how to get one, most preferred first.
type BrowserInstallOffer struct {
	Missing  bool                   `json:"missing"`
	ExecPath string                 `json:"execPath,omitempty"`
	Searched string                 `json:"searched,omitempty"`
	Platform string                 `json:"platform"`
	Proot    bool                   `json:"proot,omitempty"`
	Methods  []BrowserInstallMethod `json:"methods"`
}

// BrowserInstaller gets the browser executable the threads' browser runs.
type BrowserInstaller interface {
	InstallOffer() BrowserInstallOffer
	// Install runs one offered method, writing its output to log, and
	// returns the executable that resolves afterwards. Success is what
	// resolves, not the installer's exit status.
	Install(ctx context.Context, method string, log io.Writer) (string, error)
}
