package aggregate

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// BrowserLogin is a login saved in a browser profile for one origin. Its
// password lives in the secret store, never on the aggregate.
type BrowserLogin struct {
	id        string
	profile   string
	origin    string
	username  string
	createdAt time.Time
	updatedAt time.Time
}

// NormalizeOrigin reduces a URL to its http(s) origin; false for any
// other scheme.
func NormalizeOrigin(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), true
}

// NewBrowserLogin builds a login for an origin in a profile.
func NewBrowserLogin(id, profile, origin, username string, now time.Time) (*BrowserLogin, error) {
	vo, err := valueobject.NewID("login", id)
	if err != nil {
		return nil, err
	}
	o, ok := NormalizeOrigin(origin)
	if !ok {
		return nil, errors.New("a saved login needs an http or https origin")
	}
	if strings.TrimSpace(profile) == "" {
		return nil, errors.New("a saved login needs a profile")
	}
	return &BrowserLogin{id: vo.String(), profile: profile, origin: o, username: username, createdAt: now, updatedAt: now}, nil
}

// Touch stamps a refresh of the saved password.
func (l *BrowserLogin) Touch(now time.Time) { l.updatedAt = now }

// Restore sets the stored timestamps.
func (l *BrowserLogin) Restore(createdAt, updatedAt time.Time) {
	l.createdAt, l.updatedAt = createdAt, updatedAt
}

func (l BrowserLogin) ID() string           { return l.id }
func (l BrowserLogin) Profile() string      { return l.profile }
func (l BrowserLogin) Origin() string       { return l.origin }
func (l BrowserLogin) Username() string     { return l.username }
func (l BrowserLogin) CreatedAt() time.Time { return l.createdAt }
func (l BrowserLogin) UpdatedAt() time.Time { return l.updatedAt }
