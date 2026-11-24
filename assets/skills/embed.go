// Package skills embeds NusaShell's builtin agent skills.
package skills

import (
	"embed"
	"io/fs"
)

//go:embed all:skill-creator all:tool-creator all:nusashell all:coding-workflow
var embedded embed.FS

// Builtins returns the embedded skill tree for application installation.
func Builtins() fs.FS { return embedded }
