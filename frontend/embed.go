// Package frontend embeds the native HTML/CSS/JS assets so the binary is
// self-contained. Development mode (NUSASHELL_DEV=1) serves the same tree
// from disk instead.
package frontend

import "embed"

//go:embed index.html styles fonts js vendor icons manifest.webmanifest nusashell-mark.png agent-offline-mascot.png sw.js
var FS embed.FS
