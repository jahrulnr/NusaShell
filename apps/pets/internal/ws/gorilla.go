package ws

import (
	"context"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// GorillaDialer adapts gorilla/websocket.Dialer to the ws.Dialer interface.
type GorillaDialer struct {
	HandshakeTimeout time.Duration
}

// NewGorillaDialer returns a Dialer backed by gorilla/websocket.
func NewGorillaDialer() *GorillaDialer {
	return &GorillaDialer{HandshakeTimeout: 5 * time.Second}
}

// Dial connects to url and returns a Conn.
func (d *GorillaDialer) Dial(ctx context.Context, url string) (Conn, error) {
	gd := websocket.Dialer{
		HandshakeTimeout: d.HandshakeTimeout,
	}
	conn, _, err := gd.DialContext(ctx, url, http.Header{})
	if err != nil {
		return nil, err
	}
	return &gorillaConn{c: conn}, nil
}

type gorillaConn struct {
	c *websocket.Conn
}

func (g *gorillaConn) ReadMessage() (int, []byte, error) {
	mt, data, err := g.c.ReadMessage()
	return mt, data, err
}

func (g *gorillaConn) Close() error {
	return g.c.Close()
}
