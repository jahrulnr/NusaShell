package websocket

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	appbrowser "github.com/jahrulnr/nusashell/application/browser"
	dtransport "github.com/jahrulnr/nusashell/infrastructure/delivery/transport"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// BrowserService is the browser use case the live view needs.
type BrowserService interface {
	Watch(ctx context.Context, convID string) (<-chan dservice.BrowserFrame, appbrowser.ViewerDrive, error)
}

// BrowserHandler serves a thread's live browser view: JPEG frames as
// binary messages, state as JSON, and the viewer's input back.
type BrowserHandler struct {
	svc      BrowserService
	origin   *Handler // its origin check
	upgrader websocket.Upgrader
}

// NewBrowserHandler wires the live view with the same Origin rule as the
// conversation socket.
func NewBrowserHandler(svc BrowserService, authorities []string) *BrowserHandler {
	h := &BrowserHandler{svc: svc, origin: &Handler{authorities: append([]string(nil), authorities...)}}
	h.upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return h.origin.originAllowed(r.Header.Get("Origin")) }}
	return h
}

// Messages from server to viewer.
type browserMessage struct {
	Type     string `json:"type"` // hello, state, keyboard, refused, closed
	Viewer   string `json:"viewer,omitempty"`
	Editable bool   `json:"editable,omitempty"`
	Message  string `json:"message,omitempty"`
	*dservice.BrowserState
}

const inputQueue = 64

// Serve is the gin handler for GET /api/conversations/:id/browser/ws.
func (h *BrowserHandler) Serve(c *gin.Context) {
	if !h.origin.originAllowed(c.Request.Header.Get("Origin")) {
		c.Header("Content-Type", "application/problem+json")
		c.AbortWithStatusJSON(http.StatusForbidden, dtransport.NewProblem(http.StatusForbidden, "forbidden_origin", "websocket upgrade requires an exact Origin"))
		return
	}
	convID := c.Param("id")
	ctx, cancel := context.WithCancel(context.WithoutCancel(c.Request.Context()))
	frames, drive, err := h.svc.Watch(ctx, convID)
	if err != nil {
		cancel()
		p := dtransport.NewProblem(apperror.As(err).StatusCode(), "not_found", err.Error())
		c.Header("Content-Type", "application/problem+json")
		c.AbortWithStatusJSON(apperror.As(err).StatusCode(), p)
		return
	}
	ws, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		cancel()
		return
	}
	defer ws.Close()
	defer cancel()
	viewer := rand.Text()[:12]

	// One writer: frames and messages share an ordered path.
	out := make(chan browserMessage, 8)
	go func() {
		defer cancel()
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		write := func(kind int, b []byte) bool {
			_ = ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
			return ws.WriteMessage(kind, b) == nil
		}
		send := func(m browserMessage) bool {
			b, _ := json.Marshal(m)
			return write(websocket.TextMessage, b)
		}
		if !send(browserMessage{Type: "hello", Viewer: viewer}) {
			return
		}
		for {
			select {
			case m := <-out:
				if !send(m) {
					return
				}
			case f, ok := <-frames:
				if !ok {
					send(browserMessage{Type: "closed"})
					_ = ws.Close()
					return
				}
				if f.State != nil {
					if !send(browserMessage{Type: "state", BrowserState: f.State}) {
						return
					}
				}
				if len(f.JPEG) > 0 && !write(websocket.BinaryMessage, f.JPEG) {
					return
				}
			case <-ping.C:
				if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Input runs in order on one worker; a burst of pointer moves keeps
	// only the newest while the queue is full.
	inputs := make(chan appbrowser.Input, inputQueue)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case in := <-inputs:
				editable, err := drive(ctx, viewer, in)
				var m browserMessage
				switch {
				case err != nil:
					m = browserMessage{Type: "refused", Message: apperror.As(err).Error()}
				case in.Type == "touch" && in.Phase == "touchEnd":
					m = browserMessage{Type: "keyboard", Editable: editable}
				default:
					continue
				}
				select {
				case out <- m:
				default:
				}
			}
		}
	}()
	ws.SetReadLimit(64 << 10)
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			// A viewer that leaves hands the input back.
			_, _ = drive(context.Background(), viewer, appbrowser.Input{Type: "release"})
			return
		}
		var in appbrowser.Input
		if json.Unmarshal(raw, &in) != nil {
			continue
		}
		select {
		case inputs <- in:
		default:
			if in.Type != "move" {
				inputs <- in
			}
		}
	}
}
