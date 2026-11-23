// Package browser drives a local Chrome or Chromium over the DevTools
// protocol (chromedp): one headless browser with a persistent profile, and
// one tab per thread. It implements service.Browser.
package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Default viewport of a new tab.
const (
	defaultWidth  = 1280
	defaultHeight = 800
	launchWait    = 20 * time.Second
	// openWait bounds the tab-opening calls after the browser is up —
	// target creation, protocol setup, and the device update. A cold
	// launch is bounded by launchWait inside launch; a warm open pays
	// only this.
	openWait = 15 * time.Second
)

// Options configures the browser.
type Options struct {
	// ExecPath is the browser binary; empty finds Chrome or Chromium.
	ExecPath string
	// InstallDir is where the Chrome for Testing download unpacks; ""
	// offers no download.
	InstallDir string
	// ProfileDir holds one folder per profile, each keeping its cookies,
	// storage, and history across restarts.
	ProfileDir string
	// Idle closes a session no tool or viewer used for this long; 0 keeps
	// sessions until their thread or the server ends.
	Idle time.Duration
	Log  *slog.Logger
}

// Manager owns the browser process and the sessions.
type Manager struct {
	opts Options
	log  *slog.Logger

	launchMu sync.Mutex // one launch or tab opening at a time
	mu       sync.Mutex
	browsers map[string]*proc // by profile, launched on first use
	sessions map[string]*Session
	onLogin  func(dservice.BrowserLoginCapture)
	onLoad   func(dservice.BrowserLoaded)
	closed   bool
	quit     chan struct{}
}

// proc is one browser process, serving one profile.
type proc struct {
	ctx  context.Context
	stop context.CancelFunc
}

// DefaultProfile is the profile a new session opens in.
const DefaultProfile = "default"

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

var (
	_ dservice.Browser          = (*Manager)(nil)
	_ dservice.BrowserInstaller = (*Manager)(nil)
)

// New returns a manager; the browser starts on the first Open.
func New(opts Options) *Manager {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	m := &Manager{opts: opts, log: opts.Log.With("component", "browser"), browsers: map[string]*proc{}, sessions: map[string]*Session{}, quit: make(chan struct{})}
	if opts.Idle > 0 {
		go m.reap()
	}
	return m
}

// Session returns the thread's running session.
func (m *Manager) Session(conversationID string) (dservice.BrowserSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[conversationID]
	if !ok || s.ended() {
		return nil, false
	}
	return s, true
}

// Open returns the thread's session, starting the browser and a tab when
// needed. A warm browser never touches the launch lock — a second call
// while the first launches waits out only the launch, nothing more.
func (m *Manager) Open(ctx context.Context, conversationID string) (dservice.BrowserSession, error) {
	if s, ok := m.Session(conversationID); ok {
		return s, nil
	}
	return m.openTab(ctx, conversationID, DefaultProfile, nil, dservice.BrowserDevice{Preset: "responsive", Width: defaultWidth, Height: defaultHeight, DPR: 1})
}

// SwitchProfile reopens the thread's tab in another profile, at the same
// page and device. The old session ends.
func (m *Manager) SwitchProfile(ctx context.Context, conversationID, profile string) (dservice.BrowserSession, error) {
	if profile == "" {
		profile = DefaultProfile
	}
	if !profileName.MatchString(profile) {
		return nil, apperror.New(400, "a profile name is 1-32 lowercase letters, digits, - or _", nil)
	}
	url, device := "", dservice.BrowserDevice{Preset: "responsive", Width: defaultWidth, Height: defaultHeight, DPR: 1}
	m.mu.Lock()
	old := m.sessions[conversationID]
	m.mu.Unlock()
	if old != nil && !old.ended() {
		if old.Profile() == profile {
			return old, nil
		}
		url, device = old.Page().URL, old.Device()
	}
	s, err := m.openTab(ctx, conversationID, profile, old, device)
	if err != nil {
		return nil, err
	}
	if old != nil {
		s.inherit(old)
		old.close()
	}
	if url != "" && url != "about:blank" {
		_ = s.Navigate(ctx, url)
	}
	return s, nil
}

// OnLogin sets what receives submitted logins.
func (m *Manager) OnLogin(fn func(dservice.BrowserLoginCapture)) {
	m.mu.Lock()
	m.onLogin = fn
	m.mu.Unlock()
}

func (m *Manager) loginHandler() func(dservice.BrowserLoginCapture) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onLogin
}

// OnLoad sets what hears of the pages tabs finish loading.
func (m *Manager) OnLoad(fn func(dservice.BrowserLoaded)) {
	m.mu.Lock()
	m.onLoad = fn
	m.mu.Unlock()
}

func (m *Manager) loadHandler() func(dservice.BrowserLoaded) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onLoad
}

// Profiles lists the profiles a browser has run in — folders holding
// Chrome's "Local State" — always with the default one.
func (m *Manager) Profiles() []string {
	out := []string{DefaultProfile}
	entries, _ := os.ReadDir(m.opts.ProfileDir)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == DefaultProfile || !profileName.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.opts.ProfileDir, e.Name(), "Local State")); err == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// openTab opens a tab for the thread in profile at about:blank; callers
// navigate afterwards if they need a page. replace names the session
// this tab substitutes on a profile switch — it loses the register
// check to nobody. Every DevTools call runs under the caller's deadline
// capped at openWait — an abandoned Open closes the half-built target
// instead of leaving it orphaned.
func (m *Manager) openTab(ctx context.Context, conversationID, profile string, replace *Session, device dservice.BrowserDevice) (*Session, error) {
	browserCtx, err := m.launch(ctx, profile)
	if err != nil {
		return nil, err
	}
	// The opening calls borrow bounded children of the browser and tab
	// contexts — a dead caller cancels them instead of leaving a
	// half-built target running.
	callCtx, callCancel := context.WithTimeout(browserCtx, openWait)
	stop := context.AfterFunc(ctx, callCancel)
	defer stop()
	defer callCancel()
	created, err := chromedp.CallBrowser(callCtx, target.CreateTarget, target.CreateTargetParams{URL: "about:blank", NewWindow: new(true)})
	if err != nil {
		return nil, apperror.New(503, "the browser could not open a tab", err)
	}
	// The first call on a target context attaches it — and the event
	// pump that call starts borrows the CALL's context. Attach on the
	// long-lived tabCtx and bound only the wait: a bounded attach ctx
	// would kill the tab's pump when it expired.
	tabCtx, tabCancel := chromedp.NewContext(browserCtx, chromedp.WithTargetID(created.TargetID))
	attachDone := make(chan error, 1)
	go func() {
		_, err := chromedp.Call(tabCtx, page.Enable, page.EnableParams{})
		attachDone <- err
	}()
	timer := time.NewTimer(openWait)
	select {
	case err := <-attachDone:
		timer.Stop()
		if err != nil {
			tabCancel()
			return nil, apperror.New(503, "the browser tab did not start", err)
		}
	case <-ctx.Done():
		timer.Stop()
		tabCancel()
		return nil, apperror.New(503, "the browser tab did not start", ctx.Err())
	case <-timer.C:
		tabCancel()
		return nil, apperror.New(503, "the browser tab did not start", errors.New("timed out"))
	}
	// The target is attached now, so the setup and device calls can run
	// under a bounded child honoring the caller's deadline.
	setupCtx, setupCancel := context.WithTimeout(tabCtx, openWait)
	stopSetup := context.AfterFunc(ctx, setupCancel)
	defer stopSetup()
	defer setupCancel()
	hooks := consoleTapJS + ";\n" + selectShimJS + ";\n" + loginTapJS + ";\n" + recordingTapJS
	setup := func() error {
		if _, err := chromedp.Call(setupCtx, network.Enable, network.EnableParams{}); err != nil {
			return err
		}
		for _, name := range []string{loginBinding, "__nusaRecPush"} {
			if _, err := chromedp.Call(setupCtx, cdpruntime.AddBinding, cdpruntime.AddBindingParams{Name: name}); err != nil {
				return err
			}
		}
		if _, err := chromedp.Call(setupCtx, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: hooks}); err != nil {
			return err
		}
		_, err := chromedp.Call(setupCtx, cdpruntime.Evaluate, cdpruntime.EvaluateParams{Expression: hooks})
		return err
	}
	if err := setup(); err != nil {
		tabCancel()
		return nil, apperror.New(503, "the browser tab did not start", err)
	}
	s := newSession(conversationID, profile, tabCtx, tabCancel, m.log, m.loadHandler)
	go s.watchLogins(m.loginHandler)
	if err := s.UpdateDevice(setupCtx, func(dservice.BrowserDevice) (dservice.BrowserDevice, error) { return device, nil }); err != nil {
		tabCancel()
		return nil, apperror.New(503, "the browser tab did not start", err)
	}
	m.mu.Lock()
	// A session that appeared while this tab was being built wins —
	// register last and close the duplicate. The session being replaced
	// is not a race: the switch explicitly substitutes it.
	if existing := m.sessions[conversationID]; existing != nil && existing != replace && !existing.ended() {
		m.mu.Unlock()
		s.close()
		return existing, nil
	}
	m.sessions[conversationID] = s
	m.mu.Unlock()
	go func() {
		<-s.Done()
		m.mu.Lock()
		if m.sessions[conversationID] == s {
			delete(m.sessions, conversationID)
		}
		m.mu.Unlock()
	}()
	return s, nil
}

// Close ends the thread's session.
func (m *Manager) Close(conversationID string) {
	m.mu.Lock()
	s := m.sessions[conversationID]
	delete(m.sessions, conversationID)
	m.mu.Unlock()
	if s != nil {
		s.close()
	}
}

// Shutdown ends every session and the browser.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.quit)
	sessions := m.sessions
	m.sessions = map[string]*Session{}
	browsers := m.browsers
	m.browsers = map[string]*proc{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
	for _, p := range browsers {
		p.stop()
	}
}

// Preheat warms the default profile's browser so a thread's first
// browser call does not pay the cold start inside its own deadline.
// It launches in the background under the server context and logs the
// outcome through launch.
func (m *Manager) Preheat(ctx context.Context) {
	go func() {
		ctx, cancel := context.WithTimeout(ctx, launchWait)
		defer cancel()
		_, _ = m.launch(ctx, DefaultProfile)
	}()
}

// launch returns the profile's running browser, starting it when it is
// not up. The warm path never touches launchMu — only an actual launch
// is serialized, and it bounds itself by the caller's deadline capped at
// launchWait. Before exec, the profile's leftover Chrome from a previous
// run is reaped so the singleton lock can't stall the fresh launch.
func (m *Manager) launch(ctx context.Context, profile string) (context.Context, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, apperror.New(503, "the server is shutting down", nil)
	}
	if p := m.browsers[profile]; p != nil && p.ctx.Err() == nil {
		m.mu.Unlock()
		return p.ctx, nil
	}
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.launchMu.Lock()
	defer m.launchMu.Unlock()
	// Recheck under the launch lock — a parallel open may have finished
	// the launch while this one waited on it.
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, apperror.New(503, "the server is shutting down", nil)
	}
	if p := m.browsers[profile]; p != nil && p.ctx.Err() == nil {
		m.mu.Unlock()
		return p.ctx, nil
	}
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	path, _, err := resolveExec(m.opts.ExecPath, m.opts.InstallDir)
	if err != nil {
		return nil, err
	}
	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(path),
		chromedp.Headless,
		chromedp.WindowSize(defaultWidth, defaultHeight),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
		// The one tab a viewer watches must not be throttled as a
		// background tab.
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	}
	// Chrome refuses to sandbox as root, and cannot build its sandbox
	// under proot.
	if runtime.GOOS == "linux" && (os.Geteuid() == 0 || prootLike()) {
		opts = append(opts, chromedp.NoSandbox)
	}
	if m.opts.ProfileDir != "" {
		dir := filepath.Join(m.opts.ProfileDir, profile)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, apperror.New(503, "the browser profile folder could not be created", err)
		}
		// Chrome refuses its own profile while a stale or orphaned
		// instance's lock file stands — clear ours before the exec.
		m.reapOrphan(dir, profile)
		opts = append(opts, chromedp.UserDataDir(dir))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	stop := func() { browserCancel(); allocCancel() }
	// The first call starts the process and binds it to browserCtx; the
	// deadline abandons only the wait — the caller's too, when it ends
	// first. A failed start kills the process it left half-up.
	start := time.Now()
	errc := make(chan error, 1)
	go func() {
		_, err := chromedp.CallBrowser(browserCtx, browser.GetVersion, cdp.Empty{})
		errc <- err
	}()
	launchCtx, launchCancel := context.WithTimeout(ctx, launchWait)
	defer launchCancel()
	select {
	case err = <-errc:
	case <-launchCtx.Done():
		err = launchCtx.Err()
	}
	if err != nil {
		stop()
		m.log.Warn("browser launch failed", "exec", path, "profile", profile,
			"took", time.Since(start).Round(time.Millisecond), "err", err)
		msg := fmt.Sprintf("the browser %s did not start: %v", path, err)
		if hint := profileDirHint(m.opts.ProfileDir); hint != "" {
			msg += "; " + hint
		}
		return nil, apperror.New(503, msg, err)
	}
	m.log.Info("browser started", "exec", path, "profile", profile,
		"took", time.Since(start).Round(time.Millisecond))
	m.mu.Lock()
	m.browsers[profile] = &proc{ctx: browserCtx, stop: stop}
	m.mu.Unlock()
	return browserCtx, nil
}

// reap closes sessions idle past opts.Idle.
func (m *Manager) reap() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-m.quit:
			return
		case <-t.C:
		}
		m.mu.Lock()
		var idle []*Session
		for id, s := range m.sessions {
			if s.idleFor(m.opts.Idle) {
				idle = append(idle, s)
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
		for _, s := range idle {
			m.log.Info("closing an idle browser session", "conversation", s.id)
			s.close()
		}
	}
}

// resolveExec is the configured browser, else the first Chrome or Chromium
// on PATH, in a standard install location, or downloaded into installDir.
// searched names where it looked, for an offer to show.
func resolveExec(configured, installDir string) (path, searched string, err error) {
	if configured != "" {
		if p, err := lookPath(configured); err == nil {
			return p, "", nil
		}
		return "", "browser.exec " + configured, apperror.New(503, "browser.exec "+configured+" was not found", nil)
	}
	names := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "microsoft-edge", "msedge"}
	for _, name := range names {
		if p, err := lookPath(name); err == nil {
			return p, "", nil
		}
	}
	searched = "PATH (" + strings.Join(names, ", ") + ")"
	var known []string
	switch runtime.GOOS {
	case "darwin":
		known = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if base := os.Getenv(env); base != "" {
				known = append(known,
					filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
					filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`))
			}
		}
	}
	if len(known) > 0 {
		searched += ", standard install locations"
	}
	for _, p := range known {
		if isExecutableFile(p) {
			return p, "", nil
		}
	}
	if installDir != "" {
		searched += ", " + installDir
		matches, _ := filepath.Glob(filepath.Join(installDir, "chrome-headless-shell-*", "chrome-headless-shell*"))
		for _, p := range matches {
			if isExecutableFile(p) {
				return p, "", nil
			}
		}
	}
	return "", searched, apperror.New(503, "no Chrome or Chromium found: install one from Settings → Tools or with `nusashell browser install`, or set browser.exec", nil)
}

// profileDirHint explains a launch failure the profile folder caused.
//
// Chrome keeps its process-singleton lock as a symlink, and a filesystem
// that refuses symlinks (an NTFS mount without the `symlinks` option, for
// one) makes Chrome abort with "Failed to create …/SingletonLock: No such
// file or directory" — which reads like a missing folder even though the
// folder is right there. Probing tells the two apart, and the fix is a
// setting rather than a mystery.
func profileDirHint(profileDir string) string {
	if profileDir == "" || canHoldSymlink(profileDir) {
		return ""
	}
	return fmt.Sprintf("the profile folder %s cannot hold a symlink, which this browser needs for its lock file; "+
		"point browser.dir at a folder on a filesystem that supports them", profileDir)
}

// canHoldSymlink reports whether the existing folder dir accepts the
// symlink Chrome's process-singleton lock is made of.
func canHoldSymlink(dir string) bool {
	probe := filepath.Join(dir, ".symlink-probe")
	_ = os.Remove(probe)
	if err := os.Symlink("target", probe); err != nil {
		return false
	}
	_ = os.Remove(probe)
	return true
}

// ProfileRoot is where profile folders should live when the owner chose no
// place: preferred, unless its filesystem refuses the symlink Chrome's lock
// needs (a data folder on an NTFS mount without the `symlinks` option, for
// one), when it is a folder in the user's config directory that accepts it.
// moved says the answer is not preferred. Where no such folder exists the
// answer stays preferred, and a launch fails with profileDirHint's advice.
// Windows has no symlink lock, so it always keeps preferred.
func ProfileRoot(preferred string) (dir string, moved bool) {
	if runtime.GOOS == "windows" {
		return preferred, false
	}
	if os.MkdirAll(preferred, 0o700) == nil && canHoldSymlink(preferred) {
		return preferred, false
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return preferred, false
	}
	alt := filepath.Join(config, "nusashell", "browser")
	if os.MkdirAll(alt, 0o700) != nil || !canHoldSymlink(alt) {
		return preferred, false
	}
	return alt, true
}
