package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

// The child environment is an allowlist — the agent's terminal must not
// inherit the harness's provider keys — but the list has to describe both
// platforms. Windows stores its variables in whatever case they were created
// (a real os.Environ on Windows commonly holds `Path`, not `PATH`), so an
// exact-case match would drop PATH on the platform the list is meant to
// support, and a Unix-only list leaves the child without SYSTEMROOT or
// COMSPEC at all. Neither failure shows up at build time.
func TestFilterEnv(t *testing.T) {
	environ := []string{
		// Unix spellings.
		"PATH=/usr/local/bin:/usr/bin", "HOME=/home/owner", "SHELL=/bin/bash",
		"LANG=en_US.UTF-8", "LC_ALL=C", "TMPDIR=/tmp", "TZ=Asia/Jakarta",
		// Windows spellings, in the mixed case Windows actually reports.
		"Path=C:\\Windows\\system32", "SystemRoot=C:\\Windows",
		"ComSpec=C:\\Windows\\system32\\cmd.exe", "PATHEXT=.COM;.EXE;.CMD",
		"USERPROFILE=C:\\Users\\owner", "AppData=C:\\Users\\owner\\AppData\\Roaming",
		"TEMP=C:\\Users\\owner\\AppData\\Local\\Temp",
		// Secrets that must never reach an agent's child.
		"OPENAI_API_KEY=sk-live-secret", "TELEGRAM_BOT_TOKEN=123456:secret",
		"AWS_SECRET_ACCESS_KEY=secret", "NUSASHELL_SECRETS_KEY=k",
	}

	env := filterEnv(environ, nil)

	kept := []string{
		"PATH", "HOME", "SHELL", "LANG", "LC_ALL", "TMPDIR", "TZ",
		"PATHEXT", "USERPROFILE", "TEMP",
	}
	for _, key := range kept {
		if !envHas(env, key) {
			t.Errorf("want %s inherited, got %v", key, env)
		}
	}
	// The Windows entries keep the case they arrived in — rewriting them
	// would hand the child a variable the platform did not create.
	for _, want := range []string{"Path=C:\\Windows\\system32", "SystemRoot=C:\\Windows", "ComSpec=C:\\Windows\\system32\\cmd.exe"} {
		if !slicesContain(env, want) {
			t.Errorf("want %q preserved verbatim, got %v", want, env)
		}
	}
	for _, leaked := range []string{"OPENAI_API_KEY", "TELEGRAM_BOT_TOKEN", "AWS_SECRET_ACCESS_KEY", "NUSASHELL_SECRETS_KEY"} {
		if envHas(env, leaked) {
			t.Errorf("%s leaked into the child environment: %v", leaked, env)
		}
	}
}

// The agent may name variables for its own child; those are appended on top
// of the allowlist, and an unnamed entry is skipped rather than emitted as
// "=value".
func TestFilterEnvAppendsAgentVariables(t *testing.T) {
	env := filterEnv([]string{"PATH=/usr/bin"}, []acp.EnvVar{
		{Name: "AGENT_TOKEN", Value: "abc"},
		{Name: "", Value: "orphan"},
	})

	if !slicesContain(env, "AGENT_TOKEN=abc") {
		t.Fatalf("want AGENT_TOKEN appended, got %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "=") {
			t.Fatalf("unnamed variable emitted as %q", e)
		}
	}
}

// envHas reports a variable by case-insensitive name, matching how the
// platform treats the names it stores.
func envHas(env []string, key string) bool {
	for _, e := range env {
		if k, _, _ := strings.Cut(e, "="); strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

func slicesContain(env []string, want string) bool {
	return slices.Contains(env, want)
}
