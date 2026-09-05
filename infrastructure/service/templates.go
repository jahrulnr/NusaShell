package service

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf16"
)

// SystemdUnit renders the systemd user unit for the core service. Secrets
// are not embedded here: the unit only carries non-secret configuration and
// the supervised marker. RestartPreventExitStatus is intentionally absent —
// NusaShell has no fatal-config exit code contract yet, and StartLimitBurst
// already bounds a failing server to five attempts per minute.
func SystemdUnit(opts Options, env []string) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=NusaShell core service\n")
	b.WriteString("After=network-online.target\n")
	b.WriteString("Wants=network-online.target\n")
	b.WriteString("StartLimitBurst=5\n")
	b.WriteString("StartLimitIntervalSec=60\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("ExecStart=" + systemdQuote(opts.BinaryPath) + "\n")
	b.WriteString("WorkingDirectory=" + opts.DataDir + "\n")
	for _, line := range systemdEnvLines(env) {
		b.WriteString(line + "\n")
	}
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=5\n")
	b.WriteString("TimeoutStopSec=30\n")
	b.WriteString("TimeoutStartSec=30\n")
	b.WriteString("SuccessExitStatus=0 143\n")
	b.WriteString("KillMode=control-group\n\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

// systemdQuote escapes a value for systemd ExecStart/Environment directives:
// one backslash is consumed before the next character, so backslashes and
// quotes must both be escaped for byte-for-byte round-trips.
func systemdQuote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func systemdEnvLines(env []string) []string {
	lines := make([]string, 0, len(env))
	for _, pair := range env {
		_, value, found := strings.Cut(pair, "=")
		if !found || value == "" {
			continue
		}
		lines = append(lines, `Environment=`+systemdQuote(pair))
	}
	return lines
}

// LaunchdPlist renders the macOS LaunchAgent for the core service. KeepAlive
// mirrors systemd Restart=always; ThrottleInterval keeps launchd's default
// spawn throttle explicit; Umask 077 (decimal 63) keeps created files private.
func LaunchdPlist(opts Options, env []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("  <dict>\n")
	b.WriteString("    <key>Label</key>\n    <string>" + xmlEscape(LaunchdLabel) + "</string>\n")
	b.WriteString("    <key>ProgramArguments</key>\n    <array>\n")
	b.WriteString("      <string>" + xmlEscape(opts.BinaryPath) + "</string>\n")
	b.WriteString("    </array>\n")
	b.WriteString("    <key>WorkingDirectory</key>\n    <string>" + xmlEscape(opts.DataDir) + "</string>\n")
	b.WriteString("    <key>RunAtLoad</key>\n    <true/>\n")
	b.WriteString("    <key>KeepAlive</key>\n    <true/>\n")
	b.WriteString("    <key>ThrottleInterval</key>\n    <integer>10</integer>\n")
	b.WriteString("    <key>ExitTimeOut</key>\n    <integer>20</integer>\n")
	b.WriteString("    <key>ProcessType</key>\n    <string>Background</string>\n")
	b.WriteString("    <key>Umask</key>\n    <integer>63</integer>\n")
	if entries := envDict(env); len(entries) > 0 {
		b.WriteString("    <key>EnvironmentVariables</key>\n    <dict>\n")
		for _, entry := range entries {
			b.WriteString("      <key>" + xmlEscape(entry[0]) + "</key>\n")
			b.WriteString("      <string>" + xmlEscape(entry[1]) + "</string>\n")
		}
		b.WriteString("    </dict>\n")
	}
	b.WriteString("    <key>StandardInPath</key>\n    <string>/dev/null</string>\n")
	b.WriteString("    <key>StandardOutPath</key>\n    <string>" + xmlEscape(unixLogPath(opts.DataDir, "service-stdout.log")) + "</string>\n")
	b.WriteString("    <key>StandardErrorPath</key>\n    <string>" + xmlEscape(unixLogPath(opts.DataDir, "service-stderr.log")) + "</string>\n")
	b.WriteString("  </dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

// envDict returns sorted KEY/VALUE pairs; empty values are skipped because
// plist dictionary values render as raw strings.
func envDict(env []string) [][2]string {
	entries := make([][2]string, 0, len(env))
	for _, pair := range env {
		key, value, found := strings.Cut(pair, "=")
		if !found || value == "" {
			continue
		}
		entries = append(entries, [2]string{key, value})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i][0] < entries[j][0] })
	return entries
}

// WindowsTask describes the artifacts behind the Scheduled Task: the .cmd run
// script and the .vbs hidden launcher the task actually executes.
type WindowsTask struct {
	Name         string
	User         string // DOMAIN\user; empty falls back to the Users group
	ScriptPath   string
	LauncherPath string
}

// WindowsTaskXML renders the Scheduled Task definition consumed by
// `schtasks /Create /XML`. XML is required because the CLI form cannot
// disable battery-stop defaults (the task would die when a laptop unplugs)
// nor express restart-on-failure. The file must be written UTF-16 LE with a
// BOM (see WindowsUTF16LE).
func WindowsTaskXML(task WindowsTask) string {
	principal := "\n      <GroupId>S-1-5-32-545</GroupId>"
	triggerUser := ""
	if task.User != "" {
		principal = "\n      <UserId>" + xmlEscape(task.User) + "</UserId>\n      <LogonType>InteractiveToken</LogonType>"
		triggerUser = "\n        <UserId>" + xmlEscape(task.User) + "</UserId>"
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>` + xmlEscape("NusaShell core service") + `</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>` + triggerUser + `
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">` + principal + `
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlEscape(task.LauncherPath) + `</Command>
    </Exec>
  </Actions>
</Task>`
}

// WindowsTaskScript renders the .cmd wrapper: it moves to the data dir,
// exports the service environment, then runs the binary with output appended
// to the service log. CRLF-terminated; cmd.exe is the parser.
func WindowsTaskScript(opts Options, env []string) string {
	assertNoCmdLineBreak(opts.DataDir, "task script working directory")
	var lines []string
	lines = append(lines, "@echo off", "rem NusaShell core service")
	lines = append(lines, `cd /d `+quoteCmdScriptArg(opts.DataDir))
	for _, pair := range env {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "PATH" || value == "" {
			continue
		}
		assertNoCmdLineBreak(value, "task script environment")
		lines = append(lines, `set "`+key+`=`+value+`"`)
	}
	assertNoCmdLineBreak(opts.BinaryPath, "task script binary")
	logPath := windowsLogPath(opts.DataDir, "service.log")
	lines = append(lines, quoteCmdScriptArg(opts.BinaryPath)+` >> `+quoteCmdScriptArg(logPath)+` 2>&1`)
	return strings.Join(lines, "\r\n") + "\r\n"
}

// WindowsHiddenLauncher renders the .vbs launcher so the task runs without a
// console window. VBS doubles embedded double quotes.
func WindowsHiddenLauncher(task WindowsTask) string {
	assertNoCmdLineBreak(task.ScriptPath, "hidden launcher script path")
	lines := []string{
		"' NusaShell core service",
		`CreateObject("WScript.Shell").Run ` + quoteVBSString(`"`+task.ScriptPath+`"`),
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

func quoteCmdScriptArg(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteVBSString(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func assertNoCmdLineBreak(value, label string) {
	if strings.ContainsAny(value, "\r\n") {
		panic(fmt.Sprintf("%s cannot contain CR or LF: %q", label, value))
	}
}

// WindowsTaskScriptPath returns the .cmd wrapper path under the service dir.
func WindowsTaskScriptPath(dataDir string) string {
	return windowsServicePath(dataDir, "nusashell-service.cmd")
}

// WindowsHiddenLauncherPath returns the .vbs launcher path under the service
// dir; this is the command the Scheduled Task executes.
func WindowsHiddenLauncherPath(dataDir string) string {
	return windowsServicePath(dataDir, "nusashell-service.vbs")
}

// WindowsTaskLogPath returns the service log file under the data dir.
func WindowsTaskLogPath(dataDir string) string {
	return windowsLogPath(dataDir, "service.log")
}

// windowsServicePath joins with Windows separators regardless of the
// building OS so generated artifacts stay byte-stable on any host.
func windowsServicePath(dataDir, name string) string {
	return winJoin(dataDir, "service", name)
}

func windowsLogPath(dataDir, name string) string {
	return winJoin(dataDir, "logs", name)
}

// unixLogPath joins with POSIX separators for launchd log paths.
func unixLogPath(dataDir, name string) string {
	return path.Join(dataDir, "logs", name)
}

func winJoin(parts ...string) string {
	return strings.ReplaceAll(path.Join(parts...), "/", `\`)
}

// WindowsTaskUser resolves the DOMAIN\user identity for the task principal.
// Without a resolvable identity the task is scoped to the built-in Users
// group instead.
func WindowsTaskUser(getenv func(string) string) string {
	username := firstNonEmpty(getenv("USERNAME"), getenv("USER"), getenv("LOGNAME"))
	if username == "" {
		return ""
	}
	if strings.Contains(username, `\`) {
		return username
	}
	domain := getenv("USERDOMAIN")
	if domain == "" || strings.EqualFold(domain, "workgroup") {
		return username
	}
	return domain + `\` + username
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// WindowsUTF16LE encodes text as UTF-16 little-endian with the BOM that
// Task Scheduler's /XML import requires on every locale.
func WindowsUTF16LE(text string) []byte {
	units := utf16.Encode([]rune(text))
	out := make([]byte, 2, 2+2*len(units))
	out[0] = 0xff
	out[1] = 0xfe
	for _, unit := range units {
		out = append(out, byte(unit), byte(unit>>8))
	}
	return out
}

// NormalizeDefinition strips CR characters and trailing whitespace so
// generated definitions compare byte-stable against installed files.
func NormalizeDefinition(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
