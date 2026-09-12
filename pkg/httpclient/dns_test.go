package httpclient

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestIsNonPublicAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{name: "ipv4 private 10", addr: "10.0.0.1", want: true},
		{name: "ipv4 loopback", addr: "127.0.0.1", want: true},
		{name: "ipv4 private 172", addr: "172.16.0.1", want: true},
		{name: "ipv4 private 172 upper bound", addr: "172.31.255.255", want: true},
		{name: "ipv4 private 192", addr: "192.168.1.1", want: true},
		{name: "ipv4 link local", addr: "169.254.1.1", want: true},
		{name: "ipv6 loopback", addr: "::1", want: true},
		{name: "ipv6 ula", addr: "fd00::1", want: true},
		{name: "ipv6 link local", addr: "fe80::1", want: true},
		{name: "ipv6 multicast", addr: "ff02::1", want: true},
		{name: "ipv4 mapped private", addr: "::ffff:10.0.0.1", want: true},
		{name: "ipv4 public", addr: "1.1.1.1", want: false},
		{name: "ipv6 public", addr: "2606:4700:4700::1111", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := netip.ParseAddr(tt.addr)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.addr, err)
			}
			if got := isNonPublicAddr(addr); got != tt.want {
				t.Fatalf("isNonPublicAddr(%s) = %v, want %v", addr, got, tt.want)
			}
		})
	}
}

func TestPublicResolverReturnsIPv4AndIPv6Answers(t *testing.T) {
	server := newTestDNSServer(t, netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("2001:db8::7"))
	resolver := newResolverForServers([]string{server.address()})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := resolver.LookupNetIP(ctx, "ip", "example.test")
	if err != nil {
		t.Fatalf("LookupNetIP: %v", err)
	}

	want := map[netip.Addr]bool{
		netip.MustParseAddr("203.0.113.7"): false,
		netip.MustParseAddr("2001:db8::7"): true,
	}
	if len(got) != len(want) {
		t.Fatalf("LookupNetIP returned %v, want one A and one AAAA answer", got)
	}
	for _, addr := range got {
		if _, ok := want[addr]; !ok {
			t.Errorf("unexpected answer %s", addr)
		}
	}
}

func TestPublicResolverRotatesUpstreams(t *testing.T) {
	first := newTestDNSServer(t, netip.MustParseAddr("203.0.113.8"), netip.MustParseAddr("2001:db8::8"))
	second := newTestDNSServer(t, netip.MustParseAddr("203.0.113.9"), netip.MustParseAddr("2001:db8::9"))
	resolver := newResolverForServers([]string{first.address(), second.address()})

	for i, want := range []string{first.address(), second.address(), first.address(), second.address()} {
		conn, err := resolver.Dial(context.Background(), "udp", "ignored:53")
		if err != nil {
			t.Fatalf("Dial %d: %v", i, err)
		}
		got := conn.RemoteAddr().String()
		_ = conn.Close()
		if got != want {
			t.Fatalf("Dial %d remote address = %q, want %q", i, got, want)
		}
	}
}

func TestPublicResolverDowngradesIPv6NetworkForIPv4Upstream(t *testing.T) {
	server := newTestDNSServer(t, netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10"))
	resolver := newResolverForServers([]string{server.address()})

	conn, err := resolver.Dial(context.Background(), "udp6", "ignored:53")
	if err != nil {
		t.Fatalf("Dial udp6: %v", err)
	}
	defer conn.Close()
	if got := conn.RemoteAddr().String(); got != server.address() {
		t.Fatalf("remote address = %q, want IPv4 upstream %q", got, server.address())
	}
}

func TestRejectNonPublicAddress(t *testing.T) {
	tests := []struct {
		address string
		wantErr bool
	}{
		{address: "127.0.0.1:443", wantErr: true},
		{address: "10.0.0.1:443", wantErr: true},
		{address: "[fd00::1]:443", wantErr: true},
		{address: "1.1.1.1:443", wantErr: false},
		{address: "[2606:4700:4700::1111]:443", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			err := rejectNonPublicAddress(context.Background(), "tcp", tt.address, nil)
			if tt.wantErr && err == nil {
				t.Fatalf("rejectNonPublicAddress(%q) returned nil", tt.address)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("rejectNonPublicAddress(%q) = %v", tt.address, err)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "non-public") {
				t.Fatalf("error = %v, want non-public explanation", err)
			}
		})
	}
}

func TestDialerRejectsNonPublicDNSAnswer(t *testing.T) {
	server := newTestDNSServer(t, netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("fd00::1"))
	dialer := &net.Dialer{
		Timeout:        time.Second,
		Resolver:       newResolverForServers([]string{server.address()}),
		ControlContext: rejectNonPublicAddress,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := dialer.DialContext(ctx, "tcp", "blocked.example:443")
	if err == nil {
		t.Fatal("DialContext accepted private DNS answers")
	}
	if !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("DialContext error = %v, want non-public explanation", err)
	}
}

type testDNSServer struct {
	conn      *net.UDPConn
	answerA   netip.Addr
	answerAAA netip.Addr
	closeOnce sync.Once
}

func newTestDNSServer(t *testing.T, answerA, answerAAA netip.Addr) *testDNSServer {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	server := &testDNSServer{conn: conn, answerA: answerA, answerAAA: answerAAA}
	t.Cleanup(func() {
		server.closeOnce.Do(func() { _ = conn.Close() })
	})
	go server.serve()
	return server
}

func (s *testDNSServer) address() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(s.conn.LocalAddr().(*net.UDPAddr).Port))
}

func (s *testDNSServer) serve() {
	buf := make([]byte, 1500)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var parser dnsmessage.Parser
		header, err := parser.Start(buf[:n])
		if err != nil {
			continue
		}
		question, err := parser.Question()
		if err != nil {
			continue
		}

		builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
			ID:                 header.ID,
			Response:           true,
			RecursionAvailable: true,
			RCode:              dnsmessage.RCodeSuccess,
		})
		builder.EnableCompression()
		if err := builder.StartQuestions(); err != nil {
			continue
		}
		if err := builder.Question(question); err != nil {
			continue
		}
		if err := builder.StartAnswers(); err != nil {
			continue
		}
		var addErr error
		switch question.Type {
		case dnsmessage.TypeA:
			var answer [4]byte
			copy(answer[:], s.answerA.AsSlice())
			addErr = builder.AResource(dnsmessage.ResourceHeader{Name: question.Name, TTL: 60}, dnsmessage.AResource{A: answer})
		case dnsmessage.TypeAAAA:
			addErr = builder.AAAAResource(dnsmessage.ResourceHeader{Name: question.Name, TTL: 60}, dnsmessage.AAAAResource{AAAA: s.answerAAA.As16()})
		}
		if addErr != nil {
			continue
		}
		packet, err := builder.Finish()
		if err == nil {
			_, _ = s.conn.WriteToUDP(packet, addr)
		}
	}
}
