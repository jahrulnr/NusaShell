//go:build darwin

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// darwinManager supervises the core through a per-user launchd LaunchAgent.
type darwinManager struct {
	opts     Options
	run      Runner
	plistDir string     // ~/Library/LaunchAgents; overridden in tests
	uid      func() int // gui domain owner; overridden in tests
	homeDir  func() (string, error)
}

func newDarwinManager(opts Options, run Runner) *darwinManager {
	return &darwinManager{
		opts:     opts,
		run:      run,
		plistDir: launchdAgentsDir(),
		uid:      os.Getuid,
		homeDir:  os.UserHomeDir,
	}
}

func launchdAgentsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "LaunchAgents")
}

func (m *darwinManager) plistPath() string {
	return filepath.Join(m.plistDir, LaunchdLabel+".plist")
}

func (m *darwinManager) domain() string {
	return fmt.Sprintf("gui/%d", m.uid())
}

func (m *darwinManager) target() string {
	return m.domain() + "/" + LaunchdLabel
}

func (m *darwinManager) Install() error {
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
	if err := os.MkdirAll(m.plistDir, 0o755); err != nil {
		return err
	}
	plistPath := m.plistPath()
	if err := assertNotSymlink(plistPath); err != nil {
		return err
	}
	previous, snapshotErr := readDefinition(plistPath)
	if snapshotErr != nil {
		return snapshotErr
	}
	if err := atomicWrite(plistPath, []byte(LaunchdPlist(m.opts, env)), 0o644); err != nil {
		return err
	}
	// bootout first so a re-install replaces the loaded definition; a job that
	// was never loaded reports an error here which is safe to ignore.
	_, _ = m.run.Run("launchctl", "bootout", m.target())
	if _, err := m.run.Run("launchctl", "bootstrap", m.domain(), plistPath); err != nil {
		// Restore the previous definition so a failed bootstrap never leaves a
		// half-installed agent behind.
		if previous == nil {
			_ = os.Remove(plistPath)
		} else {
			_ = atomicWrite(plistPath, previous, 0o644)
		}
		return fmt.Errorf("launchctl bootstrap failed: %w", err)
	}
	return nil
}

func (m *darwinManager) Uninstall() error {
	if err := guardOutsideService(ActionUninstall); err != nil {
		return err
	}
	_, _ = m.run.Run("launchctl", "bootout", m.target())
	plistPath := m.plistPath()
	if _, err := os.Lstat(plistPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// Move to the user's Trash instead of unlinking so removal stays visible
	// and recoverable (same contract as openclaw's LaunchAgent uninstall).
	if home, err := m.homeDir(); err == nil {
		trash := filepath.Join(home, ".Trash")
		if err := os.MkdirAll(trash, 0o755); err == nil {
			if err := os.Rename(plistPath, filepath.Join(trash, LaunchdLabel+".plist")); err == nil {
				return nil
			}
		}
	}
	return os.Remove(plistPath)
}

func (m *darwinManager) Status() (Status, error) {
	plistPath := m.plistPath()
	st := Status{DefinitionPath: plistPath}
	if content, err := os.ReadFile(plistPath); err == nil {
		st.Installed = true
		st.Drifted = NormalizeDefinition(string(content)) != NormalizeDefinition(LaunchdPlist(m.opts, ServiceEnv(m.opts)))
	} else if !os.IsNotExist(err) {
		return st, err
	}
	out, err := m.run.Run("launchctl", "print", m.target())
	if err != nil {
		return st, nil // not bootstrapped
	}
	st.Loaded = true
	if strings.Contains(out, "state = running") {
		st.Running = true
	}
	return st, nil
}

func (m *darwinManager) Start() error {
	if err := guardOutsideService(ActionStart); err != nil {
		return err
	}
	_, err := m.run.Run("launchctl", "kickstart", m.target())
	return err
}

func (m *darwinManager) Stop() error {
	if err := guardOutsideService(ActionStop); err != nil {
		return err
	}
	// bootout unloads the definition so KeepAlive cannot immediately respawn
	// the process; start re-bootstraps.
	_, err := m.run.Run("launchctl", "bootout", m.target())
	return err
}

func (m *darwinManager) Restart() error {
	if err := guardOutsideService(ActionRestart); err != nil {
		return err
	}
	_, err := m.run.Run("launchctl", "kickstart", "-k", m.target())
	return err
}
