package pet

import (
	"os/exec"
	"strings"
)

// graphicalEnvKeys are session variables the desktop pet needs. Pets require
// X11 (DISPLAY); without it the SDL window exits immediately after spawn.
var graphicalEnvKeys = []string{"DISPLAY", "XAUTHORITY"}

// lookupSystemdUserEnv loads DISPLAY/XAUTHORITY from the systemd user
// manager. Login services often start before the desktop imports these into
// the manager environment; a later lookup still succeeds. Overridable in tests.
var lookupSystemdUserEnv = func() map[string]string {
	out, err := exec.Command("systemctl", "--user", "show-environment").Output()
	if err != nil {
		return nil
	}
	return parseSystemdShowEnvironment(string(out))
}

func parseSystemdShowEnvironment(out string) map[string]string {
	got := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key == "" {
			continue
		}
		for _, want := range graphicalEnvKeys {
			if key == want && value != "" {
				got[key] = value
			}
		}
	}
	return got
}

// enrichGraphicalEnv copies missing DISPLAY/XAUTHORITY from lookup into
// environ so a systemd-supervised Go core can still spawn a visible pet
// after the desktop session has published those variables.
func enrichGraphicalEnv(environ []string, lookup func() map[string]string) []string {
	if envValue(environ, "DISPLAY") != "" {
		return environ
	}
	if lookup == nil {
		return environ
	}
	extra := lookup()
	if len(extra) == 0 {
		return environ
	}
	return mergeEnvIfMissing(environ, extra)
}

func envValue(environ []string, key string) string {
	prefix := key + "="
	for i := len(environ) - 1; i >= 0; i-- {
		if strings.HasPrefix(environ[i], prefix) {
			return strings.TrimPrefix(environ[i], prefix)
		}
	}
	return ""
}

func mergeEnvIfMissing(environ []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return environ
	}
	out := make([]string, 0, len(environ)+len(extra))
	present := make(map[string]bool, len(graphicalEnvKeys))
	for _, kv := range environ {
		key, _, ok := strings.Cut(kv, "=")
		if ok {
			for _, want := range graphicalEnvKeys {
				if key == want {
					present[key] = true
				}
			}
		}
		out = append(out, kv)
	}
	for _, key := range graphicalEnvKeys {
		if present[key] {
			continue
		}
		if value := extra[key]; value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}
