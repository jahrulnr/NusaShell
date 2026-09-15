package domain

import (
	"errors"
	"time"
)

// PairingState is the lifecycle state of a pairing challenge.
type PairingState string

const (
	PairingStatePending  PairingState = "pending"
	PairingStateApproved PairingState = "approved"
	PairingStateRejected PairingState = "rejected"
	PairingStateUsed     PairingState = "used"
	PairingStateExpired  PairingState = "expired"
)

// Pairing challenge errors. These are domain decisions, not wire errors.
var (
	ErrPairingChallengeNotPending = errors.New("pairing challenge is no longer pending")
	ErrPairingChallengeExpired    = errors.New("pairing challenge has expired")
)

// PairingChallenge is a short-lived, single-use code that a remote device
// presents to prove it was authorized by the local user. The code is stored
// hashed; the plaintext is shown to the user once and never persisted.
type PairingChallenge struct {
	ID          string
	CodeHash    string // SHA-256 hash of the short pairing code
	State       PairingState
	DeviceLabel string // optional human-readable label set at approval
	CreatedAt   time.Time
	ExpiresAt   time.Time
	DecidedAt   *time.Time // when approved/rejected/used
}

// NewPairingChallenge creates a pending challenge with the given expiry.
func NewPairingChallenge(id, codeHash string, ttl time.Duration, now time.Time) *PairingChallenge {
	return &PairingChallenge{
		ID:        id,
		CodeHash:  codeHash,
		State:     PairingStatePending,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
}

// IsExpired reports whether the challenge has passed its expiry time. A
// challenge is expired at or after ExpiresAt, so the exact expiry instant is
// no longer valid.
func (c *PairingChallenge) IsExpired(now time.Time) bool {
	return !now.Before(c.ExpiresAt)
}

// canTransition returns nil if the challenge is still pending and not expired.
func (c *PairingChallenge) canTransition(now time.Time) error {
	if c.IsExpired(now) {
		return ErrPairingChallengeExpired
	}
	if c.State != PairingStatePending {
		return ErrPairingChallengeNotPending
	}
	return nil
}

// Approve transitions the challenge to the approved state. Only a pending,
// non-expired challenge can be approved.
func (c *PairingChallenge) Approve(now time.Time) error {
	if err := c.canTransition(now); err != nil {
		return err
	}
	c.State = PairingStateApproved
	t := now
	c.DecidedAt = &t
	return nil
}

// Reject transitions the challenge to the rejected state. Only a pending,
// non-expired challenge can be rejected.
func (c *PairingChallenge) Reject(now time.Time) error {
	if err := c.canTransition(now); err != nil {
		return err
	}
	c.State = PairingStateRejected
	t := now
	c.DecidedAt = &t
	return nil
}

// MarkUsed transitions the challenge to the used state, indicating the
// challenge has been exchanged for a session token. Only an approved,
// non-expired challenge can be marked used.
func (c *PairingChallenge) MarkUsed(now time.Time) error {
	if c.IsExpired(now) {
		return ErrPairingChallengeExpired
	}
	if c.State != PairingStateApproved {
		return ErrPairingChallengeNotPending
	}
	c.State = PairingStateUsed
	t := now
	c.DecidedAt = &t
	return nil
}

// PairingSession is an opaque high-entropy session token issued in exchange
// for a valid pairing challenge. Only the token hash is stored server-side;
// the plaintext token is returned once and carried in a cookie by the client.
type PairingSession struct {
	ID          string
	TokenHash   string // SHA-256 hash of the opaque session token
	DeviceLabel string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	Revoked     bool
}

// NewPairingSession creates a new active session with the given expiry.
func NewPairingSession(id, tokenHash, deviceLabel string, ttl time.Duration, now time.Time) *PairingSession {
	return &PairingSession{
		ID:          id,
		TokenHash:   tokenHash,
		DeviceLabel: deviceLabel,
		CreatedAt:   now,
		LastSeenAt:  now,
		ExpiresAt:   now.Add(ttl),
	}
}

// IsValid reports whether the session is active, not revoked, and not expired.
func (s *PairingSession) IsValid(now time.Time) bool {
	if s.Revoked {
		return false
	}
	return now.Before(s.ExpiresAt)
}

// Touch updates the last-seen timestamp.
func (s *PairingSession) Touch(now time.Time) {
	s.LastSeenAt = now
}

// Revoke marks the session as revoked. It will no longer pass IsValid.
func (s *PairingSession) Revoke() {
	s.Revoked = true
}
