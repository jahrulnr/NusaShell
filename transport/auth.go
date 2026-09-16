package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// pairingManagementMethods are the pairing.* RPC methods that administer
// pairing and device sessions. They are local-only: only loopback callers may
// invoke them. A paired remote device gets the normal NusaShell API, not
// device-management authority. Exchange runs on the public /pairing/exchange
// route, not via RPC, so the RPC method is denied to non-loopback callers
// here to prevent a paired remote session from exchanging via RPC.
var pairingManagementMethods = map[string]bool{
	contracts.MethodPairingChallengeCreate:   true,
	contracts.MethodPairingChallengeApprove:  true,
	contracts.MethodPairingChallengeReject:   true,
	contracts.MethodPairingChallengeExchange: true,
	contracts.MethodPairingStatus:            true,
	contracts.MethodPairingSessionsList:      true,
	contracts.MethodPairingSessionsRevoke:    true,
	contracts.MethodPairingSessionsRevokeAll: true,
}

// IsPairingManagementMethod reports whether the RPC method administers
// pairing and must be restricted to loopback callers.
func IsPairingManagementMethod(method string) bool {
	return pairingManagementMethods[method]
}

// SessionCookieName is the HttpOnly cookie carrying the opaque session token.
const SessionCookieName = "nusashell_session"

// protectedPrefixes are the route prefixes that require authentication when
// the request is not from loopback. Everything else (static assets, /healthz,
// /pairing/*, /sounds/*) is public so the pairing page can load.
var protectedPrefixes = []string{
	"/rpc/",
	"/ws",
	"/stream",
	"/local-file",
	"/plugins",
}

// isProtectedPath reports whether the request path requires a paired session.
func isProtectedPath(path string) bool {
	for _, p := range protectedPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// immediatePeerIP returns the IP of the immediate TCP peer (from RemoteAddr),
// ignoring any forwarded headers.
func immediatePeerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// immediatePeerLoopback reports whether the immediate TCP peer is a loopback
// address. Forwarded headers are never consulted here; this is the trust
// boundary for honoring X-Forwarded-For / X-Forwarded-Proto.
func immediatePeerLoopback(r *http.Request) bool {
	ip := net.ParseIP(immediatePeerIP(r))
	return ip != nil && ip.IsLoopback()
}

// ClientIP derives the effective client IP using a safe, explicit
// trusted-proxy rule:
//
//   - If the immediate peer is NOT loopback, the peer address is the client.
//     Forwarded headers from a public peer are never trusted.
//   - If the immediate peer IS loopback (a trusted reverse proxy on the same
//     host) and X-Forwarded-For is present, the rightmost non-empty entry is
//     treated as the client: it is the address the trusted proxy appended
//     for its own TCP peer. Earlier entries are client-supplied and
//     trivially spoofable, so they are never trusted.
//   - If the immediate peer is loopback with no X-Forwarded-For, the client is
//     the loopback peer itself (direct local access).
//
// This preserves direct loopback no-auth while preventing an external client
// behind a loopback reverse proxy from impersonating a loopback caller. The
// trusted proxy MUST append or overwrite X-Forwarded-For (the nginx
// $proxy_add_x_forwarded_for / Caddy default behavior); a proxy that passes
// the client-supplied header through verbatim breaks this rule and must not
// be used.
func ClientIP(r *http.Request) string {
	peer := immediatePeerIP(r)
	if !immediatePeerLoopback(r) {
		return peer
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if entry := strings.TrimSpace(parts[i]); entry != "" {
				return entry
			}
		}
	}
	return peer
}

// IsLoopbackRequest reports whether the request is a genuine direct local
// call: the effective client (after the proxy-aware derivation in ClientIP)
// must be a loopback address AND the request Host must name a loopback
// authority.
//
// Both halves are required. A host-local tunnel or proxy that dials the core
// over loopback but forwards a public Host and injects no X-Forwarded-For would
// otherwise be indistinguishable from a local caller, which would hand every
// remote client the loopback no-auth bypass. Requiring the local Host keeps the
// bypass for direct access (a browser at http://127.0.0.1:PORT, the Electron
// wrapper, an `ssh -L` forward to a local port) and treats a forwarded public
// Host as remote, where pairing applies.
func IsLoopbackRequest(r *http.Request) bool {
	if !isLocalHost(r.Host) {
		return false
	}
	ip := net.ParseIP(ClientIP(r))
	return ip != nil && ip.IsLoopback()
}

// isLocalHost reports whether a request Host names a loopback authority
// (localhost, 127.0.0.0/8, ::1), with or without a port. Everything else — a
// public hostname, a LAN address, the wildcard address, or an empty Host — is
// not local. The check is deliberately fail-closed: an unrecognized Host is
// treated as remote.
func isLocalHost(host string) bool {
	if host == "" {
		return false
	}
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.TrimSuffix(strings.Trim(name, "[]"), ".")
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// isSameOriginRequest reports whether a browser-supplied Origin header matches
// the request host. Requests without an Origin (non-browser clients such as
// curl, MCP bridges, and internal calls) are allowed; a malformed, opaque
// ("null"), or foreign Origin is not.
//
// The comparison is host-only on purpose. The host is what a cross-site page
// cannot forge, and it is where the attack lives: a page on another origin must
// never be able to drive side effects. The scheme is not compared because
// behind a TLS-terminating proxy it is derived from X-Forwarded-Proto, which is
// only trusted for loopback peers; requiring a scheme match here would reject
// legitimate requests through a trusted non-loopback proxy.
func isSameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	return u.Host == r.Host
}

// requireRemoteSession reports whether a non-loopback caller may proceed,
// writing the matching rejection when it may not: remote access disabled when
// no pairing service exists, pairing required when no valid session is
// presented. Handlers that must hold even when the mux is exercised directly
// (WebSocket upgrades, local file reads) call this instead of repeating the two
// rejection paths.
func (s *Server) requireRemoteSession(w http.ResponseWriter, r *http.Request) bool {
	if IsLoopbackRequest(r) {
		return true
	}
	if s.Pairing == nil {
		writeRemoteAccessDisabled(w)
		return false
	}
	if !s.HasValidSession(r) {
		writePairingRequired(w)
		return false
	}
	return true
}

// hashToken returns the SHA-256 hex hash of a session token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// SessionValidator is the interface the auth middleware uses to validate
// a session token hash. Implemented by *application.PairingService.
type SessionValidator interface {
	ValidateSession(tokenHash string) (*domain.PairingSession, bool)
}

// PairingHandler is the full pairing service surface needed by the transport
// layer: session validation for the auth middleware + challenge lifecycle
// for the public pairing routes. Implemented by *application.PairingService.
type PairingHandler interface {
	SessionValidator
	CreateChallenge(source string) (*contracts.PairingChallengeCreateResult, *contracts.RPCError)
	GetChallengeStatus(challengeID string) (*contracts.PairingChallengeStatusResult, *contracts.RPCError)
	ExchangeChallenge(req contracts.PairingChallengeExchangeRequest, source string) (*contracts.PairingChallengeExchangeResult, *contracts.RPCError)
}

// IsRemoteAccessSettingsChange reports whether an RPC attempts to mutate the
// host-owned remote-access gate. Paired remote sessions may use normal agent
// settings, but they cannot enable/disable the listener policy or rewrite the
// addresses used for host-generated pairing links.
func IsRemoteAccessSettingsChange(method string, payload json.RawMessage) bool {
	if method != contracts.MethodSettingsSet {
		return false
	}
	var req contracts.SettingsSetRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return false
	}
	return req.RemoteAccessEnabled != nil || req.RemoteAccessAddresses != nil
}

// AuthMiddleware wraps the mux with pairing-based authentication. Loopback
// requests bypass auth entirely. Non-loopback requests to protected paths
// require a valid session cookie. Public paths (static assets, /healthz,
// /pairing/*) are always allowed.
func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Loopback always bypasses auth.
		if IsLoopbackRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		// Public paths don't require auth.
		if !isProtectedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Non-loopback protected path: require valid session.
		if s.Pairing == nil {
			writeRemoteAccessDisabled(w)
			return
		}
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil || cookie.Value == "" {
			writePairingRequired(w)
			return
		}
		sess, ok := s.Pairing.ValidateSession(hashToken(cookie.Value))
		if !ok || sess == nil {
			writePairingRequired(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writePairingRequired sends a 401 JSON response that the frontend can
// detect to show the pairing gate. The body uses the PAIRING_REQUIRED
// error code so the frontend rpc.js can map it to the pairing UI.
func writePairingRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(contracts.Response{
		OK:    false,
		Error: &contracts.RPCError{Code: contracts.CodePairingRequired, Message: "Device pairing required. Pair this device from the host NusaShell instance."},
	})
}

// writeRemoteAccessDisabled explains the safe default to a non-loopback
// caller without presenting a pairing flow that cannot succeed.
func writeRemoteAccessDisabled(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(contracts.Response{
		OK: false,
		Error: &contracts.RPCError{
			Code:    contracts.CodeRemoteAccessDisabled,
			Message: "Remote access is disabled. Enable it from the host NusaShell Settings.",
		},
	})
}

// isHTTPS reports whether the request is over HTTPS, either directly (TLS) or
// behind a trusted loopback TLS-terminating proxy that set X-Forwarded-Proto.
// X-Forwarded-Proto is honored only when the immediate peer is loopback; it is
// never trusted from a public peer.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if immediatePeerLoopback(r) && r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return false
}

// SetSessionCookie sets the HttpOnly session cookie on the response. Secure
// is set when the request is over HTTPS (directly or via a trusted loopback
// proxy), so local development over plain HTTP is not broken while remote
// sessions over HTTPS get a Secure cookie. MaxAge is derived from the
// session's expiry so the cookie lifetime cannot silently drift from
// PairingSessionTTL.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if maxAge := int(time.Until(expiresAt).Seconds()); maxAge > 0 {
		cookie.MaxAge = maxAge
	}
	if isHTTPS(r) {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
	if isHTTPS(r) {
		cookie.Secure = true
	}
	http.SetCookie(w, cookie)
}

// HasValidSession checks whether the current request has a valid paired
// session. Used by the WS and SSE handlers to gate upgrades.
func (s *Server) HasValidSession(r *http.Request) bool {
	if s.Pairing == nil {
		return false
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	sess, ok := s.Pairing.ValidateSession(hashToken(cookie.Value))
	return ok && sess != nil
}
