// Package httpclient owns NusaShell's outbound HTTP client and transport
// policy. Callers should create request values per operation, then reuse the
// returned client (or its shared transport) for the lifetime of the adapter.
// New uses the host resolver for provider and local endpoint compatibility;
// NewPublic is reserved for public web retrieval and uses the app-owned
// public DNS plus destination filtering.
//
// Authentication, cookies, redirects, and request-specific behavior stay on
// client wrappers or requests. The transport is shared so its keep-alive and
// HTTP/2 connection pools are reused across infrastructure adapters without
// sharing credentials between them.
package httpclient

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	// DialTimeout bounds establishing a new network connection. Request
	// contexts still provide the caller-specific cancellation boundary.
	DialTimeout = 300 * time.Second
	// KeepAlive controls TCP keep-alive probes for connections in use.
	KeepAlive = 300 * time.Second
	// TLSHandshakeTimeout bounds the TLS handshake for a fresh connection.
	TLSHandshakeTimeout = 300 * time.Second
	// ResponseHeaderTimeout bounds waiting for response headers. It does not
	// limit streaming response bodies.
	ResponseHeaderTimeout = 300 * time.Second
	// IdleConnTimeout bounds how long an idle keep-alive connection remains
	// in the shared pool.
	IdleConnTimeout = 300 * time.Second
	// DefaultRequestTimeout is the default whole-request bound for finite
	// outbound operations such as model metadata, media, and downloads that
	// do not use a streaming body.
	DefaultRequestTimeout = 300 * time.Second
	// MaxIdleConns is the process-wide cap for idle connections in the pool.
	MaxIdleConns = 100
	// MaxIdleConnsPerHost is intentionally higher than net/http's default of
	// two because NusaShell can issue parallel provider/tool requests.
	MaxIdleConnsPerHost = 16
)

var (
	transportOnce       sync.Once
	sharedTransport     *http.Transport
	sharedClient        *http.Client
	publicTransportOnce sync.Once
	publicTransport     *http.Transport
	publicClient        *http.Client
)

func newTransport(resolver *net.Resolver, publicOnly bool) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   DialTimeout,
		KeepAlive: KeepAlive,
		Resolver:  resolver,
	}
	if publicOnly {
		dialer.ControlContext = rejectNonPublicAddress
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   TLSHandshakeTimeout,
		ResponseHeaderTimeout: ResponseHeaderTimeout,
		IdleConnTimeout:       IdleConnTimeout,
		MaxIdleConns:          MaxIdleConns,
		MaxIdleConnsPerHost:   MaxIdleConnsPerHost,
	}
}

func initShared() {
	transportOnce.Do(func() {
		sharedTransport = newTransport(nil, false)
		sharedClient = &http.Client{Transport: sharedTransport}
	})
}

func initPublic() {
	publicTransportOnce.Do(func() {
		publicTransport = newTransport(newPublicResolver(), true)
		publicClient = &http.Client{Transport: publicTransport}
	})
}

// Shared returns the process-wide client for calls that do not need a
// client-specific timeout, cookie jar, or redirect policy. The client and its
// transport are safe for concurrent use and must not be mutated after use.
func Shared() *http.Client {
	initShared()
	return sharedClient
}

// New returns a client wrapper using the process-wide transport. Each call
// returns a distinct wrapper so callers can safely attach client-local state
// by cloning it, while all wrappers still share the transport pool.
func New() *http.Client {
	return Clone(Shared())
}

// NewWithTimeout returns a client wrapper using the process-wide transport and
// the supplied whole-request timeout. A zero timeout leaves streaming bodies
// uncapped by the client; callers should use request contexts for cancellation.
func NewWithTimeout(timeout time.Duration) *http.Client {
	client := New()
	client.Timeout = timeout
	return client
}

// NewPublic returns a client for public web retrieval. It uses the
// application-owned Cloudflare/Google resolver and refuses non-public
// destination addresses, including addresses returned by redirects.
func NewPublic() *http.Client {
	initPublic()
	return Clone(publicClient)
}

// Clone returns a shallow client copy that keeps the central transport. This
// is useful for per-client Jar, CheckRedirect, or Timeout settings. The
// returned client must be configured before it is used concurrently.
func Clone(base *http.Client) *http.Client {
	if base == nil {
		return New()
	}
	clone := *base
	if clone.Transport == nil {
		clone.Transport = Shared().Transport
	}
	return &clone
}

// CloseIdleConnections releases idle connections owned by the shared
// transport. Call this during application shutdown or after a network/proxy
// configuration change, never after each request.
func CloseIdleConnections() {
	initShared()
	sharedTransport.CloseIdleConnections()
	initPublic()
	publicTransport.CloseIdleConnections()
}
