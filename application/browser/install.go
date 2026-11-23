package appbrowser

import (
	"context"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// installWait bounds an install: a stalled mirror must not hang it.
const installWait = 15 * time.Minute

// installLogMax keeps the end of an install's output: apt writes megabytes
// and only the end explains a failure.
const installLogMax = 32 << 10

// UseInstaller sets how the browser executable is found and installed.
func (a *App) UseInstaller(i dservice.BrowserInstaller) { a.installer = i }

// InstallOffer reports the browser executable, or how to get one.
func (a *App) InstallOffer() (dservice.BrowserInstallOffer, error) {
	if a.installer == nil {
		return dservice.BrowserInstallOffer{}, apperror.New(503, "the browser is not set up", nil)
	}
	return a.installer.InstallOffer(), nil
}

// InstallResult is what an install reports: the executable that resolves
// now, or why the install failed, and the end of the installer's output.
// A failed install is a result, not an error: its output is the point.
type InstallResult struct {
	ExecPath string `json:"execPath,omitempty"`
	Error    string `json:"error,omitempty"`
	Log      string `json:"log"`
}

// Install runs one offered method.
func (a *App) Install(ctx context.Context, method string) (InstallResult, error) {
	if a.installer == nil {
		return InstallResult{}, apperror.New(503, "the browser is not set up", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, installWait)
	defer cancel()
	log := &tail{}
	p, err := a.installer.Install(ctx, method, log)
	if err != nil {
		return InstallResult{Error: err.Error(), Log: string(log.buf)}, nil
	}
	return InstallResult{ExecPath: p, Log: string(log.buf)}, nil
}

// tail keeps the last installLogMax bytes written.
type tail struct{ buf []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > installLogMax {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-installLogMax:]...)
	}
	return len(p), nil
}
