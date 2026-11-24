// Package patharg turns a path as a model wrote it into the path a tool
// opens. Three shapes mean something other than the literal characters:
// a file: URL names a local file, a leading ~ means the owner's home, and
// on Windows %VAR% names an environment value. Everything else is literal
// — a name is a name.
//
// The package deliberately knows nothing about workspaces: callers expand
// first and then apply their own folder rule, so one expansion covers the
// file tools, the media tools and the HTTP preview alike.
package patharg

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// hostPaths is what expansion depends on: the host OS (the Windows %VAR%
// rule and its backslash), the owner's home directory, and the environment.
// Gathering the three in one value lets the tests drive the Windows rules
// from any runner instead of shipping an untested branch.
type hostPaths struct {
	goos string
	sep  string
	home string
	env  map[string]string // names upper-cased: Windows lookup ignores case
}

// Expand turns what a model wrote into the path a tool opens: a file: URL
// becomes the local path it names, a leading `~` (alone, `~/…` or `~\…`)
// becomes the owner's home, and on Windows `%VAR%` becomes that variable's
// value. Everything else stays literal — `$HOME` and `${HOME}` are names a
// model may mean to write, `~user` names a file too (this host has no
// other users to ask about), and `%APPDATA%` on Linux is a legal folder
// name.
//
// Expand runs before any workspace decision, so an expanded path meets
// exactly the same folder rule as any other path.
func Expand(path string) (string, error) { return expandWith(processHost(), path) }

// expandWith is Expand against host facts the caller supplies.
func expandWith(h hostPaths, path string) (string, error) {
	if path == "" {
		return path, nil
	}
	local, isFile, err := fileURLPath(h.goos, path)
	if err != nil {
		return "", err
	}
	if isFile {
		path = local
	}
	// Only a leading ~ is the home marker; a ~ elsewhere is part of a name.
	if strings.HasPrefix(path, "~") && (len(path) == 1 || path[1] == '/' || path[1] == '\\') {
		if h.home == "" {
			return "", fmt.Errorf(`cannot expand "~": the home directory is not known`)
		}
		rest := strings.TrimLeft(path[1:], `/\`)
		if rest == "" {
			return h.home, nil
		}
		return h.home + h.sep + rest, nil
	}
	if h.goos == "windows" {
		return expandEnvPercents(h, path)
	}
	return path, nil
}

// processHost reads the host facts Expand uses. The environment is only
// collected where the %VAR% rule applies.
func processHost() hostPaths {
	h := hostPaths{goos: runtime.GOOS, sep: string(filepath.Separator)}
	if home, err := os.UserHomeDir(); err == nil {
		h.home = strings.TrimRight(home, `/\`)
	}
	if h.goos == "windows" {
		h.env = map[string]string{}
		for _, kv := range os.Environ() {
			if name, value, ok := strings.Cut(kv, "="); ok {
				h.env[strings.ToUpper(name)] = value
			}
		}
	}
	return h
}

// fileURLPath answers the local path a file: URL names. A host other than
// empty or localhost is refused — these tools open this machine. The slash
// before a drive letter (file:///C:/…) is Windows-only syntax: on any other
// host "/C:/…" is a real, odd path and is left alone.
func fileURLPath(goos, path string) (string, bool, error) {
	if len(path) < len("file:") || !strings.EqualFold(path[:len("file:")], "file:") {
		return path, false, nil
	}
	rest := path[len("file:"):]
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		cut := strings.IndexAny(rest, `/\`)
		if cut < 0 {
			return "", true, fmt.Errorf("file URL %q names no path", path)
		}
		if authority := rest[:cut]; authority != "" && !strings.EqualFold(authority, "localhost") {
			return "", true, fmt.Errorf("file URL %q is not a local path", path)
		}
		rest = rest[cut:]
	}
	if rest == "" {
		return "", true, fmt.Errorf("file URL %q names no path", path)
	}
	// Markdown destinations are URLs, so an agent encodes spaces; an
	// unparsable escape is a filename character, not an error.
	if decoded, err := url.PathUnescape(rest); err == nil {
		rest = decoded
	}
	if goos == "windows" && len(rest) > 2 && rest[0] == '/' && rest[2] == ':' && isASCIILetter(rest[1]) {
		rest = rest[1:]
	}
	return rest, true, nil
}

// expandEnvPercents replaces Windows %VAR% references. An unset variable is
// an error naming it rather than a literal folder: `%APPDATA%\x` silently
// becoming a folder called `%APPDATA%` is the same bug class as `~` turning
// into `./~`. A % that encloses no plausible variable name stays literal,
// so a file called "50% off" survives.
func expandEnvPercents(h hostPaths, path string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(path); {
		if path[i] != '%' {
			b.WriteByte(path[i])
			i++
			continue
		}
		end := strings.IndexByte(path[i+1:], '%')
		if end < 0 {
			b.WriteString(path[i:])
			break
		}
		name := path[i+1 : i+1+end]
		if name == "" { // %% is a literal percent
			b.WriteByte('%')
			i += 2
			continue
		}
		if !envName(name) {
			b.WriteByte('%')
			i++
			continue
		}
		value, ok := h.env[strings.ToUpper(name)]
		if !ok {
			return "", fmt.Errorf("cannot expand %%%s%%: the environment variable is not set", name)
		}
		b.WriteString(value)
		i += end + 2
	}
	return b.String(), nil
}

// envName reports whether name has the shape of a Windows environment
// variable — %ProgramFiles(x86)% included.
func envName(name string) bool {
	for i, c := range name {
		switch {
		case c == '_' || c == '(' || c == ')':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		default:
			return false
		}
	}
	return name != ""
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
