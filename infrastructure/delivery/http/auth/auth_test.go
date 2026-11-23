package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	appauth "github.com/jahrulnr/nusashell/application/auth"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	repoauth "github.com/jahrulnr/nusashell/infrastructure/repository/auth"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	"github.com/jahrulnr/nusashell/infrastructure/service/password"
	"github.com/jahrulnr/nusashell/migration"
)

func router(t *testing.T, mode enum.AuthMode) (*gin.Engine, *appauth.AuthApp) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"), migration.Files)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	app, err := appauth.New(repoauth.New(db), password.Bcrypt{Cost: 4}, mode, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SetPassword(context.Background(), "correct-horse"); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(Middleware(app))
	api := r.Group("/api")
	Register(api, app)
	api.GET("/health", func(c *gin.Context) { c.Status(200) })
	api.GET("/things", func(c *gin.Context) { c.Status(200) })
	api.POST("/things", func(c *gin.Context) { c.Status(201) })
	r.GET("/", func(c *gin.Context) { c.Status(200) })
	return r, app
}

func do(r *gin.Engine, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRequiredLoginGatesAPI(t *testing.T) {
	r, _ := router(t, enum.AuthModeOn)
	for _, p := range []string{"/", "/api/health", "/api/auth/status"} {
		if rec := do(r, "GET", p, "", nil); rec.Code != 200 {
			t.Fatalf("public %s = %d", p, rec.Code)
		}
	}
	if rec := do(r, "GET", "/api/things", "", nil); rec.Code != 401 {
		t.Fatalf("no session = %d, want 401", rec.Code)
	}
	if rec := do(r, "POST", "/api/auth/login", `{"password":"nope-nope"}`, nil); rec.Code != 401 {
		t.Fatalf("wrong password = %d", rec.Code)
	}
	rec := do(r, "POST", "/api/auth/login", `{"password":"correct-horse"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("login = %d %s", rec.Code, rec.Body)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", cookie)
	}
	var body statusBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.CSRFToken == "" || strings.Contains(rec.Body.String(), cookie.Value) {
		t.Fatalf("login body = %s", rec.Body)
	}
	jar := map[string]string{"Cookie": CookieName + "=" + cookie.Value}
	if rec := do(r, "GET", "/api/things", "", jar); rec.Code != 200 {
		t.Fatalf("GET with session = %d", rec.Code)
	}
	if rec := do(r, "POST", "/api/things", "", jar); rec.Code != 403 {
		t.Fatalf("POST without csrf = %d, want 403", rec.Code)
	}
	jar[CSRFHeader] = body.CSRFToken
	if rec := do(r, "POST", "/api/things", "", jar); rec.Code != 201 {
		t.Fatalf("POST with csrf = %d", rec.Code)
	}
	status := do(r, "GET", "/api/auth/status", "", jar)
	if !strings.Contains(status.Body.String(), body.CSRFToken) {
		t.Fatalf("status for a live session must return its csrf token: %s", status.Body)
	}
	if rec := do(r, "POST", "/api/auth/logout", "", jar); rec.Code != 204 {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := do(r, "GET", "/api/things", "", jar); rec.Code != 401 {
		t.Fatalf("after logout = %d, want 401", rec.Code)
	}
}

func TestNoLoginModePassesEverything(t *testing.T) {
	r, _ := router(t, enum.AuthModeOff)
	if rec := do(r, "POST", "/api/things", "", nil); rec.Code != 201 {
		t.Fatalf("POST without login = %d", rec.Code)
	}
	if rec := do(r, "GET", "/api/auth/status", "", nil); !strings.Contains(rec.Body.String(), `"required":false`) {
		t.Fatalf("status = %s", rec.Body)
	}
}

// The password route sits behind the session and CSRF check, and a change
// hands this page a new session while the old cookie stops working.
func TestChangePasswordRoute(t *testing.T) {
	r, _ := router(t, enum.AuthModeOn)
	body := `{"current":"correct-horse","new":"battery-staple"}`
	if rec := do(r, "POST", "/api/owner/password", body, nil); rec.Code != 401 {
		t.Fatalf("no session = %d, want 401", rec.Code)
	}
	rec := do(r, "POST", "/api/auth/login", `{"password":"correct-horse"}`, nil)
	var login statusBody
	_ = json.Unmarshal(rec.Body.Bytes(), &login)
	jar := map[string]string{"Cookie": rec.Result().Cookies()[0].Name + "=" + rec.Result().Cookies()[0].Value}
	if rec := do(r, "POST", "/api/owner/password", body, jar); rec.Code != 403 {
		t.Fatalf("no csrf = %d, want 403", rec.Code)
	}
	jar[CSRFHeader] = login.CSRFToken
	if rec := do(r, "POST", "/api/owner/password", `{"current":"wrong-one","new":"battery-staple"}`, jar); rec.Code != 403 {
		t.Fatalf("wrong current = %d, want 403", rec.Code)
	}
	rec = do(r, "POST", "/api/owner/password", body, jar)
	if rec.Code != 200 {
		t.Fatalf("change = %d %s", rec.Code, rec.Body)
	}
	var changed statusBody
	_ = json.Unmarshal(rec.Body.Bytes(), &changed)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || changed.CSRFToken == "" || changed.CSRFToken == login.CSRFToken || !changed.HasPassword {
		t.Fatalf("change answer = %s, cookies %v", rec.Body, cookies)
	}
	if rec := do(r, "GET", "/api/things", "", jar); rec.Code != 401 {
		t.Fatalf("the old session = %d, want 401", rec.Code)
	}
	fresh := map[string]string{"Cookie": CookieName + "=" + cookies[0].Value}
	if rec := do(r, "GET", "/api/things", "", fresh); rec.Code != 200 {
		t.Fatalf("the new session = %d", rec.Code)
	}
}
