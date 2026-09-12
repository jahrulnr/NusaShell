package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const dnsDialTimeout = 2 * time.Second

// publicDNSServers intentionally uses IPv4 endpoints. DNS queries sent over
// IPv4 can still request both A and AAAA records, while avoiding the broken
// IPv6 DNS path this client is designed to work around. The addresses are the
// standard resolvers documented by Cloudflare and Google:
// https://developers.cloudflare.com/1.1.1.1/ip-addresses/ and
// https://developers.google.com/speed/public-dns/docs/using.
var publicDNSServers = []string{
	"1.1.1.1:53",
	"8.8.8.8:53",
	"1.0.0.1:53",
	"8.8.4.4:53",
}

// newPublicResolver returns a Go DNS resolver that bypasses the host's local
// resolver and rotates across Cloudflare and Google Public DNS endpoints.
func newPublicResolver() *net.Resolver {
	return newResolverForServers(publicDNSServers)
}

func newResolverForServers(servers []string) *net.Resolver {
	servers = append([]string(nil), servers...)
	if len(servers) == 0 {
		servers = append([]string(nil), publicDNSServers...)
	}
	var next atomic.Uint64
	return &net.Resolver{
		PreferGo:     true,
		StrictErrors: false,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			index := next.Add(1) - 1
			server := servers[index%uint64(len(servers))]
			if strings.HasSuffix(network, "6") {
				if host, _, err := net.SplitHostPort(server); err == nil {
					if addr, parseErr := netip.ParseAddr(host); parseErr == nil && addr.Is4() {
						network = strings.TrimSuffix(network, "6") + "4"
					}
				}
			}
			return (&net.Dialer{Timeout: dnsDialTimeout}).DialContext(ctx, network, server)
		},
	}
}

// isNonPublicAddr reports whether an address must not be used as an outbound
// HTTP destination. Unmapping first makes IPv4-mapped IPv6 addresses follow
// the same private/loopback rules as their IPv4 form.
func isNonPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.Unmap()
	return addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}

// rejectNonPublicAddress is used as a net.Dialer ControlContext hook. The
// hook runs after DNS address selection but before the socket connects, so a
// private answer can never become an outbound HTTP connection.
func rejectNonPublicAddress(_ context.Context, _, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("httpclient: invalid remote address %q: %w", address, err)
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("httpclient: invalid remote address %q: %w", address, err)
	}
	if isNonPublicAddr(addr) {
		return fmt.Errorf("httpclient: refusing non-public destination %s", addr)
	}
	return nil
}
