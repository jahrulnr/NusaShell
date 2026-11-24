package browser

// Network routing for the browser_use agent: Playwright-style page.route on
// top of CDP's Fetch domain. Rules are session-scoped. Interception is armed
// when the first rule is added and disarmed when the last one is cleared, so
// a session that never routes pays no per-request cost.
//
// Scope and limits, stated here because they are not obvious from the API:
//   - Fetch interception covers requests the renderer makes on behalf of the
//     tab's frames. Requests served by a service worker after it is running,
//     and traffic from extensions, are not intercepted.
//   - Only the request stage is intercepted. Fulfilling needs no response
//     body, and the request stage is the one that can rewrite a URL or fail a
//     request before it leaves the browser.
//   - Rewriting a cross-origin request changes the origin the response is
//     attributed to; the page's CORS checks apply to the rewrite target.
//     Fulfilling a cross-origin request needs its Access-Control-Allow-Origin
//     header set by the rule itself.
//   - Rules are first-match-wins in insertion order. A rule whose `times`
//     budget is spent stays listed but stops matching until it is cleared.

import (
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"

	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"

	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Route actions, named after Playwright's route methods.
const (
	RouteFulfill  = "fulfill"
	RouteAbort    = "abort"
	RouteDelay    = "delay"
	RouteContinue = "continue"
)

const (
	// routeRuleCap bounds a session's rule list; every rule is checked on
	// every paused request while interception is armed.
	routeRuleCap = 64
	// routeBodyCap bounds one synthetic response body held in memory.
	routeBodyCap = 4 << 20
	// routeHeaderCap bounds the synthetic header list per rule.
	routeHeaderCap = 64
	// routeHeaderValueCap bounds one header value.
	routeHeaderValueCap = 8 << 10
	// routePatternCap bounds a URL pattern.
	routePatternCap = 4 << 10
	// routeDelayCap bounds one deliberate delay.
	routeDelayCap = 30 * time.Second
	// routeDispositionTimeout keeps one paused request from sitting unresolved
	// forever if a CDP call wedges; the fallback continue runs after it.
	routeDispositionTimeout = 10 * time.Second
	// routeConcurrency bounds in-flight dispositions so a burst of image
	// requests cannot spawn unbounded goroutines.
	routeConcurrency = 16
)

// RouteError reports an invalid rule (bad pattern, action, header, or body).
// The HTTP bridge maps it to 400.
var ErrRouteInvalid = errors.New("invalid route")

type RouteRule = dservice.BrowserRouteRule

type RouteState = dservice.BrowserRouteState

// routeRule is the stored form: the compiled matcher and decoded body live
// beside the request fields so the pause path does no parsing.
type routeRule struct {
	RouteRule
	id        string
	re        *regexp.Regexp
	body      []byte
	remaining int // -1 unlimited
	hits      int64
	lastError string
}

// resourceTypes is the accepted resourceType vocabulary (lowercase).
var resourceTypes = map[string]network.ResourceType{
	"document":           network.ResourceTypeDocument,
	"stylesheet":         network.ResourceTypeStylesheet,
	"image":              network.ResourceTypeImage,
	"media":              network.ResourceTypeMedia,
	"font":               network.ResourceTypeFont,
	"script":             network.ResourceTypeScript,
	"texttrack":          network.ResourceTypeTextTrack,
	"xhr":                network.ResourceTypeXHR,
	"fetch":              network.ResourceTypeFetch,
	"prefetch":           network.ResourceTypePrefetch,
	"eventsource":        network.ResourceTypeEventSource,
	"websocket":          network.ResourceTypeWebSocket,
	"manifest":           network.ResourceTypeManifest,
	"signedexchange":     network.ResourceTypeSignedExchange,
	"ping":               network.ResourceTypePing,
	"cspviolationreport": network.ResourceTypeCSPViolationReport,
	"preflight":          network.ResourceTypePreflight,
	"fedcm":              network.ResourceTypeFedCM,
	"other":              network.ResourceTypeOther,
}

// routeErrorReasons maps the accepted abort vocabulary (lowercase) onto CDP.
var routeErrorReasons = map[string]network.ErrorReason{
	"failed":               network.ErrorReasonFailed,
	"aborted":              network.ErrorReasonAborted,
	"timedout":             network.ErrorReasonTimedOut,
	"accessdenied":         network.ErrorReasonAccessDenied,
	"connectionclosed":     network.ErrorReasonConnectionClosed,
	"connectionreset":      network.ErrorReasonConnectionReset,
	"connectionrefused":    network.ErrorReasonConnectionRefused,
	"connectionaborted":    network.ErrorReasonConnectionAborted,
	"connectionfailed":     network.ErrorReasonConnectionFailed,
	"namenotresolved":      network.ErrorReasonNameNotResolved,
	"internetdisconnected": network.ErrorReasonInternetDisconnected,
	"addressunreachable":   network.ErrorReasonAddressUnreachable,
	"blockedbyclient":      network.ErrorReasonBlockedByClient,
	"blockedbyresponse":    network.ErrorReasonBlockedByResponse,
}

// prepareRouteRule validates a rule and returns its stored form.
func prepareRouteRule(in RouteRule) (*routeRule, error) {
	re, err := globURL(in.Pattern, in.Regex)
	if err != nil {
		return nil, err
	}
	r := &routeRule{RouteRule: in, re: re, remaining: -1}
	if in.Times < 0 {
		return nil, fmt.Errorf("%w: times must be 0 or positive", ErrRouteInvalid)
	}
	if in.Times > 0 {
		r.remaining = in.Times
	}
	if in.Method != "" {
		m := strings.ToUpper(strings.TrimSpace(in.Method))
		if !methodToken.MatchString(m) {
			return nil, fmt.Errorf("%w: invalid method %q", ErrRouteInvalid, in.Method)
		}
		r.Method = m
	}
	if in.ResourceType != "" {
		rt, ok := resourceTypes[strings.ToLower(strings.TrimSpace(in.ResourceType))]
		if !ok {
			return nil, fmt.Errorf("%w: unknown resourceType %q", ErrRouteInvalid, in.ResourceType)
		}
		// Fetch never sees the WebSocket handshake, so a rule here would look
		// registered and silently never match.
		if rt == network.ResourceTypeWebSocket {
			return nil, fmt.Errorf("%w: use browser_route_ws for WebSockets; the Fetch domain does not intercept the handshake", ErrRouteInvalid)
		}
		r.ResourceType = string(rt)
	}
	r.Action = strings.ToLower(strings.TrimSpace(in.Action))
	switch r.Action {
	case RouteFulfill:
		if r.Status == 0 {
			r.Status = 200
		}
		if r.Status < 100 || r.Status > 599 {
			return nil, fmt.Errorf("%w: status must be 100-599", ErrRouteInvalid)
		}
		if len(in.Headers) > routeHeaderCap {
			return nil, fmt.Errorf("%w: at most %d headers", ErrRouteInvalid, routeHeaderCap)
		}
		headers := make(map[string]string, len(in.Headers))
		for k, v := range in.Headers {
			name := strings.TrimSpace(k)
			if name == "" || !headerToken.MatchString(name) {
				return nil, fmt.Errorf("%w: invalid header name %q", ErrRouteInvalid, k)
			}
			if len(v) > routeHeaderValueCap {
				return nil, fmt.Errorf("%w: header %q is longer than %d bytes", ErrRouteInvalid, name, routeHeaderValueCap)
			}
			if strings.ContainsAny(v, "\r\n") {
				return nil, fmt.Errorf("%w: header %q contains a line break", ErrRouteInvalid, name)
			}
			headers[name] = v
		}
		r.Headers = headers
		if in.BodyBase64 {
			body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(in.Body))
			if err != nil {
				return nil, fmt.Errorf("%w: bodyBase64 is not valid base64", ErrRouteInvalid)
			}
			r.body = body
		} else {
			r.body = []byte(in.Body)
		}
		if len(r.body) > routeBodyCap {
			return nil, fmt.Errorf("%w: body is larger than %d bytes", ErrRouteInvalid, routeBodyCap)
		}
	case RouteAbort:
		reason := strings.ToLower(strings.TrimSpace(in.ErrorReason))
		if reason == "" {
			reason = "failed"
		}
		if _, ok := routeErrorReasons[reason]; !ok {
			return nil, fmt.Errorf("%w: unknown errorReason %q", ErrRouteInvalid, in.ErrorReason)
		}
		r.ErrorReason = reason
	case RouteDelay:
		if in.DelayMS <= 0 {
			return nil, fmt.Errorf("%w: delayMs must be positive", ErrRouteInvalid)
		}
		if time.Duration(in.DelayMS)*time.Millisecond > routeDelayCap {
			return nil, fmt.Errorf("%w: delayMs must not exceed %d", ErrRouteInvalid, routeDelayCap.Milliseconds())
		}
	case RouteContinue:
		if in.TargetURL != "" {
			u, err := url.Parse(strings.TrimSpace(in.TargetURL))
			if err != nil || !u.IsAbs() {
				return nil, fmt.Errorf("%w: targetUrl must be absolute", ErrRouteInvalid)
			}
			switch strings.ToLower(u.Scheme) {
			case "http", "https", "ws", "wss":
			default:
				return nil, fmt.Errorf("%w: targetUrl scheme must be http, https, ws, or wss", ErrRouteInvalid)
			}
			r.TargetURL = u.String()
		}
	default:
		return nil, fmt.Errorf("%w: action must be fulfill, abort, delay, or continue", ErrRouteInvalid)
	}
	return r, nil
}

var (
	methodToken = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)
	// headerToken is the RFC 7230 field-name token set.
	headerToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

func (r *routeRule) matches(method string, rt network.ResourceType, rawURL string) bool {
	if r.Method != "" && !strings.EqualFold(r.Method, method) {
		return false
	}
	if r.ResourceType != "" && !strings.EqualFold(r.ResourceType, string(rt)) {
		return false
	}
	return r.re.MatchString(rawURL)
}

func (r *routeRule) state() RouteState {
	st := RouteState{
		ID:           r.id,
		Pattern:      r.Pattern,
		Regex:        r.Regex,
		Method:       r.Method,
		ResourceType: r.ResourceType,
		Action:       r.Action,
		Status:       r.Status,
		Headers:      r.Headers,
		BodyBytes:    len(r.body),
		DelayMS:      r.DelayMS,
		TargetURL:    r.TargetURL,
		ErrorReason:  r.ErrorReason,
		Times:        r.Times,
		Hits:         r.hits,
		LastError:    r.lastError,
	}
	st.Remaining = -1
	if r.remaining >= 0 {
		st.Remaining = r.remaining
	}
	return st
}

// AddRoute validates and registers one rule, arming interception when needed.
func (s *Session) AddRoute(in RouteRule) (RouteState, error) {
	rule, err := prepareRouteRule(in)
	if err != nil {
		return RouteState{}, err
	}
	rule.id = newID("rt")

	s.routeMu.Lock()
	if len(s.routes) >= routeRuleCap {
		s.routeMu.Unlock()
		return RouteState{}, fmt.Errorf("%w: at most %d rules per session", ErrRouteInvalid, routeRuleCap)
	}
	s.routes = append(s.routes, rule)
	s.routeMu.Unlock()

	if err := s.armRoutes(); err != nil {
		s.routeMu.Lock()
		s.routes = removeRoute(s.routes, rule.id)
		s.routeMu.Unlock()
		return RouteState{}, err
	}
	return rule.state(), nil
}

// ClearRoutes removes one rule by id, or every rule when id is empty, and
// disarms interception once nothing is left. It returns how many were removed.
// The domain call runs under routeMu so a concurrent add cannot re-enable
// interception between the empty check and the disable.
func (s *Session) ClearRoutes(id string) int {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	before := len(s.routes)
	if id == "" {
		s.routes = nil
	} else {
		s.routes = removeRoute(s.routes, id)
	}
	removed := before - len(s.routes)
	if len(s.routes) == 0 && s.routeArmed {
		ctx, cancel := context.WithTimeout(s.ctx, routeDispositionTimeout)
		if _, err := chromedp.Call(ctx, fetch.Disable, cdp.Empty{}); err == nil {
			s.routeArmed = false
		}
		cancel()
	}
	return removed
}

// Routes returns a snapshot of the session's rules, insertion order.
func (s *Session) Routes() []RouteState {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	out := make([]RouteState, 0, len(s.routes))
	for _, r := range s.routes {
		out = append(out, r.state())
	}
	return out
}

func removeRoute(rules []*routeRule, id string) []*routeRule {
	out := rules[:0]
	for _, r := range rules {
		if r.id != id {
			out = append(out, r)
		}
	}
	return out
}

// armRoutes enables Fetch interception on the current tab and starts its
// dispatcher once. It is a no-op without rules or when already armed.
func (s *Session) armRoutes() error {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	if s.routeArmed || len(s.routes) == 0 {
		return nil
	}
	// Enable without patterns pauses every request; matching happens in our
	// dispatcher so a rule change never needs a re-enable. The call stays
	// under routeMu so a second add cannot observe a half-armed session and
	// report success before the domain is actually on.
	ctx, cancel := context.WithTimeout(s.ctx, routeDispositionTimeout)
	_, err := chromedp.Call(ctx, fetch.Enable, fetch.EnableParams{})
	cancel()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.routeArmed = true
	// The dispatcher outlives a disable/re-enable cycle on the same tab: a
	// second goroutine would handle every event twice.
	if !s.routeWatching {
		s.routeWatching = true
		go s.watchRoutes(s.ctx)
	}
	return nil
}

// watchRoutes dispatches paused requests until the tab context ends. The event
// iterator must keep draining, so each disposition runs on its own goroutine
// with a bounded pool; the loop itself never calls CDP.
func (s *Session) watchRoutes(ctx context.Context) {
	tab := chromedp.FromContext(ctx).Target
	sem := make(chan struct{}, routeConcurrency)
	for ev, err := range cdp.Events[fetch.EventRequestPaused](ctx, tab, fetch.RequestPaused) {
		if err != nil {
			return
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func(ev fetch.EventRequestPaused) {
			defer func() { <-sem }()
			s.dispatchPaused(ctx, ev)
		}(ev)
	}
}

// dispatchPaused applies the first matching rule to one paused request and
// always resolves it. A failed disposition falls back to continueRequest: a
// request left paused would hang the page, which is worse than a missed mock.
func (s *Session) dispatchPaused(ctx context.Context, ev fetch.EventRequestPaused) {
	if ev.Request == nil {
		s.continueRequest(ctx, ev.RequestID, "")
		return
	}
	// Defensive: interception is armed at the request stage, but a future
	// response-stage pattern must still be released the right way.
	if ev.ResponseStatusCode != 0 || ev.ResponseErrorReason != "" {
		actx, cancel := context.WithTimeout(ctx, routeDispositionTimeout)
		_, _ = chromedp.Call(actx, fetch.ContinueResponse, fetch.ContinueResponseParams{RequestID: ev.RequestID})
		cancel()
		return
	}
	rule := s.matchRoute(ev.Request.Method, ev.ResourceType, ev.Request.URL)
	if rule == nil {
		s.continueRequest(ctx, ev.RequestID, "")
		return
	}
	actx, cancel := context.WithTimeout(ctx, routeDispositionTimeout)
	var err error
	switch rule.Action {
	case RouteFulfill:
		params := fulfillParams(ev.RequestID, rule)
		_, err = chromedp.Call(actx, fetch.FulfillRequest, params)
	case RouteAbort:
		_, err = chromedp.Call(actx, fetch.FailRequest, fetch.FailRequestParams{
			RequestID:   ev.RequestID,
			ErrorReason: routeErrorReasons[rule.ErrorReason],
		})
	case RouteDelay:
		timer := time.NewTimer(time.Duration(rule.DelayMS) * time.Millisecond)
		select {
		case <-timer.C:
			err = s.continueRequest(actx, ev.RequestID, rule.TargetURL)
		case <-actx.Done():
			err = actx.Err()
		}
		timer.Stop()
	case RouteContinue:
		err = s.continueRequest(actx, ev.RequestID, rule.TargetURL)
	}
	cancel()
	if err != nil {
		s.noteRouteError(rule.id, err)
		// The paused request still needs an answer; a second failure only
		// means the id is already gone (navigation, tab close).
		fctx, fcancel := context.WithTimeout(ctx, routeDispositionTimeout)
		_ = s.continueRequest(fctx, ev.RequestID, "")
		fcancel()
	}
	s.noteRouteHit(rule, ev)
}

// continueRequest releases a paused request, optionally rewriting its URL.
func (s *Session) continueRequest(ctx context.Context, id fetch.RequestID, target string) error {
	params := fetch.ContinueRequestParams{RequestID: id}
	if target != "" {
		params.URL = target
	}
	_, err := chromedp.Call(ctx, fetch.ContinueRequest, params)
	return err
}

// matchRoute returns a copy of the first matching rule and counts the hit.
// A rule whose times budget is spent no longer matches.
func (s *Session) matchRoute(method string, rt network.ResourceType, rawURL string) *routeRule {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	for _, r := range s.routes {
		if r.remaining == 0 || !r.matches(method, rt, rawURL) {
			continue
		}
		if r.remaining > 0 {
			r.remaining--
		}
		r.hits++
		hit := *r
		hit.Headers = r.Headers
		return &hit
	}
	return nil
}

// noteRouteError records the last disposition failure for route_list. A rule
// already cleared since the failure is not resurrected.
func (s *Session) noteRouteError(id string, err error) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	for _, r := range s.routes {
		if r.id == id {
			r.lastError = err.Error()
			return
		}
	}
}

// noteRouteHit is where a recording notes a rule's first hit; recordings
// are not kept yet.
func (s *Session) noteRouteHit(rule *routeRule, ev fetch.EventRequestPaused) {}

// fulfillParams builds the CDP fulfillment. The body is already decoded; the
// Content-Type is only inferred when the rule did not set one, so an explicit
// header always wins.
func fulfillParams(id fetch.RequestID, r *routeRule) fetch.FulfillRequestParams {
	headers := make([]*fetch.HeaderEntry, 0, len(r.Headers)+1)
	hasType := false
	for name, value := range r.Headers {
		if strings.EqualFold(name, "content-type") {
			hasType = true
		}
		// Chrome computes the synthetic body length; a stale Content-Length
		// from the rule would truncate or hang the response.
		if strings.EqualFold(name, "content-length") {
			continue
		}
		headers = append(headers, &fetch.HeaderEntry{Name: name, Value: value})
	}
	if !hasType && len(r.body) > 0 {
		headers = append(headers, &fetch.HeaderEntry{Name: "Content-Type", Value: inferredContentType(r.body, r.BodyBase64)})
	}
	return fetch.FulfillRequestParams{
		RequestID:       id,
		ResponseCode:    r.Status,
		ResponseHeaders: headers,
		Body:            r.body,
	}
}

// inferredContentType guesses a response type for a synthetic body: JSON when
// it parses as JSON, octet-stream for base64 payloads, text otherwise.
func inferredContentType(body []byte, binary bool) string {
	if binary {
		return "application/octet-stream"
	}
	if json.Valid(body) {
		return "application/json"
	}
	return "text/plain; charset=utf-8"
}

// globURL compiles a route pattern, wrapping errors as invalid routes.
func globURL(pattern string, regex bool) (*regexp.Regexp, error) {
	if len(pattern) > routePatternCap {
		return nil, fmt.Errorf("%w: pattern is longer than %d bytes", ErrRouteInvalid, routePatternCap)
	}
	re, err := valueobject.GlobURL(pattern, regex)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRouteInvalid, err)
	}
	return re, nil
}
