package browser

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The download method fetches the index, unpacks the platform's headless
// shell into the install folder, and the executable resolves from there;
// a zip entry that leaves the folder is refused.
func TestInstallDownload(t *testing.T) {
	plat := cftPlatforms[runtime.GOOS+"/"+runtime.GOARCH]
	if plat == "" || runtime.GOOS == "windows" {
		t.Skip("no Chrome for Testing build here")
	}
	zipped := func(name string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		w, _ := zw.CreateHeader(h)
		_, _ = io.WriteString(w, "#!/bin/sh\n")
		_ = zw.Close()
		return buf.Bytes()
	}
	good := zipped("chrome-headless-shell-" + plat + "/chrome-headless-shell")
	evil := zipped("../escape")
	var srv *httptest.Server
	serveZip := good
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			_, _ = io.WriteString(w, `{"channels":{"Stable":{"downloads":{"chrome-headless-shell":[{"platform":"`+plat+`","url":"`+srv.URL+`/shell.zip"}]}}}}`)
			return
		}
		_, _ = w.Write(serveZip)
	}))
	defer srv.Close()
	oldIndex, oldLook := cftIndex, lookPath
	cftIndex = srv.URL + "/index.json"
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound } // nothing on PATH
	t.Cleanup(func() { cftIndex, lookPath = oldIndex, oldLook })

	dir := filepath.Join(t.TempDir(), "chrome")
	m := New(Options{InstallDir: dir})
	defer m.Shutdown()
	offer := m.InstallOffer()
	if !offer.Missing || len(offer.Methods) != 1 || offer.Methods[0].ID != "download" || !strings.Contains(offer.Searched, dir) {
		t.Fatalf("offer = %+v", offer)
	}
	if _, err := m.Install(t.Context(), "pkg:nope", io.Discard); err == nil {
		t.Fatal("an unknown method must be refused")
	}

	serveZip = evil
	if _, err := m.Install(t.Context(), "download", io.Discard); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("zip slip: %v", err)
	}

	serveZip = good
	var log bytes.Buffer
	p, err := m.Install(t.Context(), "download", &log)
	want := filepath.Join(dir, "chrome-headless-shell-"+plat, "chrome-headless-shell")
	if err != nil || p != want {
		t.Fatalf("install = %q, %v\n%s", p, err, log.String())
	}
	if offer := m.InstallOffer(); offer.Missing || offer.ExecPath != want {
		t.Fatalf("after install = %+v", offer)
	}
}
