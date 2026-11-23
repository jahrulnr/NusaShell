//go:build windows

package system

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Restart on Windows cannot replace the running image: the executable is
// locked while this process lives. The updater has already left the new
// binary at <path>.new (see repository/binary); this method starts a
// background PowerShell helper that waits for this process to exit, swaps the
// staged file over the installed path, and starts the installed task (or the
// process, for a foreground run).
//
// The helper is started before os.Exit so it outlives the server; the
// update's "restarting" progress frame has already been sent by the caller.
func (r Restarter) Restart(mode string) error {
	if mode != "service" {
		mode = "process"
	}
	args := make([]string, 0, max(len(r.argv)-1, 0))
	if len(r.argv) > 1 {
		for _, a := range r.argv[1:] {
			args = append(args, winArgQuote(a))
		}
	}
	cwd, _ := os.Getwd()
	script := fmt.Sprintf(`$ErrorActionPreference = 'Continue'
$exe = %s
$new = %s
$parentPid = %d
$mode = %s
$task = %s
$argLine = %s
$cwd = %s
$logDir = Join-Path $env:LOCALAPPDATA 'nusashell\logs'
New-Item -ItemType Directory -Path $logDir -Force | Out-Null
$log = Join-Path $logDir 'update-helper.log'
function Log($m) { Add-Content -LiteralPath $log -Value ((Get-Date).ToString('o') + ' ' + $m) }
Log "start parent=$parentPid mode=$mode task=$task"
while (Get-Process -Id $parentPid -ErrorAction SilentlyContinue) { Start-Sleep -Milliseconds 300 }
Log 'parent exited'
if (Test-Path -LiteralPath $new) {
  $moved = $false
  for ($i = 0; $i -lt 60; $i++) {
    try { Move-Item -LiteralPath $new -Destination $exe -Force -ErrorAction Stop; $moved = $true; break }
    catch { Log ('move failed: ' + $_) ; Start-Sleep -Milliseconds 500 }
  }
  Log "moved=$moved"
  if (-not $moved) { exit 1 }
}
$started = $false
if ($mode -eq 'service' -and $task) {
  schtasks /End /TN $task 2>$null | Out-Null
  Log 'task ended; running'
  for ($i = 0; $i -lt 60; $i++) {
    schtasks /Run /TN $task 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) { $started = $true; break }
    Log ('task run failed: ' + $LASTEXITCODE)
    Start-Sleep -Milliseconds 500
  }
}
Log "task started=$started"
if (-not $started) {
  if ($argLine) { Start-Process -FilePath $exe -ArgumentList $argLine -WorkingDirectory $cwd }
  else { Start-Process -FilePath $exe -WorkingDirectory $cwd }
  Log 'process start requested'
}`, psQuote(r.path), psQuote(r.path+".new"), os.Getpid(), psQuote(mode), psQuote(r.name),
		psQuote(strings.Join(args, " ")), psQuote(cwd))
	logDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "nusashell", "logs")
	if logDir == "nusashell\\logs" {
		logDir = os.TempDir()
	}
	_ = os.MkdirAll(logDir, 0o700)
	// The helper appends to update-helper.log in the server's log directory;
	// a failed swap/start leaves the reason there after this process exits.
	// It must be spawned outside this task's job — a child process dies at
	// the schtasks /End the helper itself issues (see spawnOutsideJob).
	helper := "powershell -NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand " + encodePS(script)
	if err := spawnOutsideJob(helper); err != nil {
		return fmt.Errorf("start update helper: %w", err)
	}
	os.Exit(0)
	return nil // unreachable
}

// encodePS makes the UTF-16LE base64 -EncodedCommand expects.
func encodePS(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}
