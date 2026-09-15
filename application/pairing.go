package application

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// Pairing challenge TTL and session TTL. The challenge is short-lived and
// single-use; the session lasts for a long period so the remote device does
// not need to re-pair on every reconnect.
const (
	PairingChallengeTTL = 5 * time.Minute
	PairingSessionTTL   = 30 * 24 * time.Hour // 30 days
	PairingCodeLength   = 8                   // short alphanumeric code
)

// Pairing rate-limit windows. Failed exchange attempts are rate-limited per
// source to prevent brute-forcing the short pairing code. Loopback callers
// (empty source) are never rate-limited (they are the local user). Challenge
// creation also keeps a per-source bucket as defense-in-depth, though it is
// unreachable today because create is loopback-only (source is always "").
const (
	PairingRateLimitWindow = 1 * time.Minute
	PairingRateLimitMax    = 5

	PairingExchangeRateLimitWindow = 1 * time.Minute
	PairingExchangeRateLimitMax    = 10

	// pairingLastSeenFlushInterval bounds how often ValidateSession persists
	// last_seen_at, so per-request validation (including WS per-frame and
	// SSE tick re-checks) does not turn into a SQLite write per request.
	pairingLastSeenFlushInterval = 1 * time.Minute
)

// PairingService owns the device-pairing use cases: challenge creation,
// approval, rejection, exchange, session listing, and revocation. It
// enforces the domain state machine and rate limiting. Persistence is
// delegated to PairingStore; time is injected for deterministic tests.
type PairingService struct {
	store PairingStore
	clock func() time.Time

	// rateBuckets tracks challenge creation per source IP within the window.
	// exchangeBuckets tracks failed exchange attempts per source IP. Loopback
	// callers (empty source) are never rate-limited.
	mu              sync.Mutex
	rateBuckets     map[string][]time.Time
	exchangeBuckets map[string][]time.Time

	// exchangeMu serializes challenge exchange so exactly one concurrent
	// request can consume (claim) a challenge. Without it, two concurrent
	// requests could both read an approved challenge and both mark it used.
	exchangeMu sync.Mutex
}

// NewPairingService creates a PairingService backed by the given store.
// If store is nil, the service is a no-op (pairing disabled).
func NewPairingService(store PairingStore) *PairingService {
	return &PairingService{
		store:           store,
		clock:           func() time.Time { return clock.NewTime().Time() },
		rateBuckets:     make(map[string][]time.Time),
		exchangeBuckets: make(map[string][]time.Time),
	}
}

// SetClock injects a time source for deterministic tests.
func (s *PairingService) SetClock(fn func() time.Time) {
	if fn != nil {
		s.clock = fn
	}
}

// now returns the current time from the injected clock.
func (s *PairingService) now() time.Time {
	return s.clock()
}

// hashCode returns the SHA-256 hex hash of a pairing code.
func hashCode(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// hashToken returns the SHA-256 hex hash of a session token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// generateCode produces a short alphanumeric pairing code (uppercase,
// no ambiguous chars: 0/O/1/I/L). Rejection sampling keeps the alphabet
// uniform — a plain b[i]%31 would weight chars 0-7 at 9/256.
func generateCode() (string, error) {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	// 248 = 31*8 is the largest multiple of len(alphabet) below 256.
	const limit = 256 - (256 % len(alphabet))
	out := make([]byte, PairingCodeLength)
	buf := make([]byte, PairingCodeLength*2)
	for i := 0; i < PairingCodeLength; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if i >= PairingCodeLength {
				break
			}
			if int(b) < limit {
				out[i] = alphabet[int(b)%len(alphabet)]
				i++
			}
		}
	}
	return string(out), nil
}

// generateToken produces a high-entropy opaque session token (32 bytes hex).
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// checkRateLimit consumes one challenge-creation slot for the source and
// reports whether it was within the window limit. Loopback sources (empty
// string) are never rate-limited.
func (s *PairingService) checkRateLimit(source string) bool {
	return s.checkBucket(source, s.rateBuckets, PairingRateLimitWindow, PairingRateLimitMax)
}

// checkExchangeRateLimit reports whether the source is below the failed-
// attempt ceiling. It is read-only: the bucket is filled exclusively by
// recordExchangeFailure, so a single failed attempt costs exactly one slot
// and successful exchanges never drain the budget. Loopback sources (empty
// string) are never rate-limited.
func (s *PairingService) checkExchangeRateLimit(source string) bool {
	if source == "" {
		return true
	}
	now := s.now()
	cutoff := now.Add(-PairingExchangeRateLimitWindow)
	s.mu.Lock()
	defer s.mu.Unlock()
	times := s.exchangeBuckets[source]
	pruned := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	s.exchangeBuckets[source] = pruned
	return len(pruned) < PairingExchangeRateLimitMax
}

// recordExchangeFailure records a failed exchange attempt for the source so
// repeated failures eventually block the source. loopback sources are ignored.
func (s *PairingService) recordExchangeFailure(source string) {
	if source == "" {
		return
	}
	now := s.now()
	cutoff := now.Add(-PairingExchangeRateLimitWindow)
	s.mu.Lock()
	defer s.mu.Unlock()
	times := s.exchangeBuckets[source]
	pruned := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	pruned = append(pruned, now)
	s.exchangeBuckets[source] = pruned
}

// checkBucket is the shared sliding-window rate-limit primitive.
func (s *PairingService) checkBucket(source string, buckets map[string][]time.Time, window time.Duration, max int) bool {
	if source == "" {
		return true
	}
	now := s.now()
	cutoff := now.Add(-window)
	s.mu.Lock()
	defer s.mu.Unlock()
	times := buckets[source]
	pruned := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	if len(pruned) >= max {
		buckets[source] = pruned
		return false
	}
	pruned = append(pruned, now)
	buckets[source] = pruned
	return true
}

// CreateChallenge creates a new pending pairing challenge and returns the
// plaintext code (shown once to the user). The source is the remote address
// of the requester; empty means loopback (not rate-limited).
func (s *PairingService) CreateChallenge(source string) (*contracts.PairingChallengeCreateResult, *contracts.RPCError) {
	if s.store == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "pairing not configured"}
	}
	if !s.checkRateLimit(source) {
		return nil, &contracts.RPCError{Code: contracts.CodePairingRateLimit, Message: "Too many pairing attempts. Please wait a minute and try again."}
	}
	code, err := generateCode()
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to generate pairing code"}
	}
	now := s.now()
	ch := domain.NewPairingChallenge(
		domain.NewID(domain.IDPrefixPair),
		hashCode(code),
		PairingChallengeTTL,
		now,
	)
	if err := s.store.SaveChallenge(ch); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to save challenge"}
	}
	return &contracts.PairingChallengeCreateResult{
		ChallengeID: ch.ID,
		Code:        code,
		ExpiresIn:   int(PairingChallengeTTL.Seconds()),
	}, nil
}

// GetChallengeStatus returns the current state of a challenge for polling.
func (s *PairingService) GetChallengeStatus(challengeID string) (*contracts.PairingChallengeStatusResult, *contracts.RPCError) {
	if s.store == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "pairing not configured"}
	}
	ch, err := s.store.GetChallenge(challengeID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodePairingNotFound, Message: "pairing challenge not found"}
	}
	now := s.now()
	state := string(ch.State)
	expiresIn := int(ch.ExpiresAt.Sub(now).Seconds())
	if ch.IsExpired(now) && ch.State == domain.PairingStatePending {
		state = string(domain.PairingStateExpired)
	}
	if expiresIn < 0 {
		expiresIn = 0
	}
	return &contracts.PairingChallengeStatusResult{
		ChallengeID: ch.ID,
		State:       state,
		ExpiresIn:   expiresIn,
	}, nil
}

// ApproveChallenge transitions a challenge to approved. Only callable from
// a loopback or authenticated session (enforced by the transport layer).
func (s *PairingService) ApproveChallenge(challengeID, deviceLabel string) *contracts.RPCError {
	if s.store == nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "pairing not configured"}
	}
	ch, err := s.store.GetChallenge(challengeID)
	if err != nil {
		return &contracts.RPCError{Code: contracts.CodePairingNotFound, Message: "pairing challenge not found"}
	}
	if err := ch.Approve(s.now()); err != nil {
		return pairingStateError(ch, err)
	}
	if deviceLabel != "" {
		ch.DeviceLabel = deviceLabel
	}
	if err := s.store.SaveChallenge(ch); err != nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to save challenge"}
	}
	return nil
}

// RejectChallenge transitions a challenge to rejected.
func (s *PairingService) RejectChallenge(challengeID string) *contracts.RPCError {
	if s.store == nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "pairing not configured"}
	}
	ch, err := s.store.GetChallenge(challengeID)
	if err != nil {
		return &contracts.RPCError{Code: contracts.CodePairingNotFound, Message: "pairing challenge not found"}
	}
	if err := ch.Reject(s.now()); err != nil {
		return pairingStateError(ch, err)
	}
	if err := s.store.SaveChallenge(ch); err != nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to save challenge"}
	}
	return nil
}

// pairingStateError maps a domain transition error to the appropriate wire
// error code, accounting for the challenge's current state.
func pairingStateError(ch *domain.PairingChallenge, err error) *contracts.RPCError {
	if err == domain.ErrPairingChallengeExpired {
		return &contracts.RPCError{Code: contracts.CodePairingExpired, Message: "This pairing code has expired. Please generate a new one."}
	}
	switch ch.State {
	case domain.PairingStateApproved:
		return &contracts.RPCError{Code: contracts.CodePairingUsed, Message: "This pairing code has already been used."}
	case domain.PairingStateRejected:
		return &contracts.RPCError{Code: contracts.CodePairingRejected, Message: "This pairing request was rejected."}
	case domain.PairingStateUsed:
		return &contracts.RPCError{Code: contracts.CodePairingUsed, Message: "This pairing code has already been used."}
	default:
		return &contracts.RPCError{Code: contracts.CodeConflict, Message: err.Error()}
	}
}

// ExchangeChallenge validates the code against an approved challenge and,
// if valid, atomically marks it used and issues a new session token. The
// plaintext token is returned (for the transport layer to set as a cookie);
// it is never serialized into a public JSON response (see contracts).
//
// source is the proxy-aware client IP (empty for loopback). It is used for
// failed-attempt rate limiting: too many recent failures from a source block
// further attempts, preventing brute-force of the short pairing code.
//
// The claim (read → verify → mark used → persist) is serialized by
// exchangeMu so exactly one concurrent request can consume a challenge.
func (s *PairingService) ExchangeChallenge(req contracts.PairingChallengeExchangeRequest, source string) (*contracts.PairingChallengeExchangeResult, *contracts.RPCError) {
	if s.store == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "pairing not configured"}
	}
	if !s.checkExchangeRateLimit(source) {
		return nil, &contracts.RPCError{Code: contracts.CodePairingRateLimit, Message: "Too many pairing attempts. Please wait a minute and try again."}
	}
	// Serialize the claim so two concurrent requests cannot both consume the
	// same approved challenge.
	s.exchangeMu.Lock()
	defer s.exchangeMu.Unlock()
	ch, err := s.store.GetChallenge(req.ChallengeID)
	if err != nil {
		s.recordExchangeFailure(source)
		return nil, &contracts.RPCError{Code: contracts.CodePairingNotFound, Message: "pairing challenge not found"}
	}
	now := s.now()
	if ch.IsExpired(now) {
		s.recordExchangeFailure(source)
		return nil, &contracts.RPCError{Code: contracts.CodePairingExpired, Message: "This pairing code has expired. Please generate a new one."}
	}
	if ch.State == domain.PairingStateRejected {
		s.recordExchangeFailure(source)
		return nil, &contracts.RPCError{Code: contracts.CodePairingRejected, Message: "This pairing request was rejected."}
	}
	if ch.State == domain.PairingStateUsed {
		s.recordExchangeFailure(source)
		return nil, &contracts.RPCError{Code: contracts.CodePairingUsed, Message: "This pairing code has already been used."}
	}
	// Verify the code hash. A mismatch is a brute-force attempt → record it.
	// Constant-time compare so the hash prefix cannot be probed by timing.
	if subtle.ConstantTimeCompare([]byte(hashCode(req.Code)), []byte(ch.CodeHash)) != 1 {
		s.recordExchangeFailure(source)
		return nil, &contracts.RPCError{Code: contracts.CodePairingNotFound, Message: "Invalid pairing code."}
	}
	// Only approved challenges can be exchanged (user must approve first).
	if ch.State != domain.PairingStateApproved {
		return nil, &contracts.RPCError{Code: contracts.CodePairingRequired, Message: "This pairing request is still pending approval. Please approve it on the host device."}
	}
	if err := ch.MarkUsed(now); err != nil {
		return nil, pairingStateError(ch, err)
	}
	if err := s.store.SaveChallenge(ch); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to save challenge"}
	}
	// Issue session token.
	token, err := generateToken()
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to generate session token"}
	}
	label := req.DeviceLabel
	if label == "" {
		label = ch.DeviceLabel
	}
	if label == "" {
		label = "Remote device"
	}
	sess := domain.NewPairingSession(
		domain.NewID(domain.IDPrefixPairSess),
		hashToken(token),
		label,
		PairingSessionTTL,
		now,
	)
	if err := s.store.SaveSession(sess); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to save session"}
	}
	return &contracts.PairingChallengeExchangeResult{
		Token:     token,
		ExpiresAt: sess.ExpiresAt,
	}, nil
}

// ValidateSession checks whether a token hash corresponds to a valid
// (non-expired, non-revoked) session. The last-seen timestamp is updated in
// memory and persisted via the narrow TouchSession write at most once per
// pairingLastSeenFlushInterval — the narrow write can never resurrect a
// concurrently revoked session the way a full-row upsert could. Returns the
// session if valid, nil otherwise.
func (s *PairingService) ValidateSession(tokenHash string) (*domain.PairingSession, bool) {
	if s.store == nil || tokenHash == "" {
		return nil, false
	}
	sess, err := s.store.GetSessionByTokenHash(tokenHash)
	if err != nil || sess == nil {
		return nil, false
	}
	now := s.now()
	if !sess.IsValid(now) {
		return nil, false
	}
	if now.Sub(sess.LastSeenAt) >= pairingLastSeenFlushInterval {
		_ = s.store.TouchSession(sess.ID, now)
	}
	sess.Touch(now)
	return sess, true
}

// ListSessions returns all sessions as DTOs.
func (s *PairingService) ListSessions() (*contracts.PairingSessionsListResult, *contracts.RPCError) {
	if s.store == nil {
		return &contracts.PairingSessionsListResult{Sessions: []contracts.PairingSessionDTO{}}, nil
	}
	sessions, err := s.store.ListSessions()
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to list sessions"}
	}
	dtos := make([]contracts.PairingSessionDTO, 0, len(sessions))
	for _, sess := range sessions {
		dtos = append(dtos, contracts.PairingSessionDTO{
			ID:          sess.ID,
			DeviceLabel: sess.DeviceLabel,
			CreatedAt:   sess.CreatedAt,
			LastSeenAt:  sess.LastSeenAt,
			ExpiresAt:   sess.ExpiresAt,
			Revoked:     sess.Revoked,
		})
	}
	return &contracts.PairingSessionsListResult{Sessions: dtos}, nil
}

// RevokeSession revokes and removes a single session by ID.
func (s *PairingService) RevokeSession(id string) *contracts.RPCError {
	if s.store == nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "pairing not configured"}
	}
	sessions, err := s.store.ListSessions()
	if err != nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to list sessions"}
	}
	found := false
	for _, sess := range sessions {
		if sess.ID == id {
			found = true
			break
		}
	}
	if !found {
		return &contracts.RPCError{Code: contracts.CodePairingNotFound, Message: "session not found"}
	}
	if err := s.store.RevokeSession(id); err != nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to revoke session"}
	}
	return nil
}

// RevokeAllSessions revokes and removes all sessions.
func (s *PairingService) RevokeAllSessions() *contracts.RPCError {
	if s.store == nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "pairing not configured"}
	}
	if err := s.store.RevokeAllSessions(); err != nil {
		return &contracts.RPCError{Code: contracts.CodeInternal, Message: "failed to revoke sessions"}
	}
	return nil
}

// Dispatch routes pairing.* RPC methods. The transport layer calls this for
// protected methods (approve/reject/list/revoke/status); the public
// bootstrap (create/exchange) is handled by the transport pairing handler
// directly because it bypasses auth.
func (s *PairingService) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodPairingChallengeCreate:
		// Create is also available via RPC for the local UI (loopback).
		return s.CreateChallenge("")
	case contracts.MethodPairingChallengeApprove:
		var req struct {
			ChallengeID string `json:"challenge_id"`
			DeviceLabel string `json:"device_label,omitempty"`
		}
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if rpcErr := s.ApproveChallenge(req.ChallengeID, req.DeviceLabel); rpcErr != nil {
			return nil, rpcErr
		}
		return map[string]any{"ok": true}, nil
	case contracts.MethodPairingChallengeReject:
		var req struct {
			ChallengeID string `json:"challenge_id"`
		}
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if rpcErr := s.RejectChallenge(req.ChallengeID); rpcErr != nil {
			return nil, rpcErr
		}
		return map[string]any{"ok": true}, nil
	case contracts.MethodPairingChallengeExchange:
		var req contracts.PairingChallengeExchangeRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		// Exchange via RPC is local-only (enforced by the transport layer),
		// so the source is loopback and not rate-limited.
		return s.ExchangeChallenge(req, "")
	case contracts.MethodPairingSessionsList:
		return s.ListSessions()
	case contracts.MethodPairingSessionsRevoke:
		var req struct {
			ID string `json:"id"`
		}
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if rpcErr := s.RevokeSession(req.ID); rpcErr != nil {
			return nil, rpcErr
		}
		return map[string]any{"ok": true}, nil
	case contracts.MethodPairingSessionsRevokeAll:
		if rpcErr := s.RevokeAllSessions(); rpcErr != nil {
			return nil, rpcErr
		}
		return map[string]any{"ok": true}, nil
	case contracts.MethodPairingStatus:
		var req struct {
			ChallengeID string `json:"challenge_id"`
		}
		_ = contracts.DecodePayload(payload, &req)
		if req.ChallengeID == "" {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "challenge_id required"}
		}
		return s.GetChallengeStatus(req.ChallengeID)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: fmt.Sprintf("unknown pairing method: %s", method)}
	}
}
