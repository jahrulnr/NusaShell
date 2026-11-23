package valueobject

import (
	"errors"
	"time"
)

// Session is one logged-in browser. The store keeps only the hash of the
// cookie token; the CSRF token is a separate secret the page echoes on
// every state-changing request.
type Session struct {
	tokenHash string
	csrf      string
	createdAt time.Time
	lastSeen  time.Time
	expiresAt time.Time
}

// NewSession validates the session record.
func NewSession(tokenHash, csrf string, createdAt, lastSeen, expiresAt time.Time) (Session, error) {
	if tokenHash == "" || csrf == "" {
		return Session{}, errors.New("session token hash and csrf token are required")
	}
	if !expiresAt.After(createdAt) {
		return Session{}, errors.New("session must expire after it is created")
	}
	return Session{tokenHash: tokenHash, csrf: csrf, createdAt: createdAt, lastSeen: lastSeen, expiresAt: expiresAt}, nil
}

func (s Session) TokenHash() string    { return s.tokenHash }
func (s Session) CSRF() string         { return s.csrf }
func (s Session) CreatedAt() time.Time { return s.createdAt }
func (s Session) LastSeen() time.Time  { return s.lastSeen }
func (s Session) ExpiresAt() time.Time { return s.expiresAt }

// Expired reports whether the session no longer authenticates at now.
func (s Session) Expired(now time.Time) bool { return !now.Before(s.expiresAt) }

// Renewed slides the session's expiry: use at now keeps it alive for ttl.
func (s Session) Renewed(now time.Time, ttl time.Duration) Session {
	s.lastSeen = now
	s.expiresAt = now.Add(ttl)
	return s
}
