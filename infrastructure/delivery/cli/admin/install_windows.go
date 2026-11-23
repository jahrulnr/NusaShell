//go:build windows

package admin

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/system"
)

// installedBinHint is where install puts the binary, for help text.
const installedBinHint = `%LOCALAPPDATA%\NusaShell\bin\` + CLIName + `.exe`

// localAppData is the roaming-free machine-local root — %LOCALAPPDATA%.
func localAppData() (string, error) {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Abs(dir)
	}
	return os.UserCacheDir()
}

// roamingAppData is the per-user roaming root — %APPDATA%.
func roamingAppData() (string, error) {
	if dir := os.Getenv("APPDATA"); dir != "" {
		return filepath.Abs(dir)
	}
	return os.UserConfigDir()
}

func userBinPath() (string, error) {
	local, err := localAppData()
	if err != nil {
		return "", err
	}
	return filepath.Join(local, "NusaShell", "bin", CLIName+".exe"), nil
}

// userConfigPath honors an explicit XDG_CONFIG_HOME first (an opt-in
// override everywhere), then the Windows roaming profile.
func userConfigPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Abs(filepath.Join(dir, "nusashell", "config.json"))
	}
	dir, err := roamingAppData()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nusashell", "config.json"), nil
}

func userDataDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Abs(filepath.Join(dir, "nusashell"))
	}
	local, err := localAppData()
	if err != nil {
		return "", err
	}
	return filepath.Join(local, "nusashell"), nil
}

// pathAdvice is the PowerShell line a user pastes to put the install
// dir on their user PATH for new terminals — cmd and POSIX export
// syntax do not apply on Windows.
func pathAdvice(dir string) string {
	return fmt.Sprintf(`[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';%s', 'User')`,
		strings.ReplaceAll(dir, "'", "''"))
}

// samePath compares PATH entries — Windows paths compare folded.
func samePath(a, b string) bool { return strings.EqualFold(a, b) }

// prepareBinaryInstall frees the installed image so installBinary can
// replace it: Windows locks a running .exe against rename, so the
// scheduled task is ended first, then any straggler running that image
// (a foreground `serve` locks it exactly like the task), and finally
// the file must accept a writer before the copy lands. Nothing runs
// when this process IS the installed image — there is nothing to
// replace, so nothing gets stopped.
func prepareBinaryInstall(svc system.Service, bin string, stderr io.Writer) error {
	if sameExecutable(bin) {
		return nil
	}
	stopped := false
	if svc.Active(CLIName) {
		if err := svc.Stop(CLIName); err != nil {
			fmt.Fprintf(stderr, "%s: could not stop the service: %s\n", CLIName, err)
		} else {
			stopped = true
		}
	}
	killProcessesAt(bin)
	if err := waitWritable(bin, 20*time.Second); err != nil {
		return err
	}
	if stopped {
		fmt.Fprintf(stderr, "%s: stopped the running service to replace the binary\n", CLIName)
	}
	return nil
}

// sameExecutable reports whether path is this process's own image — a
// reinstall invoked from the installed binary has nothing to copy.
func sameExecutable(path string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}
	srcInfo, err := os.Stat(exe)
	if err != nil {
		return false
	}
	dstInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(srcInfo, dstInfo)
}

// killProcessesAt ends every process still running the image at path —
// the task is already stopped, so what remains is a manually-started
// copy holding the file lock. PowerShell is the only process filter
// that matches on the image path without parsing localized output.
func killProcessesAt(path string) {
	ps, err := exec.LookPath("powershell")
	if err != nil {
		return
	}
	escaped := strings.ReplaceAll(path, "'", "''")
	script := "Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq '" + escaped + "' } | Stop-Process -Force -ErrorAction SilentlyContinue"
	_ = exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

// waitWritable blocks until path opens for writing — a running image
// denies the write share, so a successful open means the old process
// is gone and the rename in installBinary will land.
func waitWritable(path string, timeout time.Duration) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err == nil {
			_ = f.Close()
			return nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s is still in use by a running process — close it and retry", path)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// servicePath is the PATH the task's daemon and its children see: the
// install dir first, then the installer's own PATH.
func servicePath(home string) string {
	lead := filepath.Join(home, "go", "bin")
	if local, err := localAppData(); err == nil {
		lead = filepath.Join(local, "NusaShell", "bin") + ";" + lead
	}
	return lead + ";" + cmp.Or(os.Getenv("PATH"), `C:\Windows\System32;C:\Windows`)
}

// removeInstalledBinary deletes the installed executable. A running
// image cannot be deleted on Windows — but it can be renamed. The file
// is parked under a stale name and a detached cmd retries the delete in
// the background, which lands once the running process exits.
func removeInstalledBinary(path string) (bool, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file; left in place", path)
	}
	if err := os.Remove(path); err == nil {
		return true, nil
	}
	stale := fmt.Sprintf("%s.old-%d", path, os.Getpid())
	_ = os.Remove(stale)
	if err := os.Rename(path, stale); err != nil {
		return false, fmt.Errorf("could not remove %s: it is in use", path)
	}
	deferredDelete(stale)
	return true, nil
}

// deferredDelete hands a stale image to a detached cmd that retries the
// delete for about two minutes — long enough for the process holding it
// (including this one, when `uninstall` ran the installed copy) to exit.
// The loop keys on the file being gone, not del's exit code: del reports
// success even on "Access is denied", so && would exit on attempt one.
// SysProcAttr.CmdLine passes the line verbatim with its argv[0]: cmd's
// own parsing would mangle the nested quotes Go escapes, and a bare /C
// would be read as the program name.
func deferredDelete(path string) {
	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | 0x00000200, // CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP
		CmdLine: `cmd /C for /l %i in (1,1,60) do (del /f /q "` + path + `" >nul 2>&1 & if not exist "` +
			path + `" (exit /b 0) else ping -n 2 127.0.0.1 >nul)`,
	}
	if err := cmd.Start(); err == nil {
		_ = cmd.Process.Release()
	}
}
