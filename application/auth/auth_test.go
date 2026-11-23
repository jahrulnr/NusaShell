package appauth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	repoauth "github.com/jahrulnr/nusashell/infrastructure/repository/auth"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/infrastructure/service/password"
	"github.com/jahrulnr/nusashell/migration"
)

func newApp(t *testing.T, mode enum.AuthMode, exposed bool) *AuthApp {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	app, err := New(repoauth.New(db), password.Bcrypt{Cost: 4}, mode, exposed)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func status(err error) int { return apperror.As(err).StatusCode() }

func TestModePolicy(t *testing.T) {
	if _, err := New(nil, nil, enum.AuthModeOff, true); status(err) != 400 {
		t.Fatalf("off on an exposed server must refuse to start, got %v", err)
	}
	for _, c := range []struct {
		mode    enum.AuthMode
		exposed bool
		want    bool
	}{{enum.AuthModeAuto, false, false}, {enum.AuthModeAuto, true, true}, {enum.AuthModeOn, false, true}, {enum.AuthModeOff, false, false}} {
		app, err := New(nil, nil, c.mode, c.exposed)
		if err != nil || app.Required() != c.want {
			t.Fatalf("%s exposed=%v: required=%v err=%v", c.mode, c.exposed, app.Required(), err)
		}
	}
}

func TestLoginSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	app := newApp(t, enum.AuthModeOn, false)
	pw, err := app.EnsurePassword(ctx)
	if err != nil || len(pw) < app.session.MinPasswordLen {
		t.Fatalf("generated password %q, %v", pw, err)
	}
	if again, _ := app.EnsurePassword(ctx); again != "" {
		t.Fatal("an existing password must not be replaced")
	}
	if _, _, err := app.Login(ctx, "wrong-password", "1.2.3.4"); status(err) != 401 {
		t.Fatalf("wrong password err = %v", err)
	}
	token, csrf, err := app.Login(ctx, pw, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	s, err := app.Authenticate(ctx, token)
	if err != nil || !CheckCSRF(s, csrf) || CheckCSRF(s, "nope") {
		t.Fatalf("authenticate = %v, %v", s, err)
	}
	// Sessions slide: a use after touchEvery renews the expiry.
	before := s.ExpiresAt()
	app.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	s, err = app.Authenticate(ctx, token)
	if err != nil || !s.ExpiresAt().After(before) {
		t.Fatalf("renewal: %v → %v, %v", before, s.ExpiresAt(), err)
	}
	// Past the TTL the session is gone.
	app.now = func() time.Time { return time.Now().Add(3*time.Hour + app.session.TTL) }
	if _, err := app.Authenticate(ctx, token); status(err) != 401 {
		t.Fatalf("expired session err = %v", err)
	}
	app.now = time.Now
	// A password change ends every session.
	token, _, _ = app.Login(ctx, pw, "1.2.3.4")
	if err := app.SetPassword(ctx, "another-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Authenticate(ctx, token); status(err) != 401 {
		t.Fatalf("session after password change err = %v", err)
	}
	if err := app.SetPassword(ctx, "xyz"); status(err) != 400 {
		t.Fatalf("short password err = %v", err)
	}
}

func TestLoginThrottlesFailures(t *testing.T) {
	ctx := context.Background()
	app := newApp(t, enum.AuthModeOn, false)
	if err := app.SetPassword(ctx, "correct-horse"); err != nil {
		t.Fatal(err)
	}
	for range app.session.LoginBurst {
		_, _, _ = app.Login(ctx, "bad", "9.9.9.9")
	}
	if _, _, err := app.Login(ctx, "correct-horse", "9.9.9.9"); status(err) != 429 {
		t.Fatalf("throttled client err = %v, want 429", err)
	}
	if _, _, err := app.Login(ctx, "correct-horse", "8.8.8.8"); err != nil {
		t.Fatalf("another client must still log in: %v", err)
	}
	app.now = func() time.Time { return time.Now().Add(app.session.LoginWindow + time.Minute) }
	if _, _, err := app.Login(ctx, "correct-horse", "9.9.9.9"); err != nil {
		t.Fatalf("the window must reopen: %v", err)
	}
}

// Changing the password from a page asks the current one, ends every
// other session, and hands the caller a fresh one.
func TestChangePassword(t *testing.T) {
	ctx := context.Background()
	app := newApp(t, enum.AuthModeOn, false)
	if err := app.SetPassword(ctx, "first-pass"); err != nil {
		t.Fatal(err)
	}
	other, _, err := app.Login(ctx, "first-pass", "9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.ChangePassword(ctx, "nope", "second-pass", "1.1.1.1"); status(err) != 403 {
		t.Fatalf("a wrong current password: %v", err)
	}
	if _, _, err := app.ChangePassword(ctx, "first-pass", "x", "1.1.1.1"); status(err) != 400 {
		t.Fatalf("a too-short new password: %v", err)
	}
	token, csrf, err := app.ChangePassword(ctx, "first-pass", "second-pass", "1.1.1.1")
	if err != nil || token == "" || csrf == "" {
		t.Fatalf("change: %q %q %v", token, csrf, err)
	}
	if _, err := app.Authenticate(ctx, other); status(err) != 401 {
		t.Fatalf("the other session must end: %v", err)
	}
	if s, err := app.Authenticate(ctx, token); err != nil || s.CSRF() != csrf {
		t.Fatalf("the caller's new session: %v", err)
	}
	if _, _, err := app.Login(ctx, "second-pass", "9.9.9.9"); err != nil {
		t.Fatalf("the new password logs in: %v", err)
	}
	for range 5 {
		_, _, _ = app.ChangePassword(ctx, "guess", "third-pass", "6.6.6.6")
	}
	if _, _, err := app.ChangePassword(ctx, "second-pass", "third-pass", "6.6.6.6"); status(err) != 429 {
		t.Fatalf("guessing the current password is throttled like a login: %v", err)
	}
}

// With no password set yet, and logins off here, a password is set
// without the current one and no session is opened.
func TestSetFirstPasswordFromAPage(t *testing.T) {
	ctx := context.Background()
	app := newApp(t, enum.AuthModeAuto, false)
	token, _, err := app.ChangePassword(ctx, "", "first-pass", "127.0.0.1")
	if err != nil || token != "" {
		t.Fatalf("first password: %q %v", token, err)
	}
	if has, _ := app.HasPassword(ctx); !has {
		t.Fatal("the password must be set")
	}
	if _, _, err := app.ChangePassword(ctx, "", "second-pass", "127.0.0.1"); status(err) != 403 {
		t.Fatalf("once set, the current password is asked: %v", err)
	}
}
