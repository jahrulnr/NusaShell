package reposkill

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SweepTemps removes orphaned write temps under root. Write publishes a skill
// by creating a .skill-* file in the skill's directory and renaming it onto
// SKILL.md; a crash between the two leaves the temp behind. Directories,
// including .archive, and every other file stay.
func SweepTemps(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == ".archive" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".skill-") {
			return os.Remove(path)
		}
		return nil
	})
}
