package conversation

import (
	"path/filepath"
	"strings"
	"testing"
)

// The preview resolves a linked path with the same expansion the file and
// media tools use: one rule for a file:, a leading ~ and (on Windows)
// %VAR%; everything else is a plain path.
func TestLocalPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{"empty", "", "", "a file path is required"},
		{"relative", "notes.md", filepath.FromSlash("notes.md"), ""},
		{"nested relative", "sub/notes.md", filepath.FromSlash("sub/notes.md"), ""},
		{"absolute", "/tmp/a.md", filepath.FromSlash("/tmp/a.md"), ""},
		{"home alone", "~", home, ""},
		{"home path", "~/notes.md", filepath.Join(home, "notes.md"), ""},
		{"home backslash", `~\notes.md`, filepath.Join(home, "notes.md"), ""},
		{"tilde user is a name", "~owner/x.md", filepath.FromSlash("~owner/x.md"), ""},
		{"tilde mid path is a name", "a/~/b.md", filepath.FromSlash("a/~/b.md"), ""},
		{"file url", "file:///tmp/a.md", filepath.FromSlash("/tmp/a.md"), ""},
		{"file url localhost", "file://localhost/tmp/a.md", filepath.FromSlash("/tmp/a.md"), ""},
		{"file url home", "file://" + home + "/pic.png", filepath.Join(home, "pic.png"), ""},
		{"file url other host", "file://server/share/a.md", "", "not a local path"},
		{"file url no path", "file://localhost", "", "names no path"},
		{"remote scheme", "https://example.com/a.md", "", "expected a local file path"},
		{"hash is a filename character", "a#b.md", filepath.FromSlash("a#b.md"), ""},
		{"percent is a filename character", "50% off.md", filepath.FromSlash("50% off.md"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := localPath(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("localPath(%q) = %q, %v; want error containing %q", tc.in, got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("localPath(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("localPath(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
