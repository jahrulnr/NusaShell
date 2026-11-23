package httpbrowser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// fakeService implements only what these routes call.
type fakeService struct {
	Service
	started  []string
	startErr error
	state    dservice.BrowserState
	open     bool
}

func (f *fakeService) Start(_ context.Context, convID, url string) error {
	f.started = append(f.started, convID+" "+url)
	if f.startErr == nil {
		f.open = true
	}
	return f.startErr
}

func (f *fakeService) Status(string) (dservice.BrowserState, bool) { return f.state, f.open }

func post(t *testing.T, svc Service, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r.Group("/api"), svc)
	req := httptest.NewRequest(http.MethodPost, "/api/conversations/c_1/browser", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestStartAnswersLikeStatus(t *testing.T) {
	svc := &fakeService{state: dservice.BrowserState{
		Page:    dservice.BrowserPage{URL: "https://example.com/", Title: "Example"},
		Profile: "default",
	}}
	w := post(t, svc, `{"url":"example.com"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if len(svc.started) != 1 || svc.started[0] != "c_1 example.com" {
		t.Fatalf("started = %v", svc.started)
	}
	for _, want := range []string{`"open":true`, `"url":"https://example.com/"`, `"profile":"default"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body lacks %s: %s", want, w.Body)
		}
	}
}

func TestStartWithoutAnAddressOpensABlankTab(t *testing.T) {
	svc := &fakeService{}
	if w := post(t, svc, `{}`); w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if len(svc.started) != 1 || svc.started[0] != "c_1 " {
		t.Fatalf("started = %v", svc.started)
	}
}

func TestStartRefusalsAreProblems(t *testing.T) {
	svc := &fakeService{startErr: apperror.New(400, "url must be an http or https address", nil)}
	w := post(t, svc, `{"url":"file:///etc/passwd"}`)
	if w.Code != 400 || !strings.Contains(w.Header().Get("Content-Type"), "problem+json") {
		t.Fatalf("status = %d, type = %q", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "http or https") {
		t.Fatalf("detail lost: %s", w.Body)
	}

	// An unknown field is not ignored.
	if w := post(t, &fakeService{}, `{"url":"x.com","profile":"p"}`); w.Code != 400 {
		t.Fatalf("unknown field: status = %d", w.Code)
	}
}

type historyService struct {
	Service
	asked string
}

func (h *historyService) Visited(_ context.Context, profile, query string, limit int) ([]drepo.BrowserVisit, error) {
	h.asked = profile + "|" + query + "|" + strconv.Itoa(limit)
	return []drepo.BrowserVisit{{URL: "https://example.com/", Title: "Example", Visits: 3, VisitedAt: time.UnixMilli(42)}}, nil
}

func TestHistorySuggestions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &historyService{}
	r := gin.New()
	Register(r.Group("/api"), svc)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/browser/history?q=ex&profile=work&limit=5", nil))
	if w.Code != 200 || svc.asked != "work|ex|5" {
		t.Fatalf("status %d, asked %q", w.Code, svc.asked)
	}
	want := `{"data":[{"title":"Example","url":"https://example.com/","visitedAt":42,"visits":3}]}`
	if w.Body.String() != want {
		t.Fatalf("body = %s", w.Body)
	}
}
