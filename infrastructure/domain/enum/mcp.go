package enum

// MCPTransport is how an MCP server is reached: a child process on stdio,
// or a URL over streamable HTTP or the older SSE transport.
type MCPTransport string

const (
	MCPStdio MCPTransport = "stdio"
	MCPHTTP  MCPTransport = "http"
	MCPSSE   MCPTransport = "sse"
)

func (t MCPTransport) String() string { return string(t) }

// ParseMCPTransport reads a transport name; empty is stdio.
func ParseMCPTransport(s string) (MCPTransport, bool) {
	switch MCPTransport(s) {
	case "", MCPStdio:
		return MCPStdio, true
	case MCPHTTP, MCPSSE:
		return MCPTransport(s), true
	}
	return "", false
}

// Remote reports a URL transport.
func (t MCPTransport) Remote() bool { return t == MCPHTTP || t == MCPSSE }
