//go:build linux

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// linuxManager supervises the core through a systemd user unit.
type linuxManager struct {
	opts    Options
	run     Runner
	unitDir string // ~/.config/systemd/user (XDG_CONFIG_HOME aware); overridden in tests
}

func newLinuxManager(opts Options, run Runner) *linuxManager {
	return &linuxManager{
		opts:    opts,
		run:     run,
		unitDir: systemdUserUnitDir(),
	}
}

func systemdUserUnitDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user")
}

func (m *linuxManager) unitPath() string {
	return filepath.Join(m.unitDir, ServiceName+".service")
}

func (m *linuxManager) Install() error {
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
	if err := os.MkdirAll(m.unitDir, 0o755); err != nil {
		return err
	}
	unitPath := m.unitPath()
	if err := assertNotSymlink(unitPath); err != nil {
		return err
	}
	if err := backupExisting(unitPath); err != nil {
		return fmt.Errorf("backing up previous unit: %w", err)
	}
	if err := atomicWrite(unitPath, []byte(SystemdUnit(m.opts, env)), 0o644); err != nil {
		return err
	}
	return m.activate()
}

func (m *linuxManager) activate() error {
	if err := m.ensureUserBus(); err != nil {
		return err
	}
	if _, err := m.run.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload failed: %w", err)
	}
	for _, action := range []string{"enable", "restart"} {
		if _, err := m.run.Run("systemctl", "--user", action, ServiceName+".service"); err != nil {
			return fmt.Errorf("systemctl --user %s failed: %w", action, err)
		}
	}
	return nil
}

// ensureUserBus verifies the systemd user instance is reachable. On fresh
// SSH sessions the user bus may not exist until linger is enabled; try
// enable-linger (polkit often allows it unprivileged) before failing with a
// precise remediation.
func (m *linuxManager) ensureUserBus() error {
	out, err := m.run.Run("systemctl", "--user", "is-system-running")
	if err == nil {
		return nil
	}
	detail := out + err.Error()
	if !strings.Contains(detail, "Failed to connect to bus") {
		// Bus reachable; the instance is merely degraded/stopping — fine.
		return nil
	}
	if _, lerr := m.run.Run("loginctl", "enable-linger"); lerr == nil {
		if _, rerr := m.run.Run("systemctl", "--user", "is-system-running"); rerr == nil {
			return nil
		}
	}
	return fmt.Errorf("systemd user bus unavailable (%v); open a login session or run 'loginctl enable-linger' (may require sudo), then retry", err)
}

func (m *linuxManager) Uninstall() error {
	if err := guardOutsideService(ActionUninstall); err != nil {
		return err
	}
	// disable may fail when the unit never existed or the bus is unreachable;
	// the unit-file removal below is the source of truth, so uninstall stays
	// idempotent either way.
	_, _ = m.run.Run("systemctl", "--user", "disable", "--now", ServiceName+".service")
	unitPath := m.unitPath()
	if err := os.Remove(unitPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if _, err := m.run.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload after removal failed: %w", err)
	}
	return nil
}

func (m *linuxManager) Status() (Status, error) {
	unitPath := m.unitPath()
	st := Status{DefinitionPath: unitPath}
	if content, err := os.ReadFile(unitPath); err == nil {
		st.Installed = true
		st.Drifted = NormalizeDefinition(string(content)) != NormalizeDefinition(SystemdUnit(m.opts, ServiceEnv(m.opts)))
	} else if !os.IsNotExist(err) {
		return st, err
	}
	enabled, err := m.run.Run("systemctl", "--user", "is-enabled", ServiceName+".service")
	if err == nil && strings.TrimSpace(enabled) == "enabled" {
		st.Loaded = true
	}
	active, err := m.run.Run("systemctl", "--user", "is-active", ServiceName+".service")
	if err == nil && strings.TrimSpace(active) == "active" {
		st.Running = true
	}
	return st, nil
}

func (m *linuxManager) Start() error {
	if err := guardOutsideService(ActionStart); err != nil {
		return err
	}
	_, err := m.run.Run("systemctl", "--user", "start", ServiceName+".service")
	return err
}

func (m *linuxManager) Stop() error {
	if err := guardOutsideService(ActionStop); err != nil {
		return err
	}
	_, err := m.run.Run("systemctl", "--user", "stop", ServiceName+".service")
	return err
}

func (m *linuxManager) Restart() error {
	if err := guardOutsideService(ActionRestart); err != nil {
		return err
	}
	_, err := m.run.Run("systemctl", "--user", "restart", ServiceName+".service")
	return err
}
