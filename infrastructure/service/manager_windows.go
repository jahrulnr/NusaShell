//go:build windows

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// windowsManager supervises the core through a per-user Scheduled Task with
// a Startup-folder fallback for hosts where schtasks creation is denied.
type windowsManager struct {
	opts    Options
	run     Runner
	getenv  func(string) string
	tempDir func() string
}

func newWindowsManager(opts Options, run Runner) *windowsManager {
	return &windowsManager{
		opts:    opts,
		run:     run,
		getenv:  os.Getenv,
		tempDir: os.TempDir,
	}
}

// fallbackDetail matches localized schtasks failures that mean "this host
// will not let us create the task": keep the list in sync with the regexes
// used by openclaw/hermes (English, Spanish, Czech, timeout).
func fallbackDetail(detail string) bool {
	if strings.Contains(detail, "timed out") {
		return true
	}
	for _, marker := range []string{
		"access is denied", "acceso denegado", "přístup byl odepřen",
	} {
		if strings.Contains(strings.ToLower(detail), marker) {
			return true
		}
	}
	return false
}

func (m *windowsManager) startupDir() string {
	appData := m.getenv("APPDATA")
	if appData == "" {
		return ""
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
}

func (m *windowsManager) startupEntryPath() string {
	startup := m.startupDir()
	if startup == "" {
		return ""
	}
	return filepath.Join(startup, strings.ReplaceAll(TaskName, " ", "-")+".vbs")
}

func (m *windowsManager) Install() error {
	if err := guardOutsideService(ActionInstall); err != nil {
		return err
	}
	if err := checkBinary(m.opts.BinaryPath); err != nil {
		return err
	}
	if err := ensureDataDir(m.opts.DataDir); err != nil {
		return err
	}
	env, err := ServiceEnvChecked(m.opts)
	if err != nil {
		return err
	}
	scriptPath := WindowsTaskScriptPath(m.opts.DataDir)
	launcherPath := WindowsHiddenLauncherPath(m.opts.DataDir)
	for _, dir := range []string{filepath.Dir(scriptPath), filepath.Dir(WindowsTaskLogPath(m.opts.DataDir))} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	task := WindowsTask{
		Name:         TaskName,
		User:         WindowsTaskUser(m.getenv),
		ScriptPath:   scriptPath,
		LauncherPath: launcherPath,
	}
	if err := os.WriteFile(scriptPath, []byte(WindowsTaskScript(m.opts, env)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(launcherPath, []byte(WindowsHiddenLauncher(task)), 0o644); err != nil {
		return err
	}
	xmlPath := filepath.Join(m.tempDir(), fmt.Sprintf("nusashell-task-%d.xml", os.Getpid()))
	if err := os.WriteFile(xmlPath, WindowsUTF16LE(WindowsTaskXML(task)), 0o644); err != nil {
		return err
	}
	defer os.Remove(xmlPath)
	out, createErr := m.run.Run("schtasks", "/Create", "/F", "/TN", TaskName, "/XML", xmlPath)
	if createErr == nil {
		return nil
	}
	detail := strings.TrimSpace(out) + " " + createErr.Error()
	if !fallbackDetail(detail) {
		return fmt.Errorf("schtasks create failed: %s", strings.TrimSpace(detail))
	}
	// Fallback: a Startup-folder entry survives logon without Task Scheduler
	// (locked-down corporate boxes) and runs the same hidden launcher.
	entry := m.startupEntryPath()
	if entry == "" {
		return fmt.Errorf("schtasks create failed and the Startup folder is unavailable: %s", strings.TrimSpace(detail))
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(entry, []byte(WindowsHiddenLauncher(task)), 0o644); err != nil {
		return err
	}
	return nil
}

func (m *windowsManager) Uninstall() error {
	if err := guardOutsideService(ActionUninstall); err != nil {
		return err
	}
	_, _ = m.run.Run("schtasks", "/Delete", "/F", "/TN", TaskName)
	if entry := m.startupEntryPath(); entry != "" {
		if err := os.Remove(entry); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	scriptPath := WindowsTaskScriptPath(m.opts.DataDir)
	launcherPath := WindowsHiddenLauncherPath(m.opts.DataDir)
	for _, path := range []string{scriptPath, launcherPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (m *windowsManager) Status() (Status, error) {
	st := Status{DefinitionPath: WindowsTaskScriptPath(m.opts.DataDir)}
	scriptPath := WindowsTaskScriptPath(m.opts.DataDir)
	if _, err := os.Stat(scriptPath); err == nil {
		st.Installed = true
		// Drift compares the generated wrapper against the installed one; the
		// env snapshot makes this deterministic for the same install options.
		content, rerr := os.ReadFile(scriptPath)
		if rerr != nil {
			return st, rerr
		}
		env, _ := ServiceEnvChecked(m.opts)
		st.Drifted = NormalizeDefinition(string(content)) != NormalizeDefinition(WindowsTaskScript(m.opts, env))
	}
	out, err := m.run.Run("schtasks", "/Query", "/TN", TaskName, "/FO", "LIST", "/V")
	if err != nil {
		return st, nil // not registered
	}
	st.Loaded = true
	if strings.Contains(out, "Status:") && strings.Contains(out, "Running") {
		st.Running = true
	}
	return st, nil
}

func (m *windowsManager) Start() error {
	if err := guardOutsideService(ActionStart); err != nil {
		return err
	}
	if entry := m.startupEntryPath(); fileExists(entry) {
		// The Startup fallback owns the service; launch the same script.
		if _, err := m.run.Run("wscript.exe", entry); err != nil {
			return err
		}
		return nil
	}
	_, err := m.run.Run("schtasks", "/Run", "/TN", TaskName)
	return err
}

func (m *windowsManager) Stop() error {
	if err := guardOutsideService(ActionStop); err != nil {
		return err
	}
	_, err := m.run.Run("schtasks", "/End", "/TN", TaskName)
	return err
}

func (m *windowsManager) Restart() error {
	if err := guardOutsideService(ActionRestart); err != nil {
		return err
	}
	// End ignores a not-running task; Run then starts a fresh instance.
	_, _ = m.run.Run("schtasks", "/End", "/TN", TaskName)
	_, err := m.run.Run("schtasks", "/Run", "/TN", TaskName)
	return err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
