package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tarGz builds a tar.gz in memory. Entries are {name, body} for regular
// files, {name, dir:true} for dirs, and {name, link, hard} for links.
type tarEntry struct {
	name string
	body string
	dir  bool
	link string
	hard bool
	mode int64
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode}
		switch {
		case e.dir:
			hdr.Typeflag = tar.TypeDir
		case e.link != "":
			hdr.Linkname = e.link
			if e.hard {
				hdr.Typeflag = tar.TypeLink
			} else {
				hdr.Typeflag = tar.TypeSymlink
			}
		default:
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(e.body))
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestUntarGzExtractsTree(t *testing.T) {
	dest := t.TempDir()
	data := buildTarGz(t, []tarEntry{
		{name: "dir/", dir: true},
		{name: "dir/file.txt", body: "hello", mode: 0o640},
		{name: "top.txt", body: "x"},
	})
	if err := UntarGz(bytes.NewReader(data), dest, nil); err != nil {
		t.Fatalf("UntarGz: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "dir", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q", got)
	}
	// Compare against a control file written with the same mode so the
	// assertion survives the test environment's umask.
	control := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(control, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	cinfo, err := os.Stat(control)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dest, "dir", "file.txt")); err != nil || info.Mode().Perm() != cinfo.Mode().Perm() {
		t.Fatalf("mode = %v, want %v (control)", info, cinfo.Mode().Perm())
	}
}

func TestUntarGzForcedFileMode(t *testing.T) {
	dest := t.TempDir()
	data := buildTarGz(t, []tarEntry{{name: "bin", body: "x", mode: 0o755}})
	if err := UntarGz(bytes.NewReader(data), dest, &Options{FileMode: 0o644}); err != nil {
		t.Fatalf("UntarGz: %v", err)
	}
	control := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(control, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cinfo, err := os.Stat(control)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != cinfo.Mode().Perm() {
		t.Fatalf("mode = %v, want %v (control)", info.Mode().Perm(), cinfo.Mode().Perm())
	}
}

func TestUntarGzRejectsTraversal(t *testing.T) {
	cases := map[string]string{
		"parent":          "../evil.txt",
		"absolute":        "/abs/evil.txt",
		"rootedBackslash": `\abs\evil.txt`,
		"nestedEscape":    "a/../../b",
		"midPath":         "ok/../evil.txt",
		"backslash":       `..\evil.txt`,
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			outside := filepath.Join(filepath.Dir(dest), "evil.txt")
			data := buildTarGz(t, []tarEntry{{name: entry, body: "boom"}})
			err := UntarGz(bytes.NewReader(data), dest, nil)
			if err == nil {
				t.Fatalf("entry %q must be rejected", entry)
			}
			if !strings.Contains(err.Error(), "unsafe archive entry") {
				t.Fatalf("error = %v", err)
			}
			if _, serr := os.Stat(outside); serr == nil {
				t.Fatalf("entry %q escaped to %s", entry, outside)
			}
		})
	}
}

func TestUntarGzAllowsNonTraversalDotDotName(t *testing.T) {
	// ".." inside a segment is not traversal; rejecting it was a false
	// positive of the old substring guard.
	dest := t.TempDir()
	data := buildTarGz(t, []tarEntry{{name: "v1..2.txt", body: "ok"}})
	if err := UntarGz(bytes.NewReader(data), dest, nil); err != nil {
		t.Fatalf("UntarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "v1..2.txt")); err != nil {
		t.Fatalf("file missing: %v", err)
	}
}

func TestUntarGzSkipsLinksByDefault(t *testing.T) {
	dest := t.TempDir()
	data := buildTarGz(t, []tarEntry{
		{name: "real.txt", body: "x"},
		{name: "link.txt", link: "real.txt"},
	})
	if err := UntarGz(bytes.NewReader(data), dest, nil); err != nil {
		t.Fatalf("UntarGz: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "link.txt")); err == nil {
		t.Fatal("symlink must not be recreated by default")
	}
}

func TestUntarGzRecreatesSafeSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	dest := t.TempDir()
	data := buildTarGz(t, []tarEntry{
		{name: "lib.so.1", body: "ELF"},
		{name: "lib.so", link: "lib.so.1"},
	})
	if err := UntarGz(bytes.NewReader(data), dest, &Options{RecreateLinks: true}); err != nil {
		t.Fatalf("UntarGz: %v", err)
	}
	got, err := os.Readlink(filepath.Join(dest, "lib.so"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "lib.so.1" {
		t.Fatalf("link target = %q", got)
	}
}

func TestUntarGzRejectsEscapingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	for _, link := range []string{"../outside.txt", "/etc/passwd", "sub/../../escape.txt"} {
		dest := t.TempDir()
		data := buildTarGz(t, []tarEntry{
			{name: "ok.txt", body: "x"},
			{name: "bad.link", link: link},
		})
		err := UntarGz(bytes.NewReader(data), dest, &Options{RecreateLinks: true})
		if err == nil || !strings.Contains(err.Error(), "unsafe archive link target") {
			t.Fatalf("link %q: expected rejection, got %v", link, err)
		}
	}
}

func TestUntarGzRejectsEscapingHardlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hardlink test uses unix paths")
	}
	dest := t.TempDir()
	data := buildTarGz(t, []tarEntry{
		{name: "ok.txt", body: "x"},
		{name: "bad.hard", link: "../outside.txt", hard: true},
	})
	err := UntarGz(bytes.NewReader(data), dest, &Options{RecreateLinks: true})
	if err == nil || !strings.Contains(err.Error(), "unsafe archive link target") {
		t.Fatalf("expected rejection, got %v", err)
	}
}

func TestUntarGzPrefixScope(t *testing.T) {
	dest := t.TempDir()
	data := buildTarGz(t, []tarEntry{
		{name: "piper/bin", body: "x"},
		{name: "piper/sub/deep.txt", body: "d"},
		{name: "other/stray.txt", body: "s"},
	})
	if err := UntarGz(bytes.NewReader(data), dest, &Options{Prefix: "piper/"}); err != nil {
		t.Fatalf("UntarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin")); err != nil {
		t.Fatal("piper/bin must land at dest/bin")
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "deep.txt")); err != nil {
		t.Fatal("piper/sub/deep.txt missing")
	}
	if _, err := os.Stat(filepath.Join(dest, "other")); !os.IsNotExist(err) {
		t.Fatal("non-prefix entries must be skipped")
	}
}

func TestUntarGzPrefixRejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	data := buildTarGz(t, []tarEntry{{name: "piper/../evil.txt", body: "boom"}})
	err := UntarGz(bytes.NewReader(data), dest, &Options{Prefix: "piper/"})
	if err == nil || !strings.Contains(err.Error(), "unsafe archive entry") {
		t.Fatalf("expected rejection, got %v", err)
	}
}

func TestUntarGzBadStream(t *testing.T) {
	err := UntarGz(strings.NewReader("not gzip"), t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "bad archive") {
		t.Fatalf("expected bad archive error, got %v", err)
	}
}

func TestUnzipExtractsTree(t *testing.T) {
	dest := t.TempDir()
	zr := buildZip(t, map[string]string{
		"manifest.json":  `{"id":"x"}`,
		"mcp/server.cjs": "code",
	})
	if err := Unzip(zr, dest, nil); err != nil {
		t.Fatalf("Unzip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "mcp", "server.cjs")); err != nil {
		t.Fatal(err)
	}
}

func TestUnzipRejectsTraversal(t *testing.T) {
	for _, entry := range []string{"../evil", "/abs/evil", `\abs\evil`, "a/../../b"} {
		dest := t.TempDir()
		zr := buildZip(t, map[string]string{entry: "boom"})
		err := Unzip(zr, dest, nil)
		if err == nil || !strings.Contains(err.Error(), "unsafe archive entry") {
			t.Fatalf("entry %q: expected rejection, got %v", entry, err)
		}
	}
}

func TestUnzipPrefixAndSizeCaps(t *testing.T) {
	dest := t.TempDir()
	zr := buildZip(t, map[string]string{
		"skill/SKILL.md":   "md",
		"skill/big.bin":    strings.Repeat("x", 64),
		"outside/stray.md": "s",
	})
	opts := &Options{Prefix: "skill/", MaxFileBytes: 32}
	err := Unzip(zr, dest, opts)
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("expected file cap error, got %v", err)
	}

	dest = t.TempDir()
	zr = buildZip(t, map[string]string{
		"skill/a": strings.Repeat("x", 64),
		"skill/b": strings.Repeat("y", 64),
	})
	opts = &Options{Prefix: "skill/", MaxTotalBytes: 100}
	err = Unzip(zr, dest, opts)
	if err == nil || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("expected total cap error, got %v", err)
	}

	dest = t.TempDir()
	zr = buildZip(t, map[string]string{
		"skill/SKILL.md":   "md",
		"outside/stray.md": "s",
	})
	if err := Unzip(zr, dest, &Options{Prefix: "skill/", FileMode: 0o644}); err != nil {
		t.Fatalf("Unzip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatal("skill/SKILL.md must land at dest/SKILL.md")
	}
	if _, err := os.Stat(filepath.Join(dest, "outside")); !os.IsNotExist(err) {
		t.Fatal("non-prefix entries must be skipped")
	}
}

func TestWalkTarGzEarlyStop(t *testing.T) {
	data := buildTarGz(t, []tarEntry{
		{name: "a.txt", body: "a"},
		{name: "b.txt", body: "b"},
	})
	var seen []string
	err := WalkTarGz(bytes.NewReader(data), func(hdr *tar.Header, body io.Reader) (bool, error) {
		seen = append(seen, hdr.Name)
		return false, nil
	})
	if err != nil {
		t.Fatalf("WalkTarGz: %v", err)
	}
	if len(seen) != 1 || seen[0] != "a.txt" {
		t.Fatalf("seen = %v", seen)
	}
}
