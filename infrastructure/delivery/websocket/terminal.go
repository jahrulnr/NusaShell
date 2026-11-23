package websocket

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	dtransport "github.com/jahrulnr/nusashell/infrastructure/delivery/transport"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// TerminalService starts the owner's terminal for a thread.
type TerminalService interface {
	OpenTerminal(ctx context.Context, p appconversation.Place, dir string, cols, rows int) (dservice.Shell, string, error)
}

// SignInService runs an agent's interactive sign-in on a terminal.
// *appagent.AgentApp satisfies it.
type SignInService interface {
	SignIn(ctx context.Context, id, methodID string, cols, rows int) (dservice.Shell, error)
}

// TerminalHandler serves the owner's terminals — a thread's shell and an
// agent's sign-in: output as binary messages; input, resizes and the exit
// as JSON.
type TerminalHandler struct {
	svc      TerminalService
	agents   SignInService
	origin   *Handler
	upgrader websocket.Upgrader
}

func NewTerminalHandler(svc TerminalService, agents SignInService, authorities []string) *TerminalHandler {
	h := &TerminalHandler{svc: svc, agents: agents, origin: &Handler{authorities: append([]string(nil), authorities...)}}
	h.upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return h.origin.originAllowed(r.Header.Get("Origin")) }}
	return h
}

// terminalMessage is both directions' JSON: the viewer sends input and
// resize; the server sends ready (with the folder) and exit.
type terminalMessage struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	OK      bool   `json:"ok,omitempty"`
	Message string `json:"message,omitempty"`
}

func (h *TerminalHandler) problem(c *gin.Context, status int, code, detail string) {
	c.Header("Content-Type", "application/problem+json")
	c.AbortWithStatusJSON(status, dtransport.NewProblem(status, code, detail))
}

// Serve is the gin handler for GET /api/conversations/:id/terminal and
// GET /api/terminal (a draft's projectId or workspace).
func (h *TerminalHandler) Serve(c *gin.Context) {
	if !h.origin.originAllowed(c.Request.Header.Get("Origin")) {
		h.problem(c, http.StatusForbidden, "forbidden_origin", "websocket upgrade requires an exact Origin")
		return
	}
	p := appconversation.Place{
		ConversationID: c.Param("id"),
		ProjectID:      strings.TrimSpace(c.Query("projectId")),
		Workspace:      strings.TrimSpace(c.Query("workspace")),
	}
	for _, id := range []string{p.ConversationID, p.ProjectID} {
		if id != "" && valueobject.ValidID(id) != nil {
			h.problem(c, http.StatusBadRequest, "invalid_argument", "invalid id")
			return
		}
	}
	cols, _ := strconv.Atoi(c.Query("cols"))
	rows, _ := strconv.Atoi(c.Query("rows"))
	ctx, cancel := context.WithCancel(context.WithoutCancel(c.Request.Context()))
	defer cancel()
	sh, cwd, err := h.svc.OpenTerminal(ctx, p, c.Query("cwd"), clampSize(cols, 80), clampSize(rows, 24))
	if err != nil {
		status := apperror.As(err).StatusCode()
		h.problem(c, status, "invalid_argument", apperror.As(err).Error())
		return
	}
	h.pump(ctx, cancel, c, sh, terminalMessage{Type: "ready", Cwd: cwd}, "The shell")
}

// ServeSignIn is the gin handler for GET /api/agents/:id/sign-in: the
// agent's terminal sign-in method methodId, run until it exits.
func (h *TerminalHandler) ServeSignIn(c *gin.Context) {
	if !h.origin.originAllowed(c.Request.Header.Get("Origin")) {
		h.problem(c, http.StatusForbidden, "forbidden_origin", "websocket upgrade requires an exact Origin")
		return
	}
	cols, _ := strconv.Atoi(c.Query("cols"))
	rows, _ := strconv.Atoi(c.Query("rows"))
	ctx, cancel := context.WithCancel(context.WithoutCancel(c.Request.Context()))
	defer cancel()
	sh, err := h.agents.SignIn(ctx, c.Param("id"), c.Query("methodId"), clampSize(cols, 80), clampSize(rows, 24))
	if err != nil {
		ae := apperror.As(err)
		h.problem(c, ae.StatusCode(), "invalid_argument", ae.Error())
		return
	}
	h.pump(ctx, cancel, c, sh, terminalMessage{Type: "ready"}, "The sign-in")
}

// pump upgrades the request and joins the socket to sh until either side
// ends; what names the program in its exit message.
func (h *TerminalHandler) pump(ctx context.Context, cancel context.CancelFunc, c *gin.Context, sh dservice.Shell, ready terminalMessage, what string) {
	defer sh.Close()
	ws, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(1 << 20)

	var wmu sync.Mutex
	write := func(kind int, b []byte) bool {
		wmu.Lock()
		defer wmu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return ws.WriteMessage(kind, b) == nil
	}
	send := func(m terminalMessage) bool {
		b, _ := json.Marshal(m)
		return write(websocket.TextMessage, b)
	}
	if !send(ready) {
		return
	}

	// Output streams until the shell closes its side; then its exit is
	// reported and the socket ends.
	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := sh.Read(buf)
			if n > 0 && !write(websocket.BinaryMessage, buf[:n]) {
				return
			}
			if err != nil {
				break
			}
		}
		werr := sh.Wait()
		m := terminalMessage{Type: "exit", OK: werr == nil, Message: what + " exited."}
		if werr != nil {
			m.Message = what + " ended: " + werr.Error()
		}
		send(m)
		wmu.Lock()
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		wmu.Unlock()
	}()
	go func() {
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ping.C:
				wmu.Lock()
				err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				wmu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		_ = ws.Close() // unblocks the read below
	}()
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.BinaryMessage {
			if _, err := sh.Write(data); err != nil {
				return
			}
			continue
		}
		var m terminalMessage
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "input":
			if _, err := sh.Write([]byte(m.Data)); err != nil {
				return
			}
		case "resize":
			_ = sh.Resize(clampSize(m.Cols, 80), clampSize(m.Rows, 24))
		}
	}
}

func clampSize(n, def int) int {
	switch {
	case n <= 0:
		return def
	case n > 1000:
		return 1000
	}
	return n
}
