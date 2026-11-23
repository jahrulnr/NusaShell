package appbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

const (
	netDefaultLimit = 50
	netMaxLimit     = 300
	waitPoll        = 50 * time.Millisecond
	waitDefault     = 10 * time.Second
	waitMax         = 30 * time.Second
)

// jsonText renders v for a tool result.
func jsonText(v any) string {
	raw, _ := json.MarshalIndent(v, "", " ")
	return string(raw)
}

// invalid maps an adapter's rule error to a 400 the caller reads.
func invalid(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "invalid route") {
		return apperror.New(400, err.Error(), err)
	}
	return err
}

// Route intercepts requests matching a rule.
func (a *App) Route(ctx context.Context, convID string, rule dservice.BrowserRouteRule) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	st, err := s.AddRoute(rule)
	if err != nil {
		return Result{}, invalid(err)
	}
	return done(s, jsonText(map[string]any{"rule": st}))
}

// Routes lists the request rules, WebSocket rules, and init scripts.
func (a *App) Routes(ctx context.Context, convID string) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	return done(s, jsonText(map[string]any{"rules": s.Routes(), "webSocketRules": s.WSRoutes(), "initScripts": s.InitScripts()}))
}

// ClearRoutes removes one rule, or every one when id is empty.
func (a *App) ClearRoutes(ctx context.Context, convID, id string) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	n := s.ClearRoutes(id)
	return done(s, jsonText(map[string]any{"cleared": n, "rules": s.Routes()}))
}

// RouteWS points WebSockets the page opens at another endpoint. reload
// (default true) reloads the page to catch sockets opened at boot.
func (a *App) RouteWS(ctx context.Context, convID string, rule dservice.BrowserWSRouteRule, reload *bool) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	re := reload == nil || *reload
	st, err := s.AddWSRoute(ctx, rule, re)
	if err != nil {
		return Result{}, invalid(err)
	}
	return done(s, jsonText(map[string]any{"route": st, "reload": re, "webSocketRules": s.WSRoutes()}))
}

// ClearWSRoutes removes every WebSocket route.
func (a *App) ClearWSRoutes(ctx context.Context, convID string, reload *bool) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	re := reload == nil || *reload
	n, err := s.ClearWSRoutes(ctx, re)
	if err != nil {
		return Result{}, err
	}
	return done(s, jsonText(map[string]any{"cleared": n, "reload": re}))
}

// AddInitScript installs a script that runs before page scripts.
func (a *App) AddInitScript(ctx context.Context, convID, source, id string, runImmediately bool) (Result, error) {
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	script, err := s.AddInitScript(ctx, source, id, runImmediately)
	if err != nil {
		return Result{}, invalid(err)
	}
	return done(s, jsonText(map[string]any{"script": map[string]any{"id": script.ID, "bytes": len(script.Source)}}))
}

// RemoveInitScript uninstalls one.
func (a *App) RemoveInitScript(ctx context.Context, convID, id string) (Result, error) {
	if strings.TrimSpace(id) == "" {
		return Result{}, apperror.New(400, "id is required", nil)
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	removed := s.RemoveInitScript(id)
	return done(s, jsonText(map[string]any{"removed": removed, "scripts": len(s.InitScripts())}))
}

// NetFilter picks network log entries.
type NetFilter struct {
	Pattern string  `json:"pattern"`
	Regex   bool    `json:"regex"`
	Method  string  `json:"method"`
	Status  int64   `json:"status"`
	Errors  bool    `json:"errors"`
	After   int64   `json:"after"`
	Limit   int     `json:"limit"`
	Timeout float64 `json:"timeout"`
}

func (f NetFilter) matcher() (func(dservice.BrowserNetEntry) bool, error) {
	match := func(string) bool { return true }
	if strings.TrimSpace(f.Pattern) != "" {
		re, err := valueobject.GlobURL(f.Pattern, f.Regex)
		if err != nil {
			return nil, apperror.New(400, "pattern: "+err.Error(), err)
		}
		match = re.MatchString
	}
	return func(e dservice.BrowserNetEntry) bool {
		if !match(e.URL) {
			return false
		}
		if f.Method != "" && !strings.EqualFold(f.Method, e.Method) {
			return false
		}
		if f.Status != 0 && e.Status != f.Status {
			return false
		}
		return !f.Errors || e.Error != "" || e.Status >= 400
	}, nil
}

// NetworkLog returns the newest matching entries after the cursor, oldest
// first, and the cursor for the next call.
func (a *App) NetworkLog(ctx context.Context, convID string, f NetFilter) (Result, error) {
	if f.Limit < 0 || f.Limit > netMaxLimit || f.After < 0 {
		return Result{}, apperror.New(400, "limit must be 0-300 and after 0 or more", nil)
	}
	match, err := f.matcher()
	if err != nil {
		return Result{}, err
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	limit := f.Limit
	if limit == 0 {
		limit = netDefaultLimit
	}
	out := []dservice.BrowserNetEntry{}
	for _, e := range s.NetLog() {
		if e.Seq > f.After && match(e) {
			out = append(out, e)
		}
	}
	next := f.After
	if len(out) > 0 {
		next = out[len(out)-1].Seq
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return done(s, jsonText(map[string]any{"entries": out, "next": next}))
}

// WaitForResponse returns the newest matching entry already logged after
// the cursor, or waits for one up to the timeout (10 s, at most 30 s).
func (a *App) WaitForResponse(ctx context.Context, convID string, f NetFilter) (Result, error) {
	if f.Timeout < 0 || f.Timeout > 30 || f.After < 0 {
		return Result{}, apperror.New(400, "timeout must be 0-30 seconds and after 0 or more", nil)
	}
	match, err := f.matcher()
	if err != nil {
		return Result{}, err
	}
	s, err := a.session(ctx, convID)
	if err != nil {
		return Result{}, err
	}
	timeout := waitDefault
	if f.Timeout > 0 {
		timeout = min(time.Duration(f.Timeout*float64(time.Second)), waitMax)
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(waitPoll)
	defer tick.Stop()
	cursor, observed := f.After, 0
	for {
		var found *dservice.BrowserNetEntry
		for _, e := range s.NetLog() {
			if e.Seq <= cursor {
				continue
			}
			cursor = e.Seq
			observed++
			if match(e) {
				found = &e
			}
		}
		if found != nil {
			return done(s, jsonText(found))
		}
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-tick.C:
		case <-deadline.C:
			label := f.Pattern
			if label == "" {
				label = "**"
			}
			return Result{}, apperror.New(408, fmt.Sprintf("no response matched %q within %s (%d responses seen; pass after=<next> from browser_network_log to skip old ones)", label, timeout, observed), nil)
		}
	}
}
