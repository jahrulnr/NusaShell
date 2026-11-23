package appbrowser

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// The login vault: a login a page submits is offered to the owner to save
// in the tab's profile; saved logins fill the page again — from the live
// view on the owner's pick, or by the agent with browser_vault_fill, which
// never sees the password.

// vaultTag is what a filled password reads as in tool results.
const vaultTag = "[[vault:password]]"

type pendingLogin struct {
	offer    dservice.BrowserLoginOffer
	profile  string
	password string
}

// LoginView is a saved login without its password.
type LoginView struct {
	ID        string `json:"id"`
	Profile   string `json:"profile"`
	Origin    string `json:"origin"`
	Username  string `json:"username"`
	UpdatedAt string `json:"updatedAt"`
}

func loginView(l aggregate.BrowserLogin) LoginView {
	return LoginView{ID: l.ID(), Profile: l.Profile(), Origin: l.Origin(), Username: l.Username(), UpdatedAt: l.UpdatedAt().UTC().Format(time.RFC3339)}
}

// CaptureLogin takes a login a page submitted and asks the owner whether
// to save it — unless the profile already holds exactly that login.
func (a *App) CaptureLogin(c dservice.BrowserLoginCapture) {
	if a.logins == nil {
		return
	}
	origin, ok := aggregate.NormalizeOrigin(c.Origin)
	if !ok {
		return
	}
	ctx := context.Background()
	if saved, err := a.logins.List(ctx, c.Profile); err == nil {
		for _, l := range saved {
			if l.Origin() == origin && l.Username() == c.Username {
				if pw, err := a.logins.Password(ctx, l.ID()); err == nil && pw == c.Password {
					return
				}
			}
		}
	}
	s, ok := a.browser.Session(c.ConversationID)
	if !ok {
		return
	}
	p := &pendingLogin{offer: dservice.BrowserLoginOffer{ID: "offer_" + strings.ToLower(rand.Text()[:10]), Origin: origin, Username: c.Username},
		profile: c.Profile, password: c.Password}
	a.mu.Lock()
	a.pending[c.ConversationID] = p
	a.mu.Unlock()
	s.SetLoginOffer(&p.offer)
}

// DecideLogin saves (with optional edits) or dismisses the offer shown.
func (a *App) DecideLogin(ctx context.Context, convID, id string, save bool, username, password *string) error {
	a.mu.Lock()
	p := a.pending[convID]
	if p == nil || p.offer.ID != id {
		a.mu.Unlock()
		return apperror.NewNotFoundError("login offer", nil)
	}
	delete(a.pending, convID)
	a.mu.Unlock()
	if s, ok := a.browser.Session(convID); ok {
		s.SetLoginOffer(nil)
	}
	if !save {
		return nil
	}
	user, pass := p.offer.Username, p.password
	if username != nil {
		user = *username
	}
	if password != nil && *password != "" {
		pass = *password
	}
	l, err := aggregate.NewBrowserLogin("", p.profile, p.offer.Origin, user, time.Now())
	if err != nil {
		return apperror.New(400, err.Error(), err)
	}
	_, err = a.logins.Save(ctx, l, pass)
	return err
}

// PageLogins lists the saved logins for the page the thread's tab is on.
func (a *App) PageLogins(ctx context.Context, convID string) ([]LoginView, error) {
	s, ok := a.browser.Session(convID)
	if !ok || a.logins == nil {
		return []LoginView{}, nil
	}
	origin, ok := aggregate.NormalizeOrigin(s.Page().URL)
	if !ok {
		return []LoginView{}, nil
	}
	saved, err := a.logins.List(ctx, s.Profile())
	if err != nil {
		return nil, err
	}
	out := []LoginView{}
	for _, l := range saved {
		if l.Origin() == origin {
			out = append(out, loginView(l))
		}
	}
	return out, nil
}

// fill fills one saved login into the tab, masking its password from then
// on.
func (a *App) fill(ctx context.Context, convID string, s dservice.BrowserSession, l aggregate.BrowserLogin, replace bool) (bool, error) {
	pw, err := a.logins.Password(ctx, l.ID())
	if err != nil {
		return false, err
	}
	a.remember(convID, map[string]string{pw: vaultTag})
	return s.FillLogin(ctx, l.Origin(), l.Username(), pw, replace)
}

// Autofill fills the saved login the owner picked for this page.
func (a *App) Autofill(ctx context.Context, convID, id string) (bool, error) {
	s, ok := a.browser.Session(convID)
	if !ok || a.logins == nil {
		return false, apperror.NewNotFoundError("browser session", nil)
	}
	l, err := a.logins.Get(ctx, id)
	if err != nil {
		return false, err
	}
	origin, _ := aggregate.NormalizeOrigin(s.Page().URL)
	if l.Profile() != s.Profile() || l.Origin() != origin {
		return false, apperror.New(409, "that login is not for this page", nil)
	}
	return a.fill(ctx, convID, s, *l, true)
}

// VaultFill fills the page's login from the profile's saved login for its
// origin — the one for username, or the most recent. The caller learns
// only whether a login was filled.
func (a *App) VaultFill(ctx context.Context, convID, username string) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	if a.logins == nil {
		return done(s, "no saved logins")
	}
	views, err := a.PageLogins(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	for _, v := range views {
		if username != "" && v.Username != username {
			continue
		}
		l, err := a.logins.Get(ctx, v.ID)
		if err != nil {
			return Result{}, err
		}
		filled, err := a.fill(ctx, convID, s, *l, false)
		if err != nil {
			return Result{}, err
		}
		if filled {
			return done(s, "filled the saved login for "+v.Username+"; the form is not submitted")
		}
		return done(s, "a saved login exists but the page has no matching empty login fields")
	}
	return done(s, "no saved login for this page")
}

// Logins lists saved logins of a profile, or all with "".
func (a *App) Logins(ctx context.Context, profile string) ([]LoginView, error) {
	if a.logins == nil {
		return []LoginView{}, nil
	}
	saved, err := a.logins.List(ctx, profile)
	if err != nil {
		return nil, err
	}
	out := make([]LoginView, 0, len(saved))
	for _, l := range saved {
		out = append(out, loginView(l))
	}
	return out, nil
}

// DeleteLogin removes a saved login.
func (a *App) DeleteLogin(ctx context.Context, id string) error {
	if a.logins == nil {
		return apperror.NewNotFoundError("saved login "+id, nil)
	}
	return a.logins.Delete(ctx, id)
}
