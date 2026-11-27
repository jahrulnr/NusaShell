package reposkill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func writeSkill(t *testing.T, root, dir, text string) string {
	t.Helper()
	path := filepath.Join(root, dir, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoveryPrecedenceRefreshAndRead(t *testing.T) {
	home, workspace, builtin := t.TempDir(), t.TempDir(), t.TempDir()
	global := filepath.Join(home, ".agents", "skills")
	local := filepath.Join(workspace, ".agents", "skills")
	writeSkill(t, builtin, "base", "---\nname: base\ndescription: Built in\n---\nbase body")
	writeSkill(t, builtin, "shared", "---\nname: shared\ndescription: Built in shared\n---\n")
	writeSkill(t, global, "shared", "---\nname: shared\ndescription: Global\n---\nglobal body")
	text := "---\r\nname: shared\r\ndescription: >-\r\n  Workspace skill\r\n  with folded YAML.\r\nmetadata:\r\n  short-description: useful\r\n---\r\nraw body\r\n"
	path := writeSkill(t, local, "shared", text)
	store := NewLocal(home, "", builtin)
	catalog, err := store.List(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 2 || len(catalog.Diagnostics) != 0 {
		t.Fatalf("catalog = %+v", catalog)
	}
	if s := catalog.Skills[1]; s.Name != "shared" || s.Source != "workspace" || s.Path != path || s.Description != "Workspace skill with folded YAML." {
		t.Fatalf("skill = %+v", s)
	}
	skill, raw, err := store.Read(t.Context(), workspace, "shared")
	if err != nil || raw != text || skill.Path != path {
		t.Fatalf("read = %+v %q %v", skill, raw, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	skill, raw, err = store.Read(t.Context(), workspace, "shared")
	if err != nil || skill.Source != "global" || !strings.Contains(raw, "global body") {
		t.Fatalf("refresh = %+v %q %v", skill, raw, err)
	}
	if _, _, err := store.Read(t.Context(), workspace, "unknown"); err == nil {
		t.Fatal("unknown skill accepted")
	}
}

func TestMissingRootsMalformedMetadataAndCancellation(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	store := NewLocal(home, "", filepath.Join(t.TempDir(), "missing"))
	catalog, err := store.List(t.Context(), workspace)
	if err != nil || len(catalog.Skills) != 0 || len(catalog.Diagnostics) != 0 {
		t.Fatalf("missing roots = %+v %v", catalog, err)
	}
	root := filepath.Join(workspace, ".agents", "skills")
	for name, text := range map[string]string{
		"no-header":      "# plain markdown",
		"no-description": "---\nname: incomplete\n---\nbody",
		"bad-yaml":       "---\nname: [\n---\nbody",
		"unclosed":       "---\nname: open\ndescription: open",
	} {
		writeSkill(t, root, name, text)
	}
	catalog, err = store.List(t.Context(), workspace)
	if err != nil || len(catalog.Skills) != 0 || len(catalog.Diagnostics) != 4 {
		t.Fatalf("malformed = %+v %v", catalog, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.List(ctx, workspace); err != context.Canceled {
		t.Fatalf("cancel = %v", err)
	}
}

// A giant SKILL.md is misconfiguration, not documentation — discovery
// must report it instead of reading an unbounded file into memory.
func TestDiscoveryRejectsOversizedSkill(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	path := writeSkill(t, root, "huge", "---\nname: huge\ndescription: Huge\n---\n")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(skillFileCap + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	catalog, err := NewLocal("", "", "").List(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Skills) != 0 || len(catalog.Diagnostics) != 1 {
		t.Fatalf("oversized skill = %+v", catalog)
	}
	if !strings.Contains(catalog.Diagnostics[0], "exceeds") {
		t.Fatalf("diagnostic = %q, want the size cap named", catalog.Diagnostics[0])
	}
}

func TestInstallBuiltinsAndSymlinkDiscovery(t *testing.T) {
	home, builtin := t.TempDir(), t.TempDir()
	assets := fstest.MapFS{"creator/SKILL.md": {Data: []byte("---\nname: creator\ndescription: Create things\n---\nbody")}}
	if err := InstallBuiltins(builtin, assets); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(builtin, "removed", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(stale), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(builtin, ".skill-123"), []byte("temp"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := InstallBuiltins(builtin, assets); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("unshipped builtin file was not pruned")
	}
	if _, err := os.Stat(filepath.Dir(stale)); !os.IsNotExist(err) {
		t.Fatal("emptied builtin directory was not pruned")
	}
	if _, err := os.Stat(filepath.Join(builtin, ".skill-123")); err != nil {
		t.Fatal("in-flight publish temp was pruned")
	}
	root := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(builtin, "creator"), filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	catalog, err := NewLocal(home, "", builtin).List(t.Context(), "")
	if err != nil || len(catalog.Skills) != 1 || catalog.Skills[0].Source != "global" {
		t.Fatalf("symlink = %+v %v", catalog, err)
	}
}

// taken names the holder outside nusashell when there is one. The loop writes
// only nusashell, so a learned file over a builtin, or under another workspace
// folder's skill, must not hide that holder from the create check, while the
// focus catalog still shows the learned skill.
func TestListAcrossTakenNamesTheHolderOutsideNusashell(t *testing.T) {
	home, builtin, nusashell := t.TempDir(), t.TempDir(), t.TempDir()
	writeSkill(t, builtin, "shared", "---\nname: shared\ndescription: Built in\n---\n")
	writeSkill(t, nusashell, "shared", "---\nname: shared\ndescription: Learned\n---\n")
	writeSkill(t, nusashell, "pulled", "---\nname: pulled\ndescription: Learned\n---\n")
	other := t.TempDir()
	writeSkill(t, filepath.Join(other, ".agents", "skills"), "pulled", "---\nname: pulled\ndescription: Other folder\n---\n")
	focus := t.TempDir()

	store := NewLocal(home, nusashell, builtin)
	catalog, taken, err := store.ListAcross(t.Context(), focus, []string{other})
	if err != nil {
		t.Fatal(err)
	}
	effective := map[string]string{}
	for _, skill := range catalog.Skills {
		effective[skill.Name] = skill.Source
	}
	if effective["shared"] != "nusashell" || effective["pulled"] != "nusashell" {
		t.Fatalf("the focus catalog must show the learned skills: %v", effective)
	}
	if taken["shared"] != "builtin" {
		t.Errorf("taken[shared] = %q; a builtin holds the name, so taken names builtin", taken["shared"])
	}
	if taken["pulled"] != "workspace" {
		t.Errorf("taken[pulled] = %q; another workspace folder holds the name, so taken names workspace", taken["pulled"])
	}
}

func countReads(reads []string, path string) int {
	n := 0
	abs, err := filepath.Abs(path)
	if err != nil {
		return -1
	}
	for _, read := range reads {
		if read == abs {
			n++
		}
	}
	return n
}

// ListAcross reads the fixed roots once however many workspace folders it is
// handed. List stacked once per folder reads them again every time.
func TestListAcrossReadsFixedRootsOnce(t *testing.T) {
	home, builtin, nusashell := t.TempDir(), t.TempDir(), t.TempDir()
	global := filepath.Join(home, ".agents", "skills")
	writeSkill(t, builtin, "base", "---\nname: base\ndescription: Built in\n---\n")
	writeSkill(t, nusashell, "learned", "---\nname: learned\ndescription: Learned\n---\n")
	writeSkill(t, global, "global-only", "---\nname: global-only\ndescription: Global\n---\n")
	focus := t.TempDir()
	writeSkill(t, filepath.Join(focus, ".agents", "skills"), "focus-only", "---\nname: focus-only\ndescription: Focus\n---\n")
	writeSkill(t, filepath.Join(focus, ".agents", "skills"), "base", "---\nname: base\ndescription: Focus wins\n---\n")
	others := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		dir := t.TempDir()
		writeSkill(t, filepath.Join(dir, ".agents", "skills"), fmt.Sprintf("other-%d", i), fmt.Sprintf("---\nname: other-%d\ndescription: Other\n---\n", i))
		others = append(others, dir)
	}

	var reads []string
	orig := readSkillDir
	readSkillDir = func(name string) ([]os.DirEntry, error) {
		reads = append(reads, name)
		return orig(name)
	}
	t.Cleanup(func() { readSkillDir = orig })

	store := NewLocal(home, nusashell, builtin)
	catalog, taken, err := store.ListAcross(t.Context(), focus, others)
	if err != nil {
		t.Fatal(err)
	}
	fixed := []string{builtin, nusashell, global}
	for _, path := range fixed {
		if n := countReads(reads, path); n != 1 {
			t.Fatalf("fixed root %s read %d times, want 1 (total reads %d)", path, n, len(reads))
		}
	}
	if n := countReads(reads, filepath.Join(focus, ".agents", "skills")); n != 1 {
		t.Fatalf("focus read %d times, want 1", n)
	}
	// 3 fixed roots + focus + 8 other workspaces.
	if len(reads) != 12 {
		t.Fatalf("ListAcross reads = %d, want 12", len(reads))
	}
	for _, skill := range catalog.Skills {
		if strings.HasPrefix(skill.Name, "other-") {
			t.Fatalf("another workspace's skill %s entered the focus catalog", skill.Name)
		}
	}
	var base string
	for _, skill := range catalog.Skills {
		if skill.Name == "base" {
			base = skill.Description
		}
	}
	if base != "Focus wins" {
		t.Fatalf("focus did not override builtin: %q", base)
	}
	if taken["other-0"] != "workspace" || taken["learned"] != "nusashell" || taken["global-only"] != "global" {
		t.Fatalf("taken = %v", taken)
	}
	t.Logf("ListAcross: 3 fixed + 9 workspace = %d directory reads", len(reads))

	reads = nil
	workspaces := append([]string{focus}, others...)
	for _, workspace := range workspaces {
		if _, err := store.List(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range fixed {
		if n := countReads(reads, path); n != len(workspaces) {
			t.Fatalf("stacked List read %s %d times, want %d", path, n, len(workspaces))
		}
	}
	t.Logf("stacked List x %d: %d directory reads, fixed roots repeated %d times", len(workspaces), len(reads), len(workspaces))
}
