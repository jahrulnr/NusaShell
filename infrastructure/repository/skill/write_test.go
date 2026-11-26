package reposkill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

func writeTestSkill(t *testing.T, root, name, description, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeTestDoc() drepo.SkillDocument {
	return drepo.SkillDocument{Name: "release-notes", Description: "Use when writing release notes", Body: "## Steps\n1. read the log\n2. group by area"}
}

func writeStatus(err error) int {
	if err == nil {
		return 0
	}
	return apperror.As(err).StatusCode()
}

func TestWriteRendersAReadableSkill(t *testing.T) {
	root := t.TempDir()
	store := NewLocal("", root, "")
	written, err := store.Write(context.Background(), enum.SkillRootNusashell, writeTestDoc(), "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(written.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// the digest covers exactly the rendered bytes — what a later read compares
	if written.Digest != valueobject.SkillDigest(string(raw)) {
		t.Errorf("digest %q does not cover the written bytes", written.Digest)
	}
	// and the file parses back through the same frontmatter reader
	name, description, err := frontmatter(raw)
	if err != nil {
		t.Fatalf("the written file does not parse back: %v", err)
	}
	if name != "release-notes" || description != "Use when writing release notes" {
		t.Errorf("round trip: %q %q", name, description)
	}
	// discovery sees it, in the nusashell root, carrying the same digest
	catalog, err := store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].Source != "nusashell" {
		t.Fatalf("the written skill is not visible: %+v", catalog.Skills)
	}
	if catalog.Skills[0].Digest != written.Digest {
		t.Errorf("catalog digest %q != written digest %q", catalog.Skills[0].Digest, written.Digest)
	}
}

func TestWriteDescriptionWithSpecialCharactersRoundTrips(t *testing.T) {
	root := t.TempDir()
	store := NewLocal("", root, "")
	doc := writeTestDoc()
	doc.Description = "Use when writing release notes: the owner said \"yes, exactly\""
	written, err := store.Write(context.Background(), enum.SkillRootNusashell, doc, "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(written.Path)
	_, description, err := frontmatter(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if description != doc.Description {
		t.Errorf("a description with colons and quotes did not round-trip: %q", description)
	}
}

func TestWriteCompareAndSwapAndIdempotency(t *testing.T) {
	root := t.TempDir()
	store := NewLocal("", root, "")
	ctx := context.Background()
	first, err := store.Write(ctx, enum.SkillRootNusashell, writeTestDoc(), "")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	// create over different content is refused
	other := writeTestDoc()
	other.Body += "\n3. changed"
	if _, err := store.Write(ctx, enum.SkillRootNusashell, other, ""); writeStatus(err) != 409 {
		t.Errorf("create over other content: %v; want 409", err)
	}
	// the same write is a successful no-op
	if _, err := store.Write(ctx, enum.SkillRootNusashell, writeTestDoc(), ""); err != nil {
		t.Errorf("idempotent rewrite refused: %v", err)
	}
	// replace with a stale digest is refused
	if _, err := store.Write(ctx, enum.SkillRootNusashell, other, "stale"); writeStatus(err) != 409 {
		t.Errorf("replace with stale digest: %v; want 409", err)
	}
	// replace against the current digest works
	if _, err := store.Write(ctx, enum.SkillRootNusashell, other, first.Digest); err != nil {
		t.Errorf("replace against current digest refused: %v", err)
	}
	// and its retry — the file already holding the new bytes — is a no-op even
	// though the expected digest is the old one
	if _, err := store.Write(ctx, enum.SkillRootNusashell, other, first.Digest); err != nil {
		t.Errorf("patch retry refused though the bytes are already there: %v", err)
	}
	// no partial files or temp files are left behind
	entries, _ := os.ReadDir(filepath.Join(root, "release-notes"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".skill-") {
			t.Errorf("a temp file survived: %s", e.Name())
		}
	}
}

func TestWriteRefusesBadInput(t *testing.T) {
	root := t.TempDir()
	store := NewLocal("", root, "")
	ctx := context.Background()
	if _, err := store.Write(ctx, enum.SkillRootGlobal, writeTestDoc(), ""); writeStatus(err) != 400 {
		t.Errorf("write into global: %v; want 400", err)
	}
	doc := writeTestDoc()
	doc.Name = "../escape"
	if _, err := store.Write(ctx, enum.SkillRootNusashell, doc, ""); writeStatus(err) != 400 {
		t.Errorf("a traversal name: %v; want 400", err)
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escape")); !os.IsNotExist(err) {
		t.Error("a traversal name reached the filesystem")
	}
	unwired := NewLocal("", "", "")
	if _, err := unwired.Write(ctx, enum.SkillRootNusashell, writeTestDoc(), ""); writeStatus(err) != 400 {
		t.Errorf("a write with no configured root: %v; want 400", err)
	}
}

func TestArchiveHidesTheSkillFromList(t *testing.T) {
	root := t.TempDir()
	store := NewLocal("", root, "")
	ctx := context.Background()
	if _, err := store.Write(ctx, enum.SkillRootNusashell, writeTestDoc(), ""); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := store.Archive(ctx, enum.SkillRootNusashell, "release-notes"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	catalog, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(catalog.Skills) != 0 {
		t.Errorf("an archived skill is still visible: %+v", catalog.Skills)
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".archive"))
	if len(entries) != 1 {
		t.Fatalf("the archive does not hold the skill: %+v", entries)
	}
	if writeStatus(store.Archive(ctx, enum.SkillRootNusashell, "release-notes")) != 404 {
		t.Error("archiving a missing skill did not report 404")
	}
	// archiving twice keeps both copies
	if _, err := store.Write(ctx, enum.SkillRootNusashell, writeTestDoc(), ""); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := store.Archive(ctx, enum.SkillRootNusashell, "release-notes"); err != nil {
		t.Fatalf("second archive: %v", err)
	}
	entries, _ = os.ReadDir(filepath.Join(root, ".archive"))
	if len(entries) != 2 {
		t.Errorf("the second archive lost a copy: %+v", entries)
	}
}

func TestListPrecedenceAcrossFourRoots(t *testing.T) {
	builtin, nusashell, home, workspace := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writeTestSkill(t, builtin, "shared", "from builtin", "body one")
	writeTestSkill(t, nusashell, "shared", "from nusashell", "body two")
	writeTestSkill(t, nusashell, "learned-only", "only in nusashell", "body three")
	writeTestSkill(t, filepath.Join(home, ".agents", "skills"), "shared", "from global", "body four")
	writeTestSkill(t, filepath.Join(workspace, ".agents", "skills"), "shared", "from workspace", "body five")
	store := NewLocal(home, nusashell, builtin)
	catalog, err := store.List(context.Background(), workspace)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	sources := map[string]string{}
	for _, s := range catalog.Skills {
		sources[s.Name] = s.Source
	}
	if sources["shared"] != "workspace" {
		t.Errorf("precedence: shared resolved to %q, want workspace", sources["shared"])
	}
	if sources["learned-only"] != "nusashell" {
		t.Errorf("the learned skill resolved to %q, want nusashell", sources["learned-only"])
	}
}

func TestListNusashellOverridesBuiltin(t *testing.T) {
	builtin, nusashell := t.TempDir(), t.TempDir()
	writeTestSkill(t, builtin, "shared", "from builtin", "body one")
	writeTestSkill(t, nusashell, "shared", "from nusashell", "body two")
	store := NewLocal("", nusashell, builtin)
	catalog, err := store.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(catalog.Skills) != 1 || catalog.Skills[0].Source != "nusashell" {
		t.Errorf("nusashell must override builtin: %+v", catalog.Skills)
	}
}

func TestWriteRefusesAnUnreadableTargetInsteadOfClobbering(t *testing.T) {
	root := t.TempDir()
	store := NewLocal("", root, "")
	ctx := context.Background()
	// a directory where the file should be is readable-as-nothing: readFile
	// fails, but not with not-exist — and a create must not rename over it
	dir := filepath.Join(root, "release-notes")
	if err := os.MkdirAll(filepath.Join(dir, "SKILL.md"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(ctx, enum.SkillRootNusashell, writeTestDoc(), ""); writeStatus(err) != 500 {
		t.Errorf("an unreadable target: %v; want 500 refusal", err)
	}
	// and whatever sits there is untouched
	if fi, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil || !fi.IsDir() {
		t.Error("the unreadable target was clobbered")
	}
	// a genuinely absent target still creates
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(ctx, enum.SkillRootNusashell, writeTestDoc(), ""); err != nil {
		t.Errorf("create after the obstruction is gone refused: %v", err)
	}
}
