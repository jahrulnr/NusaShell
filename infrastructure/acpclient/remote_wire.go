package acpclient

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const (
	writeTimeout = 30 * time.Second
	readTimeout  = 5 * time.Minute
)

// remoteWire wraps a WebSocket connection to a cloud ACP agent. The
// JSON-RPC 2.0 protocol is the same as stdio — only the transport differs.
// Messages are newline-delimited JSON over stdio; over WebSocket each
// message is one Text frame.
type remoteWire struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	cancel context.CancelFunc
}

// DialRemote opens a WebSocket connection to url and returns a wire for
// ACP JSON-RPC. headers are optional HTTP headers (e.g., Authorization).
func DialRemote(ctx context.Context, url string, headers http.Header) (wire, error) {
	if url == "" {
		return nil, fmt.Errorf("acp remote url is empty")
	}
	dialCtx, cancel := context.WithCancel(ctx)
	conn, _, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("dial remote acp %s: %w", url, err)
	}
	w := &remoteWire{
		conn:   conn,
		cancel: cancel,
	}
	go func() {
		<-dialCtx.Done()
		_ = conn.Close(websocket.StatusNormalClosure, "context cancelled")
	}()
	return w, nil
}

func (w *remoteWire) Write(body []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return w.conn.Write(ctx, websocket.MessageText, body)
}

func (w *remoteWire) Read() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	_, body, err := w.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (w *remoteWire) Close() error {
	w.cancel()
	return w.conn.Close(websocket.StatusNormalClosure, "connection closed")
}
