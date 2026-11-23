// Package auth is the WebUI login boundary: the session cookie, the login
// and logout routes, and the middleware that admits requests. When the
// server requires no login every request passes.
//
// The cookie is HttpOnly, so page script never sees the session token;
// the page holds the separate CSRF token from the login or status answer
// and sends it as X-CSRF-Token on every state-changing request.
package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	appauth "github.com/jahrulnr/nusashell/application/auth"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// CookieName is the session cookie.
const CookieName = "nusashell_session"

// CSRFHeader carries the session's CSRF token on unsafe requests.
const CSRFHeader = "X-CSRF-Token"

// Service is the login use case. *appauth.AuthApp satisfies it.
type Service interface {
	Required() bool
	HasPassword(ctx context.Context) (bool, error)
	Login(ctx context.Context, password, client string) (token, csrf string, err error)
	ChangePassword(ctx context.Context, current, next, client string) (token, csrf string, err error)
	Authenticate(ctx context.Context, token string) (valueobject.Session, error)
	Logout(ctx context.Context, token string) error
}

// Register mounts /api/auth routes. A nil service registers nothing.
func Register(api *gin.RouterGroup, svc Service) {
	if svc == nil {
		return
	}
	s := &server{svc: svc}
	api.GET("/auth/status", s.status)
	api.POST("/auth/login", s.login)
	api.POST("/auth/logout", s.logout)
	// Outside /auth/, so the middleware asks this page's session and CSRF
	// token before the current password is even checked.
	api.POST("/owner/password", s.password)
}

// Middleware admits a request when no login is required, when it is
// public (page assets, health, the auth routes), or when it carries a live
// session — plus, for unsafe methods, that session's CSRF token.
func Middleware(svc Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil || !svc.Required() || public(c.Request.URL.Path) {
			c.Next()
			return
		}
		token, _ := c.Cookie(CookieName)
		sess, err := svc.Authenticate(c.Request.Context(), token)
		if err != nil {
			respond(c, err)
			c.Abort()
			return
		}
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !appauth.CheckCSRF(sess, c.GetHeader(CSRFHeader)) {
				transport.WriteProblem(c, http.StatusForbidden, "csrf", "missing or wrong "+CSRFHeader+" header")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

// public paths need no session: everything outside /api is the page and
// its assets (the login screen must load), the auth routes themselves, and
// the agents' MCP relay, which carries its own thread token.
func public(path string) bool {
	if !strings.HasPrefix(path, "/api/") && path != "/api" {
		return true
	}
	return path == "/api/health" || strings.HasPrefix(path, "/api/auth/") || strings.HasPrefix(path, "/api/mcp/")
}

// respond renders a login failure. The shared classification treats 401
// and 403 as a dead upstream credential (502); here they are the caller's
// own missing login or wrong password, so they cross as they are.
func respond(c *gin.Context, err error) {
	switch ae := apperror.As(err); ae.StatusCode() {
	case http.StatusUnauthorized:
		transport.WriteProblem(c, http.StatusUnauthorized, "unauthenticated", ae.Error())
		return
	case http.StatusForbidden:
		transport.WriteProblem(c, http.StatusForbidden, "wrong_password", ae.Error())
		return
	case http.StatusTooManyRequests:
		transport.WriteProblem(c, http.StatusTooManyRequests, "too_many_attempts", ae.Error())
		return
	}
	transport.RespondError(c, err)
}

type server struct{ svc Service }

type statusBody struct {
	Required      bool   `json:"required"`
	Authenticated bool   `json:"authenticated"`
	CSRFToken     string `json:"csrfToken,omitempty"`
	// HasPassword tells a signed-in page whether changing the password
	// asks for the current one.
	HasPassword bool `json:"hasPassword,omitempty"`
}

// status tells the page whether to show the login screen, and hands a
// reloaded page the CSRF token of the session its cookie names.
func (s *server) status(c *gin.Context) {
	has, _ := s.svc.HasPassword(c.Request.Context())
	if !s.svc.Required() {
		c.JSON(200, statusBody{Required: false, Authenticated: true, HasPassword: has})
		return
	}
	token, _ := c.Cookie(CookieName)
	sess, err := s.svc.Authenticate(c.Request.Context(), token)
	if err != nil {
		c.JSON(200, statusBody{Required: true})
		return
	}
	c.JSON(200, statusBody{Required: true, Authenticated: true, CSRFToken: sess.CSRF(), HasPassword: has})
}

type loginBody struct {
	Password string `json:"password"`
}

func (s *server) login(c *gin.Context) {
	var body loginBody
	if !transport.DecodeJSON(c, &body) {
		return
	}
	token, csrf, err := s.svc.Login(c.Request.Context(), body.Password, c.ClientIP())
	if err != nil {
		respond(c, err)
		return
	}
	setSession(c, token)
	c.JSON(200, statusBody{Required: true, Authenticated: true, CSRFToken: csrf, HasPassword: true})
}

// setSession hands the browser its session cookie.
func setSession(c *gin.Context, token string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: CookieName, Value: token, Path: "/",
		MaxAge:   int(config.Session{}.TTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https"),
	})
}

type passwordBody struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// password changes the owner password. Every other session ends; this
// page gets a new session (and CSRF token) when logins are required.
func (s *server) password(c *gin.Context) {
	var body passwordBody
	if !transport.DecodeJSON(c, &body) {
		return
	}
	token, csrf, err := s.svc.ChangePassword(c.Request.Context(), body.Current, body.New, c.ClientIP())
	if err != nil {
		respond(c, err)
		return
	}
	if !s.svc.Required() {
		c.JSON(200, statusBody{Authenticated: true, HasPassword: true})
		return
	}
	setSession(c, token)
	c.JSON(200, statusBody{Required: true, Authenticated: true, CSRFToken: csrf, HasPassword: true})
}

func (s *server) logout(c *gin.Context) {
	token, _ := c.Cookie(CookieName)
	if err := s.svc.Logout(c.Request.Context(), token); err != nil {
		transport.RespondError(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	c.Status(204)
}
