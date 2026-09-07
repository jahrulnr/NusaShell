package service

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite golden fixture files")

var templateOptions = Options{
	BinaryPath: "/opt/nusashell/current/nusashell",
	DataDir:    "/home/test/.config/nusashell",
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "service", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	if got := NormalizeDefinition(got); got != NormalizeDefinition(string(want)) {
		t.Fatalf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

func TestSystemdUnitGolden(t *testing.T) {
	assertGolden(t, "nusashell.service.golden", SystemdUnit(templateOptions, ServiceEnv(templateOptions)))
}

func TestSystemdUnitPropagatesOverrides(t *testing.T) {
	opts := Options{
		BinaryPath:  "/opt/nusashell/current/nusashell",
		DataDir:     "/srv/nusashell",
		Host:        "0.0.0.0",
		Port:        "7777",
		AllowRemote: true,
	}
	unit := SystemdUnit(opts, ServiceEnv(opts))
	for _, want := range []string{
		`Environment="NUSASHELL_HOST=0.0.0.0"`,
		`Environment="NUSASHELL_PORT=7777"`,
		`Environment="NUSASHELL_ALLOW_REMOTE=1"`,
		"WorkingDirectory=/srv/nusashell",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestSystemdUnitQuotesExecStart(t *testing.T) {
	opts := Options{BinaryPath: `/opt/nu sa"shell/nusashell`, DataDir: templateOptions.DataDir}
	unit := SystemdUnit(opts, ServiceEnv(opts))
	if !strings.Contains(unit, `ExecStart="/opt/nu sa\"shell/nusashell"`) {
		t.Fatalf("ExecStart not escaped for systemd:\n%s", unit)
	}
}

func TestSystemdUnitRejectsMultilineEnv(t *testing.T) {
	opts := Options{BinaryPath: templateOptions.BinaryPath, DataDir: "a\nb"}
	if _, err := ServiceEnvChecked(opts); err == nil {
		t.Fatal("expected error for newline in data dir")
	}
}

func TestLaunchdPlistGolden(t *testing.T) {
	assertGolden(t, "id.nusashell.core.plist.golden", LaunchdPlist(templateOptions, ServiceEnv(templateOptions)))
}

func TestLaunchdPlistEscapesXML(t *testing.T) {
	opts := Options{BinaryPath: "/opt/nu&<s>\"'hell", DataDir: templateOptions.DataDir}
	plist := LaunchdPlist(opts, ServiceEnv(opts))
	for _, want := range []string{"nu&amp;&lt;s&gt;&quot;&apos;hell", "Label</key>"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "nu&<s>") {
		t.Fatalf("raw & leaked into plist:\n%s", plist)
	}
}

func TestWindowsTaskArtifactsGolden(t *testing.T) {
	opts := Options{
		BinaryPath: `C:\Programs\NusaShell\current\nusashell.exe`,
		DataDir:    `C:\Users\test\AppData\Roaming\nusashell`,
	}
	env := ServiceEnv(opts)
	task := WindowsTask{
		Name:         TaskName,
		User:         "",
		ScriptPath:   WindowsTaskScriptPath(opts.DataDir),
		LauncherPath: WindowsHiddenLauncherPath(opts.DataDir),
	}
	assertGolden(t, "nusashell-core-task.xml.golden", WindowsTaskXML(task))
	assertGolden(t, "nusashell-service.cmd.golden", WindowsTaskScript(opts, env))
	assertGolden(t, "nusashell-service.vbs.golden", WindowsHiddenLauncher(task))
}

func TestWindowsTaskScriptPropagatesCapturedPath(t *testing.T) {
	opts := Options{
		BinaryPath: `C:\Programs\NusaShell\current\nusashell.exe`,
		DataDir:    `C:\Users\test\AppData\Roaming\nusashell`,
		Path:       `C:\Users\test\bin;C:\Program Files\Go\bin`,
	}
	script := WindowsTaskScript(opts, ServiceEnv(opts))
	if !strings.Contains(script, `set "PATH=C:\Users\test\bin;C:\Program Files\Go\bin"`) {
		t.Fatalf("task script missing captured PATH:\n%s", script)
	}
}

func TestWindowsTaskScriptEscapesPercentInCapturedPath(t *testing.T) {
	opts := Options{
		BinaryPath: `C:\nusashell.exe`,
		DataDir:    `C:\data`,
		Path:       `%USERPROFILE%\bin`,
	}
	script := WindowsTaskScript(opts, ServiceEnv(opts))
	if !strings.Contains(script, `set "PATH=%%USERPROFILE%%\bin"`) {
		t.Fatalf("task script must preserve literal percent signs in PATH:\n%s", script)
	}
}

func TestWindowsTaskXMLScopesUser(t *testing.T) {
	task := WindowsTask{Name: TaskName, User: `BOX\tester`, ScriptPath: `C:\s.cmd`, LauncherPath: `C:\l.vbs`}
	xml := WindowsTaskXML(task)
	if !strings.Contains(xml, "<UserId>BOX\\tester</UserId>") {
		t.Fatalf("xml missing scoped user:\n%s", xml)
	}
	if strings.Contains(xml, "S-1-5-32-545") {
		t.Fatalf("group fallback must be absent when user is known:\n%s", xml)
	}
}

func TestWindowsTaskUserResolution(t *testing.T) {
	got := WindowsTaskUser(func(key string) string {
		switch key {
		case "USERNAME":
			return "tester"
		case "USERDOMAIN":
			return "BOX"
		}
		return ""
	})
	if got != `BOX\tester` {
		t.Fatalf("WindowsTaskUser = %q", got)
	}
	if got := WindowsTaskUser(func(string) string { return "" }); got != "" {
		t.Fatalf("no identity must resolve empty, got %q", got)
	}
}

func TestWindowsTaskScriptRejectsMultiline(t *testing.T) {
	opts := Options{BinaryPath: `C:\a\b.exe`, DataDir: "bad\nvalue"}
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for newline in data dir")
		}
	}()
	_ = WindowsTaskScript(opts, ServiceEnv(opts))
}

func TestWindowsUTF16LEBOM(t *testing.T) {
	got := WindowsUTF16LE("x")
	if !bytes.HasPrefix(got, []byte{0xff, 0xfe}) {
		t.Fatalf("missing UTF-16 LE BOM: % x", got[:2])
	}
	if got[2] != 'x' || got[3] != 0 {
		t.Fatalf("payload not little-endian: % x", got[2:])
	}
}

func TestNormalizeDefinition(t *testing.T) {
	a := "ExecStart=/x\nKeepAlive\n"
	b := "ExecStart=/x   \r\nKeepAlive\n\n\n"
	if NormalizeDefinition(a) != NormalizeDefinition(b) {
		t.Fatal("trailing whitespace and CRLF must not affect comparison")
	}
	if NormalizeDefinition(a) == NormalizeDefinition(a+"Restart=always\n") {
		t.Fatal("content changes must still be detected")
	}
}

func TestServiceEnvList(t *testing.T) {
	env := ServiceEnv(templateOptions)
	want := []string{
		"NUSASHELL_SERVICE=1",
		"NUSASHELL_DATA_DIR=" + templateOptions.DataDir,
	}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("env = %v, want %v", env, want)
	}
}

func TestServiceEnvCheckedPropagatesOverrides(t *testing.T) {
	opts := Options{BinaryPath: "/x", DataDir: "/d", Host: "127.0.0.1", Port: "10994", AllowRemote: true}
	env, _ := ServiceEnvChecked(opts)
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"NUSASHELL_SERVICE=1",
		"NUSASHELL_DATA_DIR=/d",
		"NUSASHELL_HOST=127.0.0.1",
		"NUSASHELL_PORT=10994",
		"NUSASHELL_ALLOW_REMOTE=1",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q: %v", want, env)
		}
	}
}
