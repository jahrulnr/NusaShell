package transport

import (
	"encoding/json"
	"net/http"

	"nusashell/contracts"
)

// registerPairingRoutes adds the public pairing bootstrap routes to the mux.
// These routes bypass the auth middleware because they are the bootstrap path
// for unauthenticated remote devices to obtain a session.
//
// The flow is host-driven: the host (loopback) creates a challenge via the
// local-only pairing.challenge.create RPC and displays a QR/link carrying the
// challenge id + one-time code. The remote device opens that link, polls
// status, and exchanges after the host approves.
//
// Routes:
//
//	GET  /pairing/status   — poll challenge status (public, minimal state)
//	POST /pairing/exchange — exchange an approved challenge code for a session cookie
//
// Challenge creation is intentionally NOT a public route: only the host
// (loopback) may create challenges via the protected RPC path.
func (s *Server) registerPairingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /pairing/status", s.handlePairingStatus)
	mux.HandleFunc("POST /pairing/exchange", s.handlePairingExchange)
}

// clientSource returns the proxy-aware client IP for rate-limiting public
// pairing routes. It returns "" for loopback callers (never rate-limited).
func clientSource(r *http.Request) string {
	if IsLoopbackRequest(r) {
		return ""
	}
	return ClientIP(r)
}

// handlePairingStatus polls the status of a pairing challenge. This is
// public so the remote device can poll without auth. Only minimal state is
// revealed (state + expiry); the code, token, and device label are never
// returned here.
func (s *Server) handlePairingStatus(w http.ResponseWriter, r *http.Request) {
	if s.Pairing == nil {
		writeRemoteAccessDisabled(w)
		return
	}
	challengeID := r.URL.Query().Get("challenge_id")
	if challengeID == "" {
		writeJSON(w, http.StatusBadRequest, contracts.ErrResult(contracts.CodeValidation, "challenge_id required"))
		return
	}
	result, rpcErr := s.Pairing.GetChallengeStatus(challengeID)
	if rpcErr != nil {
		writeJSON(w, http.StatusOK, contracts.ErrResult(rpcErr.Code, rpcErr.Message))
		return
	}
	writeJSON(w, http.StatusOK, contracts.OKResult(result))
}

// handlePairingExchange exchanges an approved challenge code for a session
// cookie. The token is set as an HttpOnly cookie and is never serialized into
// the JSON response body, so it is not reachable from browser JavaScript.
// Failed attempts are rate-limited per source IP.
func (s *Server) handlePairingExchange(w http.ResponseWriter, r *http.Request) {
	// The route is public by design, but a browser request from another origin
	// is refused: the response plants a session cookie, so a third-party page
	// must not be able to trigger the exchange on a victim's behalf.
	if !isSameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, contracts.ErrResult(contracts.CodePairingUnauthorized, "cross-origin request denied"))
		return
	}
	if s.Pairing == nil {
		writeRemoteAccessDisabled(w)
		return
	}
	var req contracts.PairingChallengeExchangeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, contracts.ErrResult(contracts.CodeValidation, "malformed request body"))
		return
	}
	if req.ChallengeID == "" || req.Code == "" {
		writeJSON(w, http.StatusBadRequest, contracts.ErrResult(contracts.CodeValidation, "challenge_id and code are required"))
		return
	}
	source := clientSource(r)
	result, rpcErr := s.Pairing.ExchangeChallenge(req, source)
	if rpcErr != nil {
		writeJSON(w, http.StatusOK, contracts.ErrResult(rpcErr.Code, rpcErr.Message))
		return
	}
	// Set the session cookie for browser clients. The token is omitted from
	// the JSON body (contracts.PairingChallengeExchangeResult.Token is
	// json:"-"), so it stays internal to this transport boundary.
	SetSessionCookie(w, r, result.Token, result.ExpiresAt)
	writeJSON(w, http.StatusOK, contracts.OKResult(result))
}
