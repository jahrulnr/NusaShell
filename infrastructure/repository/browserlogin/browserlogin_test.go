package repobrowserlogin

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/migration"
)

func TestLogins(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secrets, err := reposecret.New(filepath.Join(dir, "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer secrets.Close()
	r := New(db, secrets)

	l, _ := aggregate.NewBrowserLogin("", "default", "https://Example.com/login?x=1", "ada", time.Now())
	if l.Origin() != "https://example.com" {
		t.Fatalf("origin = %q", l.Origin())
	}
	first, err := r.Save(ctx, l, "one")
	if err != nil {
		t.Fatal(err)
	}
	// The same account again refreshes its password under the same id.
	again, _ := aggregate.NewBrowserLogin("", "default", "https://example.com", "ada", time.Now().Add(time.Second))
	second, err := r.Save(ctx, again, "two")
	if err != nil || second.ID() != first.ID() {
		t.Fatalf("second save = %v, %v", second, err)
	}
	if pw, _ := r.Password(ctx, first.ID()); pw != "two" {
		t.Fatalf("password = %q", pw)
	}
	other, _ := aggregate.NewBrowserLogin("", "work", "https://example.com", "ada", time.Now())
	if _, err := r.Save(ctx, other, "w"); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.List(ctx, "default"); len(list) != 1 {
		t.Fatalf("default profile has %d logins", len(list))
	}
	if list, _ := r.List(ctx, ""); len(list) != 2 {
		t.Fatalf("all profiles have %d logins", len(list))
	}
	if err := r.Delete(ctx, first.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Password(ctx, first.ID()); apperror.As(err).StatusCode() != 404 {
		t.Fatalf("password after delete = %v", err)
	}
	if _, err := aggregate.NewBrowserLogin("", "default", "file:///etc/passwd", "x", time.Now()); err == nil {
		t.Fatal("a non-http origin must be refused")
	}
}
