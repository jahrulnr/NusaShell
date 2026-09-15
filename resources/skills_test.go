package resources

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestBuiltinSkillsEmbedHasNoHiddenPrefixedPaths guards against the Go
// embed rule that silently drops any path component starting with "_" or ".".
// A skill that places support files under such a directory (e.g.
// references/_shared/) would compile and ship but never reach the binary,
// causing the installed copy to fail its own reference checker. This test
// fails fast if any bundled skill path has a component that the embed would
// drop.
func TestBuiltinSkillsEmbedHasNoHiddenPrefixedPaths(t *testing.T) {
	err := fs.WalkDir(BuiltinSkillsFS, "agent/skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, part := range strings.Split(path, "/") {
			if part == "" {
				continue
			}
			if strings.HasPrefix(part, "_") || strings.HasPrefix(part, ".") {
				t.Errorf("embedded skill path %q has a component %q that Go embed drops (starts with _ or .); rename it so the file ships", path, part)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk BuiltinSkillsFS: %v", err)
	}
}

// TestBuiltinSkillsEmbedMatchesDiskSourceOfTruth asserts that every file
// present on disk under resources/agent/skills/ is present in the embedded
// filesystem. This catches the silent-drop class of bug where a file exists
// in the repo but never makes it into the binary because of a hidden
// path component or a missing embed directive.
func TestBuiltinSkillsEmbedMatchesDiskSourceOfTruth(t *testing.T) {
	diskFiles := map[string]bool{}
	walkErr := filepath.WalkDir("agent/skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Normalize to forward slashes to match embed FS paths.
		clean := filepath.ToSlash(path)
		diskFiles[clean] = true
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk disk agent/skills: %v", walkErr)
	}
	if len(diskFiles) == 0 {
		t.Fatal("no files found on disk under agent/skills; test setup is wrong")
	}

	embedFiles := map[string]bool{}
	embedErr := fs.WalkDir(BuiltinSkillsFS, "agent/skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		embedFiles[path] = true
		return nil
	})
	if embedErr != nil {
		t.Fatalf("walk BuiltinSkillsFS: %v", embedErr)
	}

	var missing []string
	for path := range diskFiles {
		if !embedFiles[path] {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	for _, path := range missing {
		t.Errorf("file on disk %q is missing from the embed (likely dropped by a hidden _/.-prefixed path component)", path)
	}
	if len(missing) > 0 {
		t.Logf("disk has %d files, embed has %d files", len(diskFiles), len(embedFiles))
	}
}
