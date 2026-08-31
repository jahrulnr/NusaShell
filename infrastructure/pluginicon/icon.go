// Package pluginicon resolves plugin manifest icons for display, mirroring
// NusaShell Electron's icon-resolver + PNG data-URL loader:
//
//   - text/emoji icons (e.g. "📝", "N") pass through unchanged;
//   - http(s) URLs pass through (the browser loads them directly);
//   - file-style icons ("file://icon.png", "file:///abs/icon.png",
//     "./icon.png", "icon.png") resolve against the plugin directory and are
//     returned as a `data:image/png;base64,...` URL after PNG validation;
//   - anything that cannot be resolved to a valid PNG falls back to "🧩".
package pluginicon

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// MaxIconBytes is the largest icon that will be embedded, matching the
// Electron loader's 5 MiB limit.
const MaxIconBytes = 5 << 20

var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

var fileLikeRe = regexp.MustCompile(`\.[a-zA-Z0-9]{1,8}$`)

// FallbackIcon is returned when a file icon cannot be loaded or validated.
const FallbackIcon = "🧩"

// IsRemoteURL reports whether the icon is an http(s) URL that the browser
// can load directly.
func IsRemoteURL(icon string) bool {
	t := strings.ToLower(strings.TrimSpace(icon))
	return strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://")
}

// IsPNG reports whether data has the PNG magic signature.
func IsPNG(data []byte) bool {
	return len(data) >= len(pngSignature) && bytes.Equal(data[:len(pngSignature)], pngSignature)
}

// DataURL encodes PNG bytes as a data URL.
func DataURL(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

// IconPath returns the relative file path for a file-style icon
// ("file://icon.png", "./assets/icon.png", "icon.png"), or "" when the icon
// is text, an emoji, an http(s) URL, or an absolute file URL.
func IconPath(icon string) string {
	t := strings.TrimSpace(icon)
	if t == "" || IsRemoteURL(t) {
		return ""
	}
	if p, relative, ok := fileURLPath(t); ok {
		if !relative {
			return "" // absolute file URL — caller resolves or rejects
		}
		return path.Clean(p)
	}
	if looksLikeFilePath(t) {
		return path.Clean(t)
	}
	return ""
}

// ResolveLocal resolves an icon against a local plugin directory. Text and
// http(s) icons pass through; file-style icons become PNG data URLs; anything
// invalid falls back to FallbackIcon.
func ResolveLocal(icon, baseDir string) string {
	t := strings.TrimSpace(icon)
	if t == "" || IsRemoteURL(t) {
		return t
	}
	if p, relative, ok := fileURLPath(t); ok {
		if relative {
			return readIconDataURL(filepath.Join(baseDir, filepath.FromSlash(p)), baseDir)
		}
		return readIconDataURL(filepath.FromSlash(p), baseDir)
	}
	if looksLikeFilePath(t) {
		return readIconDataURL(filepath.Join(baseDir, filepath.FromSlash(t)), baseDir)
	}
	return t
}

// fileURLPath parses a file:// URL. The returned relative flag is true for
// the `file://icon.png` / `file://assets/icon.png` forms and false for
// `file:///abs/icon.png` / `file:///C:/icon.png`.
//
// Note: url.Parse treats `file://assets/icon.png` as host "assets" with
// path "/icon.png", which is not what the manifest means. Mirror Electron
// and slice the raw string after "file://" instead.
func fileURLPath(raw string) (string, bool, bool) {
	if !strings.HasPrefix(strings.ToLower(raw), "file://") {
		return "", false, false
	}
	rawPath := raw[len("file://"):]
	if rawPath == "" {
		return "", false, false
	}
	if !strings.HasPrefix(rawPath, "/") {
		return rawPath, true, true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, false
	}
	p := u.Path
	// Windows drive form: /C:/x → C:/x
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	if p == "" {
		return "", false, false
	}
	return p, false, true
}

func looksLikeFilePath(s string) bool {
	return fileLikeRe.MatchString(s) && !strings.ContainsAny(s, " \t\n")
}

func readIconDataURL(filePath, baseDir string) string {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return FallbackIcon
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return FallbackIcon
	}
	// The icon must live inside the plugin folder.
	if absPath != absBase && !strings.HasPrefix(absPath, absBase+string(filepath.Separator)) {
		return FallbackIcon
	}
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() || info.Size() > MaxIconBytes {
		return FallbackIcon
	}
	data, err := os.ReadFile(absPath)
	if err != nil || !IsPNG(data) {
		return FallbackIcon
	}
	return DataURL(data)
}
