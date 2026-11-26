//go:build linux

package system

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A unit the installer owns carries nothing extra: rewriting it loses nothing,
// so nothing is reported.
func TestExtraUnitLinesFindsNothingOnItsOwnUnit(t *testing.T) {
	unit := "[Unit]\nDescription=NusaShell\n\n[Service]\nExecStart=/usr/bin/nusashell serve\n"
	if lost := extraUnitLines(unit, unit); len(lost) != 0 {
		t.Fatalf("an unchanged unit reported %v, want nothing", lost)
	}
}

// A line the owner added is named before the rewrite drops it.
func TestExtraUnitLinesNamesTheOwnersDirectives(t *testing.T) {
	rendered := "[Service]\nExecStart=/usr/bin/nusashell serve\n"
	existing := rendered + "MemoryHigh=8G\nMemoryMax=12G\n# my note\n"
	lost := extraUnitLines(existing, rendered)
	if len(lost) != 2 || lost[0] != "MemoryHigh=8G" || lost[1] != "MemoryMax=12G" {
		t.Fatalf("lost = %v, want the owner's two directives", lost)
	}
}

// A directive the template also writes is not the owner's, and reporting it
// would train everyone to ignore the warning.
func TestExtraUnitLinesIgnoresTheTemplatesOwnDirectives(t *testing.T) {
	rendered := "[Service]\nExecStart=/opt/nusashell serve\nRestart=on-failure\n"
	existing := "[Service]\nExecStart=/usr/bin/nusashell serve\nRestart=on-failure\n"
	lost := extraUnitLines(existing, rendered)
	if len(lost) != 1 || lost[0] != "ExecStart=/usr/bin/nusashell serve" {
		t.Fatalf("lost = %v, want only the line the template changed", lost)
	}
}

// The warning is only worth anything if Register actually emits it: the pure
// function was already covered, and a mutation that silenced the call left those
// tests green. This drives the real path with the unit directory pointed at a
// temp tree — the installed unit is never touched.
func TestRegisterWarnsAboutLinesItWillDrop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	s := systemd{}
	if err := s.Available(); err != nil {
		t.Skipf("no user systemd bus on this host: %v", err)
	}
	unitDir := filepath.Join(dir, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "nusashell-b077"
	unit := filepath.Join(unitDir, name+".service")
	if err := os.WriteFile(unit, []byte("[Service]\nExecStart=/old/nusashell serve\nMemoryHigh=8G\nMemoryMax=12G\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	// The unit lives in a temp tree systemd cannot see, so enable --now fails;
	// the warning is written before that, which is what this test is about.
	_ = s.Register(Spec{Name: name, Exec: "/bin/true", Args: []string{"serve"}, WorkingDir: t.TempDir()})

	out := buf.String()
	if !strings.Contains(out, "MemoryMax=12G") || !strings.Contains(out, "MemoryHigh=8G") {
		t.Fatalf("the register path warned about nothing: %q", out)
	}
	if !strings.Contains(out, ".d/50-local.conf") {
		t.Fatalf("the warning does not say where the lines belong: %q", out)
	}
}

// A unit with no lines of its own warns about nothing: the installer must stay
// quiet on the case it owns.
func TestRegisterStaysQuietOnItsOwnUnit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	s := systemd{}
	if err := s.Available(); err != nil {
		t.Skipf("no user systemd bus on this host: %v", err)
	}
	var buf strings.Builder
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	_ = s.Register(Spec{Name: "nusashell-b077b", Exec: "/bin/true", Args: []string{"serve"}, WorkingDir: t.TempDir()})
	if strings.Contains(buf.String(), "will remove") {
		t.Fatalf("a fresh install warned: %q", buf.String())
	}
}

// A drop-in is the owner's, and an install never touches it: the file must come
// back byte for byte — and the warning about the unit's own lines still fires,
// because an existing drop-in is not a reason to stay quiet.
func TestRegisterLeavesTheDropInAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	s := systemd{}
	if err := s.Available(); err != nil {
		t.Skipf("no user systemd bus on this host: %v", err)
	}
	name := "nusashell-b077c"
	unitDir := filepath.Join(dir, "systemd", "user")
	dropIn := filepath.Join(unitDir, name+".service.d")
	if err := os.MkdirAll(dropIn, 0o755); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dropIn, "50-local.conf")
	body := []byte("[Service]\nMemoryHigh=8G\nMemoryMax=12G\n# owner's cap, do not touch\n")
	if err := os.WriteFile(conf, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, name+".service"),
		[]byte("[Service]\nExecStart=/old/nusashell serve\nNice=5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	_ = s.Register(Spec{Name: name, Exec: "/bin/true", Args: []string{"serve"}, WorkingDir: t.TempDir()})

	after, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("the drop-in is gone: %v", err)
	}
	if string(after) != string(body) {
		t.Fatalf("the drop-in was rewritten:\n%s", after)
	}
	if !strings.Contains(buf.String(), "Nice=5") {
		t.Fatalf("an existing drop-in silenced the warning: %q", buf.String())
	}
}
