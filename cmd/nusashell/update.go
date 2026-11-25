package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"

	appupdate "github.com/jahrulnr/nusashell/application/update"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/cli/admin"
	repobinary "github.com/jahrulnr/nusashell/infrastructure/repository/binary"
	reprelease "github.com/jahrulnr/nusashell/infrastructure/service/release"
	"github.com/jahrulnr/nusashell/infrastructure/system"
)

// Where the update check looks, and the one command that takes the latest
// release by hand. The installer is the only install path, so an in-app update
// and a fresh install place the same file.
const (
	releaseAPI  = "https://api.github.com"
	releaseRepo = "jahrulnr/NusaShell"
	// updateTTL caps how often the release source is asked while the WebUI
	// runs; every browser poll in between is answered from the cache.
	updateTTL = time.Hour
	// checksumsAsset is the file the release publishes beside its binaries.
	checksumsAsset = "SHA256SUMS"
)

// installCommand is the terminal one-liner the About panel offers, matched
// to the shell the platform actually ships.
func installCommand() string {
	if runtime.GOOS == "windows" {
		return "irm https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.ps1 | iex"
	}
	return "curl -fsSL https://raw.githubusercontent.com/jahrulnr/NusaShell/master/scripts/install.sh | bash"
}

// newUpdates wires the update check and the in-app install for this build.
// The install replaces the installed executable and restarts the WebUI through
// the host service manager, or replaces the running process when none owns it.
// beforeRestart runs the shutdown barrier on the process-mode path; lifetime
// bounds the install job, so shutting the server down stops it.
func newUpdates(svc system.Service, lifetime context.Context, beforeRestart func(ctx context.Context) error) (*appupdate.App, error) {
	bin, err := admin.InstalledBinPath()
	if err != nil {
		return nil, err
	}
	// Only the installed binary may replace itself. An instance running from
	// a checkout, a temp dir, or a test harness would otherwise write a
	// release over the real install and ask the service manager to restart
	// the real service — killing whatever runs there.
	blocked := ""
	if self, selfErr := system.SelfPath(); selfErr != nil {
		blocked = fmt.Sprintf("this process cannot resolve its own executable (%v), so it cannot prove it is the installed binary %s", selfErr, bin)
	} else if !system.SameBinary(self, bin) {
		blocked = fmt.Sprintf("this server runs from %s, not from the installed binary %s — installing here would replace the real installation and restart its service; run %s to update, or use the installer", self, bin, bin)
	}
	// Releases publish linux and windows binaries only. Any other platform
	// keeps the check but cannot install, so a darwin build never fetches
	// an asset it could not run.
	asset := ""
	switch runtime.GOOS {
	case "linux":
		asset = "nusashell-linux-" + runtime.GOARCH
	case "windows":
		asset = "nusashell-windows-" + runtime.GOARCH + ".exe"
	}
	source := reprelease.NewGitHub(&http.Client{}, releaseAPI, releaseRepo, "nusashell/"+version)
	return appupdate.New(appupdate.Deps{
		Source:         source,
		Binary:         repobinary.NewInstalled(bin),
		Restarter:      restartAfterBarrier{restarter: system.NewRestarter(svc, admin.CLIName, bin, os.Args)},
		BinaryAsset:    asset,
		ChecksumsAsset: checksumsAsset,
		InstallBlocked: blocked,
		BeforeRestart:  beforeRestart,
		Lifetime:       lifetime,
	}, version, installCommand(), updateTTL), nil
}

// restartAfterBarrier owns what happens when the process-mode restart cannot
// happen. By then the barrier has closed admission, drained the sockets and
// closed storage: there is no half-alive state to return to, so a failed exec
// exits 1 with a message — a supervisor restarts the process, a foreground run
// tells the owner to start it again. Service mode is the manager's restart and
// never reaches this point.
type restartAfterBarrier struct {
	restarter system.Restarter
}

func (r restartAfterBarrier) Mode() string { return r.restarter.Mode() }

func (r restartAfterBarrier) Restart(mode string) error {
	err := r.restarter.Restart(mode)
	if err == nil || mode == "service" {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: restart after shutdown failed: %v — start it again\n", admin.CLIName, err)
	slog.Error("restart after shutdown failed", "error", err)
	os.Exit(1)
	return err // unreachable
}
