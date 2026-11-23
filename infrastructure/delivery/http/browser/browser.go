// Package httpbrowser serves a thread's browser state, the browser
// profiles, closing a session, and installing the browser itself. The
// live view is a WebSocket (see delivery/websocket).
package httpbrowser

import (
	"context"
	"encoding/json"

	"strconv"
	"strings"

	appbrowser "github.com/jahrulnr/nusashell/application/browser"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"net/http"

	"github.com/gin-gonic/gin"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// Service is the browser use case. *appbrowser.App satisfies it.
type Service interface {
	Start(ctx context.Context, convID, url string) error
	Status(convID string) (dservice.BrowserState, bool)
	Profiles() []string
	Close(convID string)
	PageLogins(ctx context.Context, convID string) ([]appbrowser.LoginView, error)
	Logins(ctx context.Context, profile string) ([]appbrowser.LoginView, error)
	DeleteLogin(ctx context.Context, id string) error
	PageConsole(ctx context.Context, convID string) ([]appbrowser.ConsoleEntry, error)
	RunScript(ctx context.Context, convID, expression string) (json.RawMessage, error)
	Visited(ctx context.Context, profile, query string, limit int) ([]drepo.BrowserVisit, error)
	Recordings(convID string) []drepo.RecordingMeta
	Recording(convID, id string) (drepo.RecordingReader, error)
	Annotate(ctx context.Context, convID string, in appbrowser.AnnotateInput) (appbrowser.AnnotationPick, error)
	PreviewAnnotate(ctx context.Context, convID string, x, y float64) (dservice.BrowserMarkPreview, error)
	ClearAnnotate(ctx context.Context, convID string)
	InstallOffer() (dservice.BrowserInstallOffer, error)
	Install(ctx context.Context, method string) (appbrowser.InstallResult, error)
}

// Register mounts the routes; nil leaves them unregistered.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/conversations/:id/browser", s.status)
	api.POST("/conversations/:id/browser", s.start)
	api.GET("/browser/profiles", s.profiles)
	api.GET("/browser/install", s.installOffer)
	api.POST("/browser/install", s.install)
	api.GET("/conversations/:id/browser/logins", s.pageLogins)
	api.GET("/browser/logins", s.logins)
	api.GET("/browser/history", s.history)
	api.GET("/conversations/:id/browser/console", s.console)
	api.POST("/conversations/:id/browser/eval", s.eval)
	api.DELETE("/browser/logins/:id", s.deleteLogin)
	api.GET("/conversations/:id/recordings", s.recordings)
	api.GET("/conversations/:id/recordings/:rec/summary", s.summary)
	api.GET("/conversations/:id/recordings/:rec/events", s.events)
	api.DELETE("/conversations/:id/browser", s.close)
	api.POST("/conversations/:id/browser/annotate", s.annotate)
	api.POST("/conversations/:id/browser/annotate/preview", s.previewAnnotate)
	api.POST("/conversations/:id/browser/annotate/clear", s.clearAnnotate)
}

type server struct{ svc Service }

func (s *server) status(c *gin.Context) {
	st, open := s.svc.Status(c.Param("id"))
	if !open {
		c.JSON(http.StatusOK, gin.H{"open": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"open": true, "page": st.Page, "device": st.Device, "lease": st.Lease, "profile": st.Profile})
}

// start opens the thread's browser, on the address given if any, and
// answers as status does.
func (s *server) start(c *gin.Context) {
	var body struct {
		URL string `json:"url"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	if err := s.svc.Start(c.Request.Context(), c.Param("id"), body.URL); err != nil {
		transport.RespondError(c, err)
		return
	}
	s.status(c)
}

func (s *server) profiles(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": s.svc.Profiles()})
}

// pageLogins lists the saved logins for the page the thread's tab is on.
func (s *server) pageLogins(c *gin.Context) {
	list, err := s.svc.PageLogins(c.Request.Context(), c.Param("id"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func (s *server) logins(c *gin.Context) {
	list, err := s.svc.Logins(c.Request.Context(), c.Query("profile"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// console answers what the open page logged.
func (s *server) console(c *gin.Context) {
	list, err := s.svc.PageConsole(c.Request.Context(), c.Param("id"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// eval runs the owner's JavaScript in the open page.
func (s *server) eval(c *gin.Context) {
	var body struct {
		Expression string `json:"expression"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	v, err := s.svc.RunScript(c.Request.Context(), c.Param("id"), body.Expression)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"value": v})
}

// history suggests pages a profile visited, for the address bar.
func (s *server) history(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	list, err := s.svc.Visited(c.Request.Context(), c.Query("profile"), c.Query("q"), limit)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	out := make([]gin.H, 0, len(list))
	for _, v := range list {
		out = append(out, gin.H{"url": v.URL, "title": v.Title, "visits": v.Visits, "visitedAt": v.VisitedAt.UnixMilli()})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

func (s *server) deleteLogin(c *gin.Context) {
	if err := s.svc.DeleteLogin(c.Request.Context(), c.Param("id")); err != nil {
		transport.RespondError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *server) close(c *gin.Context) {
	s.svc.Close(c.Param("id"))
	c.Status(http.StatusNoContent)
}

func (s *server) recordings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": s.svc.Recordings(c.Param("id"))})
}

// summary is the finished recording's summary as plain text.
func (s *server) summary(c *gin.Context) {
	r, err := s.svc.Recording(c.Param("id"), c.Param("rec"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	text, err := r.Summary()
	if err != nil {
		text = "The summary is written when the recording stops."
	}
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, text)
}

// events pages the recording's timeline: ?after=&limit=&types=a,b.
func (s *server) events(c *gin.Context) {
	r, err := s.svc.Recording(c.Param("id"), c.Param("rec"))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	after, _ := strconv.ParseInt(c.Query("after"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var types []string
	if t := c.Query("types"); t != "" {
		types = strings.Split(t, ",")
	}
	events, next, done := r.Events(after, limit, types)
	if events == nil {
		events = []drepo.RecordingEvent{}
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "next": next, "done": done})
}

// annotate marks an element (x, y), a drawn region (x, y, width,
// height), or the whole page on the live view.
func (s *server) annotate(c *gin.Context) {
	var in appbrowser.AnnotateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		transport.WriteProblem(c, http.StatusBadRequest, "invalid_argument", "invalid annotate payload")
		return
	}
	pick, err := s.svc.Annotate(c.Request.Context(), c.Param("id"), in)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusCreated, pick)
}

func (s *server) previewAnnotate(c *gin.Context) {
	var in struct{ X, Y float64 }
	if err := c.ShouldBindJSON(&in); err != nil {
		transport.WriteProblem(c, http.StatusBadRequest, "invalid_argument", "invalid preview payload")
		return
	}
	pv, err := s.svc.PreviewAnnotate(c.Request.Context(), c.Param("id"), in.X, in.Y)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, pv)
}

func (s *server) clearAnnotate(c *gin.Context) {
	s.svc.ClearAnnotate(c.Request.Context(), c.Param("id"))
	c.Status(http.StatusNoContent)
}

// installOffer reports the browser executable, or the ways to get one.
func (s *server) installOffer(c *gin.Context) {
	offer, err := s.svc.InstallOffer()
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, offer)
}

// install runs one offered method and answers when it is done; the method
// id picks from a fixed list, never a command.
func (s *server) install(c *gin.Context) {
	var body struct {
		Method string `json:"method"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	res, err := s.svc.Install(c.Request.Context(), strings.TrimSpace(body.Method))
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}
