//go:build windows

package tools

import (
	"os"
	"os/exec"
	"strings"
)

func applyPlatformAttrs(cmd *exec.Cmd) {
	// No special attributes: Process.Kill terminates the direct child, which
	// covers the shell wrapper. Tree-kill via Job Objects is a future
	// improvement once the functional baseline is proven.
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

const psEncodingPrefix = "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; "

func psArgs(command string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-Command", psEncodingPrefix + command}
}

// findBashWindows locates Git Bash in the standard Git for Windows install
// locations, then PATH.
func findBashWindows() string {
	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("bash.exe"); err == nil {
		return p
	}
	return ""
}

// shellCommand resolves the shell for Windows.
// Resolution order for ""/"auto": Git Bash → PowerShell → (never cmd
// automatically; cmd remains available explicitly).
// Explicit kinds: bash, powershell, pwsh, cmd, wsl.
// Note on wsl: the working directory is mapped by WSL itself (/mnt/…);
// older WSL versions may start in ~ instead when the cwd is a UNC path.
func shellCommand(kind, command string) (string, []string) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "cmd":
		return "cmd", []string{"/C", command}
	case "powershell":
		return "powershell", psArgs(command)
	case "pwsh":
		return "pwsh", psArgs(command)
	case "wsl":
		return "wsl", []string{"sh", "-c", command}
	default: // "", "auto", "bash", anything unknown
		if bash := findBashWindows(); bash != "" && pickAutoWindowsShell(true) == "bash" {
			return bash, []string{"-c", command}
		}
		return "powershell", psArgs(command)
	}
}
