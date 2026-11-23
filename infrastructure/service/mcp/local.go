package mcp

import (
	"context"
	"net"
)

// Local runs s in this process and connects a client to it over an
// in-memory pipe — the same protocol an external server speaks, without a
// child process. Closing the client stops the server.
func Local(ctx context.Context, s Server, clientName, clientVersion string) (*Client, error) {
	a, b := net.Pipe()
	go func() {
		_ = Serve(context.WithoutCancel(ctx), b, b, s)
		b.Close()
	}()
	return Connect(ctx, a, clientName, clientVersion)
}
