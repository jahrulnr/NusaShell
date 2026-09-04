// Package launcher decides what a single pet click should do. The policy is
// pure: given whether the Go backend and the Electron wrapper are installed
// or running, it returns an action. Spawning happens through detached
// processes so the wrapper keeps running after the pet exits.
package launcher

import (
	"net/url"
	"os/exec"
	"syscall"
)

// DefaultWebURL is the web frontend fallback when the WebSocket URL cannot
// be converted.
const DefaultWebURL = "http://127.0.0.1:9999/"

// Status is the machine state sampled at click time.
type Status struct {
	GoRunning         bool // NusaShell Go backend accepts connections
	ElectronInstalled bool // Electron wrapper binary resolvable
	ElectronRunning   bool // Electron wrapper process alive
}

// Action is what a single click triggers.
type Action int

const (
	// ActionNone does nothing: neither the Go backend nor the Electron
	// wrapper is running, so the pet must not start either of them.
	ActionNone Action = iota
	// ActionOpenElectron opens or focuses the Electron wrapper. The wrapper
	// holds a single-instance lock: spawning its launcher focuses the
	// already-running window, or starts the app when it is not running.
	ActionOpenElectron
	// ActionOpenWeb opens the web frontend in the default browser, the
	// fallback when Electron is not installed and the backend is up.
	ActionOpenWeb
)

// Choose applies the click policy:
//
//	both not running           -> nothing (never start golang or electron)
//	electron running/installed -> electron first (focus, or start it)
//	electron missing, go up    -> web fallback
//	otherwise                  -> nothing
func Choose(st Status) Action {
	switch {
	case !st.ElectronRunning && !st.GoRunning:
		return ActionNone
	case st.ElectronRunning || st.ElectronInstalled:
		return ActionOpenElectron
	case st.GoRunning:
		return ActionOpenWeb
	default:
		return ActionNone
	}
}

// WebURL converts the pet's WebSocket URL into the matching web frontend
// root (ws -> http, wss -> https). Unparsable URLs fall back to
// DefaultWebURL.
func WebURL(wsURL string) string {
	u, err := url.Parse(wsURL)
	if err != nil || u.Host == "" {
		return DefaultWebURL
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	default:
		u.Scheme = "http"
	}
	u.Path = "/"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// Spawn starts name detached: in its own process group, with no inherited
// stdio, and released from the parent so the pet can exit while the launched
// app keeps running.
func Spawn(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

// OpenWeb opens the web frontend URL with the platform default browser.
func OpenWeb(webURL string) error {
	return Spawn("xdg-open", webURL)
}
