package enum

// AuthMode decides when the owner must log in to use the server.
type AuthMode string

const (
	// AuthModeAuto requires a login exactly when the server is reachable
	// from beyond this machine.
	AuthModeAuto AuthMode = "auto"
	// AuthModeOn always requires a login.
	AuthModeOn AuthMode = "on"
	// AuthModeOff never asks for a login. It is only allowed while the
	// server is unreachable from beyond this machine.
	AuthModeOff AuthMode = "off"
)

func (m AuthMode) String() string { return string(m) }

func ParseAuthMode(v string) (AuthMode, bool) {
	switch m := AuthMode(v); m {
	case AuthModeAuto, AuthModeOn, AuthModeOff:
		return m, true
	}
	return "", false
}

// LoginRequired reports whether the owner must log in. exposed says the
// server can be reached from beyond this machine. ok is false for the one
// unsafe combination — logins off on an exposed server — which must not
// start: every agent and shell tool would be open to the network.
func (m AuthMode) LoginRequired(exposed bool) (required, ok bool) {
	switch m {
	case AuthModeOn:
		return true, true
	case AuthModeOff:
		return false, !exposed
	}
	return exposed, true
}
