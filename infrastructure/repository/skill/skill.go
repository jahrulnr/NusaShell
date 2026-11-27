// Package reposkill discovers filesystem skills and installs shipped assets.
package reposkill

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/goccy/go-yaml"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// skillFileCap bounds one SKILL.md — a skill is prompt prose, so a file
// past a megabyte is misconfigured, not documentation.
const skillFileCap = 1 << 20

// readSkillDir lists one skill root. Tests replace it to count how many
// times each root is read; production leaves it as os.ReadDir.
var readSkillDir = os.ReadDir

// readSkillFile reads one SKILL.md through a non-blocking open: a FIFO or
// device parked at the path cannot stall discovery the way os.ReadFile
// would, and the regular-file check plus size cap keep a stray special or
// giant file out of memory.
func readSkillFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > skillFileCap {
		return nil, fmt.Errorf("file exceeds %d bytes", skillFileCap)
	}
	raw, err := io.ReadAll(io.LimitReader(f, skillFileCap+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > skillFileCap {
		return nil, fmt.Errorf("file exceeds %d bytes", skillFileCap)
	}
	return raw, nil
}

type Local struct{ home, nusashell, builtin string }

// NewLocal discovers skills across the roots in precedence order: builtin,
// nusashell — the learned skills, empty while the feature is unwired — global,
// workspace. It also implements the writer port for the nusashell root: the
// same type reads and writes the format so the two cannot drift apart.
func NewLocal(home, nusashell, builtin string) *Local {
	return &Local{home: home, nusashell: nusashell, builtin: builtin}
}

func (s *Local) List(ctx context.Context, workspace string) (drepo.SkillCatalog, error) {
	catalog, _, err := s.ListAcross(ctx, workspace, nil)
	return catalog, err
}

// ListAcross reads builtin, nusashell, and global once, then focus, then
// each other workspace folder once. Stacking List per workspace would read
// the fixed roots again for every folder.
func (s *Local) ListAcross(ctx context.Context, focus string, others []string) (drepo.SkillCatalog, map[string]string, error) {
	catalog := drepo.SkillCatalog{Skills: []drepo.Skill{}}
	byName := map[string]drepo.Skill{}
	taken := map[string]string{}
	// Later roots replace earlier entries by declared name. The sources are
	// the enum values themselves: decide compares catalog rows against
	// enum.SkillRoot, and a literal drifting from it would fail closed
	// forever with no compile-time signal.
	apply := func(path, source string, intoCatalog bool) error {
		found, diags, err := scanSkillRoot(ctx, path, source)
		catalog.Diagnostics = append(catalog.Diagnostics, diags...)
		for _, skill := range found {
			if intoCatalog {
				byName[skill.Name] = skill
			}
			// taken prefers a holder outside nusashell. The loop writes only
			// nusashell, so a name another root holds is a collision whatever
			// nusashell holds under it; the catalog's own precedence (a learned
			// skill over a builtin one) must not hide that holder from taken.
			cur, held := taken[skill.Name]
			switch {
			case !held:
				taken[skill.Name] = source
			case source == string(enum.SkillRootNusashell):
				// a learned file never displaces a holder outside nusashell
			case cur == string(enum.SkillRootNusashell):
				taken[skill.Name] = source
			case intoCatalog:
				// later fixed roots replace earlier ones, as byName does
				taken[skill.Name] = source
			}
		}
		return err
	}
	roots := []struct{ path, source string }{{s.builtin, string(enum.SkillRootBuiltin)}}
	if s.nusashell != "" {
		roots = append(roots, struct{ path, source string }{s.nusashell, string(enum.SkillRootNusashell)})
	}
	if s.home != "" {
		roots = append(roots, struct{ path, source string }{filepath.Join(s.home, ".agents", "skills"), string(enum.SkillRootGlobal)})
	}
	for _, root := range roots {
		if err := apply(root.path, root.source, true); err != nil {
			return catalog, nil, err
		}
	}
	focus = cleanWorkspace(focus)
	if focus != "" {
		if err := apply(filepath.Join(focus, ".agents", "skills"), string(enum.SkillRootWorkspace), true); err != nil {
			return catalog, nil, err
		}
	}
	seen := map[string]bool{}
	if focus != "" {
		seen[focus] = true
	}
	rest := make([]string, 0, len(others))
	for _, other := range others {
		other = cleanWorkspace(other)
		if other == "" || seen[other] {
			continue
		}
		seen[other] = true
		rest = append(rest, other)
	}
	sort.Strings(rest)
	for _, other := range rest {
		if err := apply(filepath.Join(other, ".agents", "skills"), string(enum.SkillRootWorkspace), false); err != nil {
			return catalog, nil, err
		}
	}
	for _, skill := range byName {
		catalog.Skills = append(catalog.Skills, skill)
	}
	sort.Slice(catalog.Skills, func(i, j int) bool { return catalog.Skills[i].Name < catalog.Skills[j].Name })
	if err := ctx.Err(); err != nil {
		return catalog, nil, err
	}
	return catalog, taken, nil
}

func cleanWorkspace(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

// scanSkillRoot reads one root directory. An empty path is not a root. A
// missing directory is an empty root. A context cancellation returns the
// skills already accepted so the caller can hand back a partial catalog
// with the error, which is what List did before the read was shared.
func scanSkillRoot(ctx context.Context, path, source string) ([]drepo.Skill, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if path == "" {
		return nil, nil, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	dirs, err := readSkillDir(abs)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", abs, err)}, nil
	}
	var skills []drepo.Skill
	var diags []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return skills, diags, err
		}
		if !dir.IsDir() && dir.Type()&os.ModeSymlink == 0 {
			continue
		}
		skillPath := filepath.Join(abs, dir.Name(), "SKILL.md")
		raw, err := readSkillFile(skillPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			diags = append(diags, fmt.Sprintf("%s: %v", skillPath, err))
			continue
		}
		name, description, err := frontmatter(raw)
		if err != nil {
			diags = append(diags, fmt.Sprintf("%s: %v", skillPath, err))
			continue
		}
		if seen[name] {
			diags = append(diags, fmt.Sprintf("%s: duplicate skill name %q in %s", skillPath, name, abs))
			continue
		}
		seen[name] = true
		skills = append(skills, drepo.Skill{Name: name, Description: description, Path: skillPath, Source: source, Digest: valueobject.SkillDigest(string(raw))})
	}
	return skills, diags, nil
}

// frontmatter parses YAML rather than guessing at colon-separated lines, so
// folded descriptions, quoted names and optional nested metadata work.
func frontmatter(raw []byte) (string, string, error) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", "", fmt.Errorf("missing YAML frontmatter")
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] != "---" {
			continue
		}
		var meta struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:i], "\n")), &meta); err != nil {
			return "", "", fmt.Errorf("invalid frontmatter: %w", err)
		}
		name, description := strings.TrimSpace(meta.Name), strings.TrimSpace(meta.Description)
		if name == "" || description == "" {
			return "", "", fmt.Errorf("frontmatter requires name and description")
		}
		return name, description, nil
	}
	return "", "", fmt.Errorf("unclosed YAML frontmatter")
}

func (s *Local) Read(ctx context.Context, workspace, name string) (drepo.Skill, string, error) {
	catalog, err := s.List(ctx, workspace)
	if err != nil {
		return drepo.Skill{}, "", err
	}
	for _, skill := range catalog.Skills {
		if skill.Name != name {
			continue
		}
		if err := ctx.Err(); err != nil {
			return drepo.Skill{}, "", err
		}
		raw, err := readSkillFile(skill.Path)
		return skill, string(raw), err
	}
	return drepo.Skill{}, "", fmt.Errorf("unknown skill %q", name)
}

// InstallBuiltins materializes embedded assets in the application's own skill
// root. Shipped files follow the binary's contents: paths the binary no longer
// ships are pruned, and user roots are untouched.
func InstallBuiltins(dir string, assets fs.FS) error {
	shipped := map[string]bool{}
	if err := fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		shipped[path] = true
		raw, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		prior, err := readSkillFile(dest)
		if err == nil && bytes.Equal(prior, raw) {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		// Readers can discover skills while another engine bootstraps. Publish
		// each shipped file atomically so they never see a partial document.
		tmp, err := os.CreateTemp(filepath.Dir(dest), ".skill-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		_, writeErr := tmp.Write(raw)
		closeErr := tmp.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := os.Chmod(tmp.Name(), 0644); err != nil {
			return err
		}
		return os.Rename(tmp.Name(), dest)
	}); err != nil {
		return err
	}
	var dirs []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == dir {
			return nil
		}
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// Skip publish temps a concurrent bootstrap may still be writing.
		if !shipped[filepath.ToSlash(rel)] && !strings.HasPrefix(entry.Name(), ".skill-") {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, dir := range slices.Backward(dirs) {
		// Deepest-first so emptied parents collapse after their children.
		// Dirs still holding shipped files or anything unreadable stay.
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
			_ = os.Remove(dir)
		}
	}
	return nil
}
