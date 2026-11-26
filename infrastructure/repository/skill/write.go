package reposkill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// writeNameClass is the filesystem boundary check: a learned skill's name
// becomes a directory, and only the class charset can never escape its root.
// It is boundary validation, not the domain rule — the domain owns what a
// skill name means; this check only protects the path join.
var writeNameClass = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// renderSkill is the one place a SKILL.md is born: YAML frontmatter carrying
// only name and description — the file stays portable, ownership lives in the
// database — then the body. frontmatter() parses it back, so writer and
// reader cannot drift apart.
func renderSkill(doc drepo.SkillDocument) (string, error) {
	meta := struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}{doc.Name, doc.Description}
	raw, err := yaml.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("render frontmatter: %w", err)
	}
	body := doc.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return "---\n" + string(raw) + "---\n\n" + body, nil
}

// Write renders the document and publishes it atomically in the nusashell
// root: temp file, chmod, rename, so a reader never sees a partial skill. The
// compare-and-swap contract is the port's: create-only on an empty
// expectDigest, replace only while the disk digest matches a non-empty one,
// and either way a file already holding the exact rendered bytes is a
// successful no-op — the retry path of an accept whose commit failed.
//
// The digest check and the rename are not one filesystem transaction; a
// concurrent editor slipping into that gap is the race the design accepts,
// because the flip-to-user observation heals it on the next read.
func (s *Local) Write(ctx context.Context, root enum.SkillRoot, doc drepo.SkillDocument, expectDigest string) (drepo.SkillWritten, error) {
	written := drepo.SkillWritten{}
	if root != enum.SkillRootNusashell {
		return written, apperror.New(400, "learned skills live in the nusashell root, not "+string(root), nil)
	}
	if s.nusashell == "" {
		return written, apperror.New(400, "no nusashell root is configured", nil)
	}
	if !writeNameClass.MatchString(doc.Name) {
		return written, apperror.New(400, "the skill's name is not a class-level filesystem name", nil)
	}
	if err := ctx.Err(); err != nil {
		return written, err
	}
	rendered, err := renderSkill(doc)
	if err != nil {
		return written, err
	}
	digest := valueobject.SkillDigest(rendered)
	dir := filepath.Join(s.nusashell, doc.Name)
	path := filepath.Join(dir, "SKILL.md")
	written = drepo.SkillWritten{Path: path, Digest: digest}

	current, readErr := readSkillFile(path)
	exists := readErr == nil
	if !exists && !os.IsNotExist(readErr) {
		// A file that is present but unreadable — permissions, a non-regular
		// file, over the size cap — is not absence. Treating it as absent
		// would let a create-only write rename straight over it, because
		// rename needs write permission on the directory, not the file.
		// Refusing is the feature's core promise: never clobber what is there.
		return written, apperror.New(500, "the skill file at the target path cannot be read, so it cannot be compared", readErr)
	}
	if exists && valueobject.SkillDigest(string(current)) == digest {
		return written, nil
	}
	if expectDigest == "" {
		if exists {
			return written, apperror.New(409, "the skill "+doc.Name+" already exists on disk", nil)
		}
	} else if !exists {
		return written, apperror.New(409, "the skill "+doc.Name+" is no longer on disk", nil)
	} else if valueobject.SkillDigest(string(current)) != expectDigest {
		return written, apperror.New(409, "the skill "+doc.Name+" changed since it was read", nil)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return written, err
	}
	tmp, err := os.CreateTemp(dir, ".skill-*")
	if err != nil {
		return written, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(rendered); err != nil {
		tmp.Close()
		return written, err
	}
	if err := tmp.Close(); err != nil {
		return written, err
	}
	if err := os.Chmod(tmp.Name(), 0644); err != nil {
		return written, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return written, err
	}
	return written, nil
}

// Archive moves a skill's directory under .archive/ inside the same root,
// where List cannot see it: the .archive directory itself holds no SKILL.md,
// so discovery skips it. The archived directory keeps a timestamp suffix so
// one name can be archived more than once without losing either copy.
func (s *Local) Archive(ctx context.Context, root enum.SkillRoot, name string) error {
	if root != enum.SkillRootNusashell {
		return apperror.New(400, "learned skills live in the nusashell root, not "+string(root), nil)
	}
	if s.nusashell == "" {
		return apperror.New(400, "no nusashell root is configured", nil)
	}
	if !writeNameClass.MatchString(name) {
		return apperror.New(400, "the skill's name is not a class-level filesystem name", nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Join(s.nusashell, name)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		if os.IsNotExist(err) {
			return apperror.NewNotFoundError("the skill "+name, nil)
		}
		return err
	}
	archive := filepath.Join(s.nusashell, ".archive")
	if err := os.MkdirAll(archive, 0755); err != nil {
		return err
	}
	dest := filepath.Join(archive, fmt.Sprintf("%s-%d", name, time.Now().UnixNano()))
	return os.Rename(dir, dest)
}
