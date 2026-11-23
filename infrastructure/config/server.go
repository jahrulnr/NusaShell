package config

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// ServerConfig controls the WebUI listener: the address it binds, the host
// aliases it accepts in HTTP Host and Origin headers, and whether a login
// is required. Bind defaults to loopback; a reverse proxy in front of a
// loopback listener names its public host in Hosts.
type ServerConfig struct {
	Port  int      `json:"port" default:"10994"`
	Bind  string   `json:"bind" default:"127.0.0.1"`
	Hosts []string `json:"hosts"`
	// PublicURL is the URL the outside world opens this server at — a
	// reverse proxy's address. It opens no listener and never replaces
	// the bind/listen address; `serve --open` prefers it, and its host
	// is folded into Hosts so the guard accepts it.
	PublicURL string `json:"public_url"`
	// Auth is "auto" (login when the server is reachable beyond this
	// machine), "on" (always), or "off" (never; loopback-only setups).
	Auth string `json:"auth" default:"auto"`
}

func (c *ServerConfig) Normalize() {
	ApplyDefaults(c)
	if c.Hosts == nil {
		c.Hosts = []string{"127.0.0.1", "localhost"}
	}
	c.PublicURL = strings.TrimSuffix(strings.TrimSpace(c.PublicURL), "/")
	// The public host is an accepted authority too — a proxy on the
	// public URL forwards its Host header, which the guard would
	// otherwise reject.
	publicHost := ""
	if u, err := url.Parse(c.PublicURL); err == nil {
		publicHost = strings.ToLower(u.Hostname())
	}

	seen := make(map[string]struct{}, len(c.Hosts)+1)
	hosts := make([]string, 0, len(c.Hosts)+1)
	for _, host := range c.Hosts {
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	if publicHost != "" {
		if _, ok := seen[publicHost]; !ok {
			hosts = append(hosts, publicHost)
		}
	}
	c.Hosts = hosts
}

func (c ServerConfig) validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("server.port must be 1..65535, got %d", c.Port)
	}
	if net.ParseIP(c.Bind) == nil {
		return fmt.Errorf("server.bind must be an IP address, got %q", c.Bind)
	}
	switch c.Auth {
	case "auto", "on", "off":
	default:
		return fmt.Errorf("server.auth must be auto, on, or off, got %q", c.Auth)
	}
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("server.public_url must be an http(s) URL with a host, got %q", c.PublicURL)
		}
		if u.User != nil {
			return fmt.Errorf("server.public_url must not carry credentials, got %q", c.PublicURL)
		}
	}
	if len(c.Hosts) == 0 {
		return fmt.Errorf("server.hosts must contain at least one host")
	}
	for _, host := range c.Hosts {
		if !validHostAlias(host) {
			return fmt.Errorf("server.hosts contains invalid host %q", host)
		}
	}
	return nil
}

func validHostAlias(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 || strings.Contains(host, ":") {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return false
			}
		}
	}
	return true
}

// Exposed reports whether the listener can be reached from beyond this
// machine: it binds a non-loopback address, or Hosts names a host other
// than a loopback address or a localhost name — the shape of a reverse
// proxy forwarding public traffic to a loopback listener.
func (c ServerConfig) Exposed() bool {
	if ip := net.ParseIP(c.Bind); ip == nil || !ip.IsLoopback() {
		return true
	}
	for _, host := range c.Hosts {
		if ip := net.ParseIP(host); ip != nil {
			if !ip.IsLoopback() {
				return true
			}
			continue
		}
		if host != "localhost" && !strings.HasSuffix(host, ".localhost") {
			return true
		}
	}
	return false
}

// LocalAuthority is the host:port a process on this machine calls the
// server at: the exact accepted bind when it is a specific interface,
// else a loopback alias the server accepts when it has one, so the call
// never leaves the machine.
func (c ServerConfig) LocalAuthority(port int) string {
	p := strconv.Itoa(port)

	// A specific interface cannot be reached via a loopback alias.
	if ip := net.ParseIP(c.Bind); ip != nil && !ip.IsUnspecified() && slices.Contains(c.Hosts, c.Bind) {
		return net.JoinHostPort(c.Bind, p)
	}
	for _, want := range []string{"127.0.0.1", "localhost", "::1"} {
		if slices.Contains(c.Hosts, want) {
			return net.JoinHostPort(want, p)
		}
	}
	if len(c.Hosts) > 0 {
		return net.JoinHostPort(c.Hosts[0], p)
	}
	return net.JoinHostPort("127.0.0.1", p)
}
