package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserProfileDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this host")
	}
	for _, tc := range []struct {
		name       string
		dir        string
		storageDir string
		want       string
	}{
		{"unset keeps the storage default", "", "/srv/data", "/srv/data/browser"},
		{"an absolute folder is used as given", "/var/lib/profiles", "/srv/data", "/var/lib/profiles"},
		{"a tilde names the owner's home", "~/profiles", "/srv/data", filepath.Join(home, "profiles")},
		{"a bare tilde is the home itself", "~", "/srv/data", home},
		{"a relative folder stays relative to the cwd", "profiles", "/srv/data", "profiles"},
		{"surrounding space is ignored", "  /var/lib/profiles\n", "/srv/data", "/var/lib/profiles"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := browserProfileDir(tc.dir, tc.storageDir); got != tc.want {
				t.Fatalf("browserProfileDir(%q, %q) = %q, want %q", tc.dir, tc.storageDir, got, tc.want)
			}
		})
	}
}
