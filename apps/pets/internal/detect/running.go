package detect

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultBackendAddr is where the pet falls back to when the WebSocket
	// URL cannot be parsed.
	DefaultBackendAddr = "127.0.0.1:9999"
	// ProbeTimeout bounds how long a click waits on an unreachable backend.
	ProbeTimeout = 250 * time.Millisecond
	// DefaultProcRoot is the Linux procfs mount point.
	DefaultProcRoot = "/proc"
)

// DialFunc dials a network address; it matches net.Dialer.DialContext.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// BackendAddr extracts the host:port of a ws/wss URL. Unparsable or empty
// URLs fall back to DefaultBackendAddr.
func BackendAddr(wsURL string) string {
	u, err := url.Parse(wsURL)
	if err != nil || u.Host == "" {
		return DefaultBackendAddr
	}
	return u.Host
}

// GoRunning probes whether the NusaShell backend accepts TCP connections at
// the host extracted from the pet's WebSocket URL. The probe is bounded by
// ProbeTimeout; a refused or unreachable backend reports not running.
func GoRunning(wsURL string, dial DialFunc) bool {
	addr := BackendAddr(wsURL)
	ctx, cancel := context.WithTimeout(context.Background(), ProbeTimeout)
	defer cancel()
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ElectronRunning scans procfs for a running nusashell-desktop process by
// matching the process command line. procRoot is a mount point such as
// DefaultProcRoot; missing directories report not running.
func ElectronRunning(procRoot string) bool {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() || !allDigits(entry.Name()) {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(procRoot, entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if strings.Contains(string(cmdline), "nusashell-desktop") {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
