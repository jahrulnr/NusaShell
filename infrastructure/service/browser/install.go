package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// cftPlatforms maps GOOS/GOARCH onto Chrome for Testing platforms. Google
// ships no linux/arm64 build, so proot on Android goes through the
// distro's package manager.
var cftPlatforms = map[string]string{
	"linux/amd64":   "linux64",
	"darwin/amd64":  "mac-x64",
	"darwin/arm64":  "mac-arm64",
	"windows/amd64": "win64",
	"windows/386":   "win32",
}

// cftIndex lists the current Chrome for Testing downloads.
var cftIndex = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

// pkgManagers are probed on PATH in order. On Ubuntu `chromium` is a snap
// stub that installs but may not run; the resolve after the install
// reports that honestly.
var pkgManagers = []struct{ id, bin, pkg, label string }{
	{"apt", "apt-get", "chromium", "Debian / Ubuntu / proot-distro"},
	{"dnf", "dnf", "chromium", "Fedora / RHEL"},
	{"pacman", "pacman", "chromium", "Arch"},
	{"apk", "apk", "chromium", "Alpine"},
	{"zypper", "zypper", "chromium", "openSUSE"},
}

var lookPath = exec.LookPath

// prootLike reports a proot userland (Termux's proot-distro and the like):
// an Android kernel under a Linux distro, or proot's own environment.
// Chrome cannot build its sandbox there.
func prootLike() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	for _, k := range []string{"PROOT_L2S_DIR", "PROOT_TMP_DIR", "PROOT_LOADER", "PROOT_NO_SECCOMP"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	if strings.Contains(os.Getenv("LD_PRELOAD"), "proot") {
		return true
	}
	rel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(rel)), "android")
}

// InstallOffer reports the executable that resolves, or how to get one.
func (m *Manager) InstallOffer() dservice.BrowserInstallOffer {
	offer := dservice.BrowserInstallOffer{Platform: runtime.GOOS + "/" + runtime.GOARCH, Proot: prootLike(), Methods: []dservice.BrowserInstallMethod{}}
	p, searched, err := resolveExec(m.opts.ExecPath, m.opts.InstallDir)
	if err == nil {
		offer.ExecPath = p
		return offer
	}
	offer.Missing, offer.Searched = true, searched
	if plat := cftPlatforms[offer.Platform]; plat != "" && m.opts.InstallDir != "" {
		offer.Methods = append(offer.Methods, dservice.BrowserInstallMethod{
			ID: "download", Label: "Download Chrome for Testing (headless shell)",
			Detail: "chrome-headless-shell " + plat + " into " + m.opts.InstallDir,
		})
	}
	for _, pm := range pkgManagers {
		if _, err := lookPath(pm.bin); err == nil {
			offer.Methods = append(offer.Methods, dservice.BrowserInstallMethod{
				ID: "pkg:" + pm.id, Label: "Install " + pm.pkg + " with " + pm.bin,
				Detail: pm.bin + " install " + pm.pkg + " (" + pm.label + ")", NeedsRoot: true,
			})
		}
	}
	return offer
}

// Install runs one offered method and resolves the executable afterwards.
// Only the methods listed here run; nothing the caller sends becomes a
// command.
func (m *Manager) Install(ctx context.Context, method string, log io.Writer) (string, error) {
	switch {
	case method == "download":
		plat := cftPlatforms[runtime.GOOS+"/"+runtime.GOARCH]
		if plat == "" {
			return "", fmt.Errorf("no Chrome for Testing build exists for %s/%s", runtime.GOOS, runtime.GOARCH)
		}
		if m.opts.InstallDir == "" {
			return "", errors.New("no install folder is configured")
		}
		if err := installHeadlessShell(ctx, plat, m.opts.InstallDir, log); err != nil {
			return "", err
		}
	case strings.HasPrefix(method, "pkg:"):
		id := strings.TrimPrefix(method, "pkg:")
		i := -1
		for j := range pkgManagers {
			if pkgManagers[j].id == id {
				i = j
			}
		}
		if i < 0 {
			return "", fmt.Errorf("unknown install method %q", method)
		}
		if _, err := lookPath(pkgManagers[i].bin); err != nil {
			return "", fmt.Errorf("%s is not on PATH", pkgManagers[i].bin)
		}
		if err := pkgInstall(ctx, pkgManagers[i].bin, pkgManagers[i].pkg, log); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unknown install method %q", method)
	}
	p, _, err := resolveExec(m.opts.ExecPath, m.opts.InstallDir)
	if err != nil {
		return "", fmt.Errorf("the install ran but no browser executable resolves: %w", err)
	}
	fmt.Fprintf(log, "\nResolved executable: %s\n", p)
	return p, nil
}

// installHeadlessShell downloads the Stable chrome-headless-shell zip and
// unpacks it under root, where resolveExec finds it.
func installHeadlessShell(ctx context.Context, plat, root string, log io.Writer) error {
	fmt.Fprintf(log, "Fetching %s\n", cftIndex)
	var body bytes.Buffer
	if err := download(ctx, cftIndex, &body, 8<<20); err != nil {
		return fmt.Errorf("could not read the Chrome for Testing index: %w", err)
	}
	var doc struct {
		Channels map[string]struct {
			Downloads map[string][]struct {
				Platform string `json:"platform"`
				URL      string `json:"url"`
			} `json:"downloads"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(body.Bytes(), &doc); err != nil {
		return fmt.Errorf("bad Chrome for Testing index: %w", err)
	}
	url := ""
	for _, d := range doc.Channels["Stable"].Downloads["chrome-headless-shell"] {
		if d.Platform == plat {
			url = d.URL
		}
	}
	if url == "" {
		return fmt.Errorf("the index lists no chrome-headless-shell for %s", plat)
	}
	fmt.Fprintf(log, "Downloading %s\n", url)
	tmp, err := os.CreateTemp("", "nusashell-chrome-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = download(ctx, url, tmp, 512<<20)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	z, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return fmt.Errorf("the download is not a valid zip: %w", err)
	}
	defer z.Close()
	fmt.Fprintf(log, "Extracting to %s\n", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	base := filepath.Clean(root) + string(os.PathSeparator)
	for _, f := range z.File {
		target := filepath.Join(root, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), base) {
			return fmt.Errorf("zip entry escapes the install folder: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip entry is a link: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode().Perm()|0o644)
		if err == nil {
			_, err = io.Copy(out, rc)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
		}
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// download copies url into w, refusing a body over limit.
func download(ctx context.Context, url string, w io.Writer, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("larger than %d MB", limit>>20)
	}
	return err
}

// pkgInstall installs pkg with bin, streaming its output to log. apt-get
// runs noninteractive so debconf cannot stall it.
func pkgInstall(ctx context.Context, bin, pkg string, log io.Writer) error {
	var steps [][]string
	switch bin {
	case "apt-get":
		steps = [][]string{{"update"}, {"install", "-y", pkg}}
	case "dnf", "zypper":
		steps = [][]string{{"install", "-y", pkg}}
	case "pacman":
		steps = [][]string{{"-Sy", "--noconfirm", pkg}}
	case "apk":
		steps = [][]string{{"add", pkg}}
	default:
		return fmt.Errorf("no install recipe for %s", bin)
	}
	for _, args := range steps {
		fmt.Fprintf(log, "$ %s %s\n", bin, strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s %s: %w", bin, strings.Join(args, " "), err)
		}
	}
	return nil
}

// isExecutableFile reports a file this platform can run. Windows mode bits
// are synthetic, so the extension decides there.
func isExecutableFile(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".exe", ".bat", ".cmd", ".com":
			return true
		}
		return false
	}
	return st.Mode()&0o111 != 0
}
