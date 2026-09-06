package pet

import (
	"net"
	"strings"
)

const (
	envHost  = "NUSASHELL_HOST"
	envPort  = "NUSASHELL_PORT"
	envWSURL = "NUSASHELL_WS_URL"
)

// SetBackend records the Go process listen address so Launch can point the
// pet at this instance instead of a baked-in config.json URL.
func (in *Installer) SetBackend(host, port string) {
	if in == nil {
		return
	}
	in.procMu.Lock()
	defer in.procMu.Unlock()
	in.backendHost = strings.TrimSpace(host)
	in.backendPort = strings.TrimSpace(port)
}

func (in *Installer) backendLocked() (host, port string) {
	return in.backendHost, in.backendPort
}

// petConnectHost maps a listen address to a host the pet can dial. Wildcard
// binds are rewritten to loopback so ws://0.0.0.0:... is never handed out.
func petConnectHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return "127.0.0.1"
	}
	return host
}

// petWSURL builds ws://<host>:<port>/ws. Empty port yields "".
func petWSURL(host, port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		return ""
	}
	return "ws://" + net.JoinHostPort(petConnectHost(host), port) + "/ws"
}

func petSpawnArgs(assets, host, port string) []string {
	args := []string{"--assets", assets}
	if ws := petWSURL(host, port); ws != "" {
		args = append(args, "--ws-url", ws)
	}
	return args
}

func petSpawnEnv(parent []string, host, port string) []string {
	ws := petWSURL(host, port)
	if ws == "" {
		return parent
	}
	out := make([]string, 0, len(parent)+3)
	for _, kv := range parent {
		if strings.HasPrefix(kv, envHost+"=") || strings.HasPrefix(kv, envPort+"=") || strings.HasPrefix(kv, envWSURL+"=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		envHost+"="+petConnectHost(host),
		envPort+"="+strings.TrimSpace(port),
		envWSURL+"="+ws,
	)
}
