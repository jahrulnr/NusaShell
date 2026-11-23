package main

import (
	"context"
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
// lifetime bounds the install job, so shutting the server down stops it.
func newUpdates(svc system.Service, lifetime context.Context) (*appupdate.App, error) {
	bin, err := admin.InstalledBinPath()
	if err != nil {
		return nil, err
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
		Restarter:      system.NewRestarter(svc, admin.CLIName, bin, os.Args),
		BinaryAsset:    asset,
		ChecksumsAsset: checksumsAsset,
		Lifetime:       lifetime,
	}, version, installCommand(), updateTTL), nil
}
