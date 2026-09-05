// Package service installs and supervises the NusaShell core as a
// user-level platform service: a systemd user unit on Linux, a launchd
// LaunchAgent on macOS, and a Scheduled Task on Windows. Installers call
// this through `nusashell service install`; everything runs unprivileged
// in the installing user's session (no root, no system scope).
//
// The generated definitions always set NUSASHELL_SERVICE=1 so the supervised
// process can identify itself; mutating commands refuse to run from inside
// the service to prevent agent-initiated kill/restart loops.
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// ServiceName is the systemd user unit name and the general identity
	// used across platform service definitions.
	ServiceName = "nusashell"
	// LaunchdLabel is the reverse-DNS launchd label for the macOS agent.
	LaunchdLabel = "id.nusashell.core"
	// TaskName is the Windows Scheduled Task name.
	TaskName = "NusaShell Core"
	// ServiceEnvMarker is set to "1" inside the supervised process.
	ServiceEnvMarker = "NUSASHELL_SERVICE"
)

// Actions accepted by guardOutsideService. Mutating actions are refused
// inside the supervised process; status is always allowed.
const (
	ActionInstall   = "install"
	ActionUninstall = "uninstall"
	ActionStatus    = "status"
	ActionStart     = "start"
	ActionStop      = "stop"
	ActionRestart   = "restart"
)

// Options describes what the service definition supervises. BinaryPath must
// be a stable path (survive version upgrades); DataDir is the NusaShell data
// directory the server should use.
type Options struct {
	BinaryPath  string
	DataDir     string
	Host        string // optional NUSASHELL_HOST override
	Port        string // optional NUSASHELL_PORT override
	AllowRemote bool   // propagate NUSASHELL_ALLOW_REMOTE=1 (explicit consent)
}

// Status snapshots the installed service for CLI output and drift checks.
type Status struct {
	Installed      bool
	Loaded         bool
	Running        bool
	Drifted        bool
	DefinitionPath string
}

// Manager is the per-platform service surface. Implementations live in
// manager_<os>.go files; New dispatches on runtime.GOOS.
type Manager interface {
	Install() error
	Uninstall() error
	Status() (Status, error)
	Start() error
	Stop() error
	Restart() error
}

// Runner executes external service-manager commands (systemctl, launchctl,
// schtasks). Production uses execRunner; tests inject fakes.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// New returns the platform manager for the running OS.
func New(opts Options) Manager {
	run := execRunner{}
	switch runtime.GOOS {
	case "linux":
		return newLinuxManager(opts, run)
	case "darwin":
		return newDarwinManager(opts, run)
	case "windows":
		return newWindowsManager(opts, run)
	default:
		return unsupportedManager{}
	}
}

type unsupportedManager struct{}

func (unsupportedManager) Install() error          { return errUnsupportedPlatform() }
func (unsupportedManager) Uninstall() error        { return errUnsupportedPlatform() }
func (unsupportedManager) Status() (Status, error) { return Status{}, errUnsupportedPlatform() }
func (unsupportedManager) Start() error            { return errUnsupportedPlatform() }
func (unsupportedManager) Stop() error             { return errUnsupportedPlatform() }
func (unsupportedManager) Restart() error          { return errUnsupportedPlatform() }

func errUnsupportedPlatform() error {
	return fmt.Errorf("service management is not supported on %s", runtime.GOOS)
}

// guardOutsideService refuses mutating actions from inside the supervised
// process: combined with Restart=always / KeepAlive, an agent-initiated stop
// would immediately loop into a restart.
func guardOutsideService(action string) error {
	if action == ActionStatus {
		return nil
	}
	if os.Getenv(ServiceEnvMarker) == "1" {
		return fmt.Errorf("refusing to %s the service from inside the service process (%s=1); run from an external shell", action, ServiceEnvMarker)
	}
	return nil
}

// ServiceEnv builds the environment carried by every service definition:
// the supervised marker and the data directory are always present; host,
// port, and remote consent are propagated only when explicitly set.
func ServiceEnv(opts Options) []string {
	env, _ := ServiceEnvChecked(opts)
	return env
}

// ServiceEnvChecked is ServiceEnv but rejects CR/LF in any propagated value:
// every target format (systemd, plist, cmd) is line-oriented.
func ServiceEnvChecked(opts Options) ([]string, error) {
	pairs := [][2]string{
		{ServiceEnvMarker, "1"},
		{"NUSASHELL_DATA_DIR", opts.DataDir},
	}
	if opts.Host != "" {
		pairs = append(pairs, [2]string{"NUSASHELL_HOST", opts.Host})
	}
	if opts.Port != "" {
		pairs = append(pairs, [2]string{"NUSASHELL_PORT", opts.Port})
	}
	if opts.AllowRemote {
		pairs = append(pairs, [2]string{"NUSASHELL_ALLOW_REMOTE", "1"})
	}
	env := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if strings.ContainsAny(pair[1], "\r\n") || strings.ContainsAny(pair[0], "\r\n") {
			return nil, fmt.Errorf("service env %s contains a CR/LF; line-oriented service formats reject it", pair[0])
		}
		env = append(env, pair[0]+"="+pair[1])
	}
	return env, nil
}

// StableBinaryPath rewrites a versioned install path (<root>/versions/<v>/bin)
// to the stable current-install path (<root>/current/bin) so service
// definitions survive version upgrades and old-version pruning. Paths that
// do not follow the layout are returned unchanged.
func StableBinaryPath(execPath string) string {
	dir := filepath.Dir(execPath)
	if filepath.Base(filepath.Dir(dir)) != "versions" {
		return execPath
	}
	current := filepath.Join(filepath.Dir(filepath.Dir(dir)), "current")
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || resolved != dir {
		return execPath
	}
	return filepath.Join(current, filepath.Base(execPath))
}

// checkBinary refuses installs that would supervise a missing program.
func checkBinary(path string) error {
	if path == "" {
		return fmt.Errorf("service install requires a binary path")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("service binary is missing: %s", path)
	}
	return nil
}
