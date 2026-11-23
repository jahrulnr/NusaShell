// Package appauth is the owner login: whether a login is required, the
// owner password, and browser sessions. One owner per server — NusaShell
// is a personal workspace, not a multi-user service.
package appauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/creasty/defaults"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

const (
	// touchEvery bounds session writes: a request renews the session only
	// when the last renewal is older than this.
	touchEvery = time.Hour
)

// AuthApp decides and serves logins.
type AuthApp struct {
	repo     drepo.Auth
	hasher   dservice.PasswordHasher
	required bool
	now      func() time.Time

	session  config.Session
	mu       sync.Mutex
	failures map[string][]time.Time // client -> recent failed attempts
}

// New resolves the login policy. exposed says the server is reachable
// beyond this machine; an unsafe mode/exposure pair is refused.
func New(repo drepo.Auth, hasher dservice.PasswordHasher, mode enum.AuthMode, exposed bool) (*AuthApp, error) {
	required, ok := mode.LoginRequired(exposed)
	if !ok {
		return nil, apperror.New(400, "server.auth is off but the server is reachable from the network — set server.auth to auto or on, or keep server.bind and server.hosts on loopback", nil)
	}
	conf := config.Session{}
	defaults.Set(&conf)
	return &AuthApp{repo: repo, hasher: hasher, required: required, session: conf, now: time.Now, failures: map[string][]time.Time{}}, nil
}

// Required reports whether requests need a session.
func (a *AuthApp) Required() bool { return a.required }

// HasPassword reports whether an owner password is set.
func (a *AuthApp) HasPassword(ctx context.Context) (bool, error) {
	_, err := a.repo.OwnerPasswordHash(ctx)
	if apperror.As(err).StatusCode() == apperror.StatusNotFound {
		return false, nil
	}
	return err == nil, err
}

// EnsurePassword sets a generated owner password when logins are
// required and none exists. It returns the password once so the caller
// can show it; "" means nothing was generated.
func (a *AuthApp) EnsurePassword(ctx context.Context) (string, error) {
	if !a.required {
		return "", nil
	}
	has, err := a.HasPassword(ctx)
	if err != nil || has {
		return "", err
	}
	pw := randomToken(18)
	if err := a.SetPassword(ctx, pw); err != nil {
		return "", err
	}
	return pw, nil
}

// SetPassword replaces the owner password and ends every session.
func (a *AuthApp) SetPassword(ctx context.Context, password string) error {
	if len([]rune(password)) < a.session.MinPasswordLen {
		return apperror.New(400, fmt.Sprintf("password must be at least %d characters", a.session.MinPasswordLen), nil)
	}
	if len(password) > a.session.MaxPasswordBytes {
		return apperror.New(400, fmt.Sprintf("password must be at most %d bytes", a.session.MaxPasswordBytes), nil)
	}
	hash, err := a.hasher.Hash(password)
	if err != nil {
		return err
	}
	if err := a.repo.SetOwnerPasswordHash(ctx, hash); err != nil {
		return err
	}
	return a.repo.DeleteSessions(ctx)
}

// Login checks the password for client (an address used for throttling)
// and opens a session. It returns the cookie token and the CSRF token.
func (a *AuthApp) Login(ctx context.Context, password, client string) (token, csrf string, err error) {
	now := a.now()
	if !a.allow(client, now) {
		return "", "", apperror.New(429, "too many failed logins — wait a few minutes", nil)
	}
	hash, err := a.repo.OwnerPasswordHash(ctx)
	if err != nil && apperror.As(err).StatusCode() != apperror.StatusNotFound {
		return "", "", err
	}
	if err != nil || !a.hasher.Verify(hash, password) {
		a.fail(client, now)
		return "", "", apperror.New(401, "wrong password", nil)
	}
	a.clear(client)
	_ = a.repo.DeleteExpiredSessions(ctx, now.Unix())
	return a.openSession(ctx, now, a.session.TTL)
}

// ChangePassword replaces the owner password from a signed-in page.
// current must match the password set now — a wrong one counts against
// client like a failed login — and is not asked when none is set. Every
// session ends; the caller gets a fresh one so its own page stays in.
func (a *AuthApp) ChangePassword(ctx context.Context, current, next, client string) (token, csrf string, err error) {
	now := a.now()
	if !a.allow(client, now) {
		return "", "", apperror.New(429, "too many wrong passwords — wait a few minutes", nil)
	}
	hash, err := a.repo.OwnerPasswordHash(ctx)
	switch {
	case apperror.As(err).StatusCode() == apperror.StatusNotFound:
	case err != nil:
		return "", "", err
	case !a.hasher.Verify(hash, current):
		a.fail(client, now)
		return "", "", apperror.New(403, "the current password is wrong", nil)
	}
	a.clear(client)
	if err := a.SetPassword(ctx, next); err != nil {
		return "", "", err
	}
	if !a.required {
		return "", "", nil
	}
	return a.openSession(ctx, now, a.session.TTL)
}

// openSession stores a new session lasting ttl and returns its cookie
// token and CSRF token.
func (a *AuthApp) openSession(ctx context.Context, now time.Time, ttl time.Duration) (token, csrf string, err error) {
	token, csrf = randomToken(32), randomToken(24)
	s, err := valueobject.NewSession(tokenHash(token), csrf, now, now, now.Add(ttl))
	if err != nil {
		return "", "", err
	}
	if err := a.repo.SaveSession(ctx, s); err != nil {
		return "", "", err
	}
	return token, csrf, nil
}

// LocalSession opens a short session for a command run on this machine
// (the CLI reaching the running server). It asks no password: whoever can
// open the application database is the owner and could set the password
// too. End it with Logout.
func (a *AuthApp) LocalSession(ctx context.Context, ttl time.Duration) (token, csrf string, err error) {
	return a.openSession(ctx, a.now(), ttl)
}

// Authenticate resolves a cookie token to its live session, sliding its
// expiry on use. 401 when the token is unknown or expired.
func (a *AuthApp) Authenticate(ctx context.Context, token string) (valueobject.Session, error) {
	if token == "" {
		return valueobject.Session{}, apperror.New(401, "login required", nil)
	}
	s, err := a.repo.Session(ctx, tokenHash(token))
	if apperror.As(err).StatusCode() == apperror.StatusNotFound {
		return valueobject.Session{}, apperror.New(401, "login required", nil)
	}
	if err != nil {
		return valueobject.Session{}, err
	}
	now := a.now()
	if s.Expired(now) {
		_ = a.repo.DeleteSession(ctx, s.TokenHash())
		return valueobject.Session{}, apperror.New(401, "session expired", nil)
	}
	if now.Sub(s.LastSeen()) > touchEvery {
		s = s.Renewed(now, a.session.TTL)
		if err := a.repo.SaveSession(ctx, s); err != nil {
			return valueobject.Session{}, err
		}
	}
	return s, nil
}

// CheckCSRF reports whether got is the session's CSRF token.
func CheckCSRF(s valueobject.Session, got string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(s.CSRF()), []byte(got)) == 1
}

// Logout ends the session behind token. An unknown token is not an error.
func (a *AuthApp) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return a.repo.DeleteSession(ctx, tokenHash(token))
}

// allow reports whether client may attempt a login now.
func (a *AuthApp) allow(client string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	recent := a.failures[client][:0]
	for _, t := range a.failures[client] {
		if now.Sub(t) < a.session.LoginWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(a.failures, client)
	} else {
		a.failures[client] = recent
	}
	return len(recent) < a.session.LoginBurst
}

func (a *AuthApp) fail(client string, now time.Time) {
	a.mu.Lock()
	a.failures[client] = append(a.failures[client], now)
	a.mu.Unlock()
}

func (a *AuthApp) clear(client string) {
	a.mu.Lock()
	delete(a.failures, client)
	a.mu.Unlock()
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
