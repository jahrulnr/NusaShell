package admin

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	appauth "github.com/jahrulnr/nusashell/application/auth"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	repoauth "github.com/jahrulnr/nusashell/infrastructure/repository/auth"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/infrastructure/service/password"
	"github.com/jahrulnr/nusashell/migration"
)

const passwordUsage = `Usage: nusashell password [--stdin]

Sets the owner password the WebUI asks for when a login is required
(server.auth). Every open session is logged out. Without --stdin the
password is read twice from the terminal with echo off; with --stdin one
line is read from standard input.
`

// Password is the `nusashell password` handler.
func Password(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, passwordUsage) }
	fromStdin := fs.Bool("stdin", false, "read the password from standard input")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return 2
	}
	cfg, _, err := LoadConfig(false)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	var pw string
	if *fromStdin {
		pw, err = readSecretLine(stdin, io.Discard, "")
	} else {
		pw, err = readSecretLine(stdin, stderr, "New password: ")
		if err == nil {
			var again string
			again, err = readSecretLine(stdin, stderr, "Repeat password: ")
			if err == nil && again != pw {
				fmt.Fprintln(stderr, CLIName+": passwords do not match")
				return 1
			}
		}
	}
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(cfg.Storage.Dir, "nusashell.db"), migration.Files)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	defer db.Close()
	// The mode only decides when a login is asked for; setting the
	// password is allowed under every mode.
	app, err := appauth.New(repoauth.New(db), password.Bcrypt{}, enum.AuthModeOn, false)
	if err != nil {
		printErr(stderr, err)
		return 1
	}
	if err := app.SetPassword(ctx, pw); err != nil {
		printErr(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "owner password updated; open sessions were logged out")
	return 0
}
