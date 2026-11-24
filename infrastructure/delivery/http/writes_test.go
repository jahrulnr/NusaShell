package http

import (
	"context"
	"errors"
	"fmt"
	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appcontrol "github.com/jahrulnr/nusashell/application/control"
	appprovider "github.com/jahrulnr/nusashell/application/provider"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// --- write fakes ---

type fakeConvWrites struct {
	last      appconversation.Annotation // the latest Annotate call
	renamed   map[string]string
	deleted   []string
	compacted []string
	purged    bool  // DeleteAll ran
	ctxErr    error // ctx state observed inside the job call
	err       error
}

func (f *fakeConvWrites) Annotate(_ context.Context, id string, a appconversation.Annotation) error {
	f.last = a
	title := a.Title
	if f.renamed == nil {
		f.renamed = map[string]string{}
	}
	if title != nil {
		f.renamed[id] = *title
	}
	return f.err
}

func (f *fakeConvWrites) Delete(ctx context.Context, id string) error {
	f.ctxErr = ctx.Err()
	f.deleted = append(f.deleted, id)
	return f.err
}

func (f *fakeConvWrites) DeleteAll(ctx context.Context) (int, error) {
	f.ctxErr = ctx.Err()
	if f.err != nil {
		return len(f.deleted), f.err
	}
	f.purged = true
	return len(f.deleted), nil
}

func (f *fakeConvWrites) Compact(ctx context.Context, id string) error {
	f.ctxErr = ctx.Err()
	f.compacted = append(f.compacted, id)
	return f.err
}

type fakeGwWrites struct {
	lastIn  appprovider.ProviderInput
	deleted []string
	outcome appprovider.UpsertOutcome
	err     error
}

func (f *fakeGwWrites) Upsert(_ context.Context, in appprovider.ProviderInput) (appprovider.UpsertOutcome, error) {
	f.lastIn = in
	return f.outcome, f.err
}

func (f *fakeGwWrites) ImportModels(context.Context, string) (appprovider.UpsertOutcome, error) {
	return appprovider.UpsertOutcome{}, f.err
}

func (f *fakeGwWrites) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

type fakeControl struct {
	entries []appcontrol.ActivityEntry
	last    appcontrol.ActivityFilter
	cleared bool
}

func (f *fakeControl) Activity(filter appcontrol.ActivityFilter) []appcontrol.ActivityEntry {
	f.last = filter
	return f.entries
}

func (f *fakeControl) Clear() { f.cleared = true }

type fakeSettingsWriter struct {
	values map[string]string
	doc    []byte
	err    error
}

func (f *fakeSettingsWriter) SetMany(values map[string]string) ([]byte, error) {
	f.values = values
	return f.doc, f.err
}

// doBody issues a mutation request with a body; the Origin header is
// exact by default so handler behavior — not the guard — is under test.
func doBody(t *testing.T, h *stdhttp.Handler, method, path, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+testAuthority+path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Origin", "http://"+testAuthority)
	rec := httptest.NewRecorder()
	(*h).ServeHTTP(rec, req)
	return rec
}

// --- body envelope rules ---

func TestBodyEnvelopeRules(t *testing.T) {
	fcw := &fakeConvWrites{}
	// PATCH answers with a fresh projection, so it only exists when the
	// read surface is wired — the envelope rules below exercise that route.
	h := newTestHandler(t, Deps{Conversations: &fakeConversations{}, ConversationWrites: fcw})
	cases := []struct {
		name, method, path, ct, body string
	}{
		{"wrong content type", "PATCH", "/api/conversations/c_1", "text/plain", `{"title":"x"}`},
		{"no content type", "PATCH", "/api/conversations/c_1", "", `{"title":"x"}`},
		{"malformed json", "PATCH", "/api/conversations/c_1", "application/json", `{"title":`},
		{"unknown field", "PATCH", "/api/conversations/c_1", "application/json", `{"title":"x","extra":1}`},
		{"two documents", "PATCH", "/api/conversations/c_1", "application/json", `{"title":"x"}{"title":"y"}`},
		{"missing title", "PATCH", "/api/conversations/c_1", "application/json", `{}`},
		{"bad id", "PATCH", "/api/conversations/..", "application/json", `{"title":"x"}`},
	}
	for _, tc := range cases {
		rec := doBody(t, h, tc.method, tc.path, tc.ct, tc.body)
		if rec.Code != 400 {
			t.Fatalf("%s: want 400, got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
		if decodeBody(t, rec)["code"] != "invalid_argument" {
			t.Fatalf("%s: want invalid_argument, got %s", tc.name, rec.Body.String())
		}
	}
	if len(fcw.renamed) != 0 {
		t.Fatal("rejected requests must not reach the use case")
	}

	// Over the 1 MiB body cap → 413.
	big := `{"title":"` + strings.Repeat("x", 1<<20) + `"}`
	rec := doBody(t, h, "PATCH", "/api/conversations/c_1", "application/json", big)
	if rec.Code != 413 || decodeBody(t, rec)["code"] != "input_too_large" {
		t.Fatalf("want 413 input_too_large, got %d %s", rec.Code, rec.Body.String())
	}
}

// --- conversation mutations ---

func TestRenameConversation(t *testing.T) {
	fc := &fakeConversations{infos: []drepo.ConversationInfo{mkInfo("c_1")}}
	fcw := &fakeConvWrites{}
	h := newTestHandler(t, Deps{Conversations: fc, ConversationWrites: fcw})

	rec := doBody(t, h, "PATCH", "/api/conversations/c_1", "application/json", `{"title":"New Title"}`)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if fcw.renamed["c_1"] != "New Title" {
		t.Fatalf("rename did not reach the use case: %v", fcw.renamed)
	}
	doc := decodeBody(t, rec)
	if doc["id"] != "c_1" || doc["title"] != "t-c_1" {
		t.Fatalf("want sanitized refreshed conversation, got %v", doc)
	}
}

// The picker's reasoning effort and upstream pin are the room's to keep: a
// PATCH carries them without a title or a model, "default" and "" return them
// to the model's own, and an effort that is no word of the vocabulary is a 400.
func TestPatchConversationEffortAndUpstream(t *testing.T) {
	fc := &fakeConversations{infos: []drepo.ConversationInfo{mkInfo("c_1")}}
	fcw := &fakeConvWrites{}
	h := newTestHandler(t, Deps{Conversations: fc, ConversationWrites: fcw})

	rec := doBody(t, h, "PATCH", "/api/conversations/c_1", "application/json", `{"effort":"high","upstream":" Alpha "}`)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if fcw.last.Effort == nil || *fcw.last.Effort != enum.EffortHigh || fcw.last.Upstream == nil || *fcw.last.Upstream != " Alpha " {
		t.Fatalf("annotation = %+v", fcw.last)
	}

	for _, body := range []string{`{"effort":"default"}`, `{"effort":""}`} {
		fcw.last = appconversation.Annotation{}
		if rec := doBody(t, h, "PATCH", "/api/conversations/c_1", "application/json", body); rec.Code != 200 {
			t.Fatalf("%s: want 200, got %d: %s", body, rec.Code, rec.Body.String())
		}
		if fcw.last.Effort == nil || !fcw.last.Effort.IsUnset() {
			t.Fatalf("%s: effort = %v, want unset", body, fcw.last.Effort)
		}
	}

	fcw.last = appconversation.Annotation{}
	rec = doBody(t, h, "PATCH", "/api/conversations/c_1", "application/json", `{"effort":"furious"}`)
	if rec.Code != 400 || decodeBody(t, rec)["code"] != "invalid_argument" {
		t.Fatalf("an unknown effort: want 400 invalid_argument, got %d %s", rec.Code, rec.Body.String())
	}
	if fcw.last.Effort != nil {
		t.Fatal("a rejected effort must not reach the use case")
	}
}

func TestDeleteConversationIs204AndRunsUnderJobsCtx(t *testing.T) {
	fcw := &fakeConvWrites{}
	h := newTestHandler(t, Deps{
		Conversations:      &fakeConversations{},
		ConversationWrites: fcw,
		Jobs:               context.Background(),
	})

	req := httptest.NewRequest("DELETE", "http://"+testAuthority+"/api/conversations/c_1", nil)
	req.Header.Set("Origin", "http://"+testAuthority)
	// A request-scoped context that is already abandoned: the job ctx
	// must be the server lifecycle, so ctx.Err() inside Delete is nil.
	deadCtx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	(*h).ServeHTTP(rec, req.WithContext(deadCtx))

	if rec.Code != 204 {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(fcw.deleted) != 1 || fcw.deleted[0] != "c_1" {
		t.Fatalf("delete did not reach use case: %v", fcw.deleted)
	}
	if fcw.ctxErr != nil {
		t.Fatalf("abandoned request canceled an accepted job: %v", fcw.ctxErr)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("204 must have no body, got %q", rec.Body.String())
	}
}

// DELETE /api/conversations requires the typed confirm word — anything
// else is a 400 confirm_required that never reaches the use case.
func TestDeleteAllRequiresConfirm(t *testing.T) {
	fcw := &fakeConvWrites{}
	ctl := &fakeControl{}
	h := newTestHandler(t, Deps{
		Conversations:      &fakeConversations{},
		ConversationWrites: fcw,
		Control:            ctl,
		Jobs:               context.Background(),
	})
	for _, body := range []string{`{}`, `{"confirm":"delete"}`, `{"confirm":"yes"}`, `not-json`} {
		rec := doBody(t, h, "DELETE", "/api/conversations", "application/json", body)
		if body == "not-json" {
			if rec.Code != 400 {
				t.Fatalf("malformed body → %d, want 400", rec.Code)
			}
			continue
		}
		if rec.Code != 400 || decodeBody(t, rec)["code"] != "confirm_required" {
			t.Fatalf("confirm %s → %d %s, want 400 confirm_required", body, rec.Code, rec.Body.String())
		}
	}
	if fcw.purged {
		t.Fatal("purge ran without the confirm word")
	}
	if ctl.cleared {
		t.Fatal("activity cleared without a purge")
	}
}

// With the confirm word the purge runs under the jobs ctx, answers with
// the deleted count, and drops the activity log that named the threads.
func TestDeleteAllPurgesAndClearsActivity(t *testing.T) {
	fcw := &fakeConvWrites{}
	ctl := &fakeControl{}
	h := newTestHandler(t, Deps{
		Conversations:      &fakeConversations{},
		ConversationWrites: fcw,
		Control:            ctl,
		Jobs:               context.Background(),
	})
	rec := doBody(t, h, "DELETE", "/api/conversations", "application/json", `{"confirm":"DELETE"}`)
	if rec.Code != 200 || decodeBody(t, rec)["deleted"] != float64(0) {
		t.Fatalf("want {deleted:0}, got %d %s", rec.Code, rec.Body.String())
	}
	if !fcw.purged || !ctl.cleared {
		t.Fatalf("purge=%v clear=%v, both must run", fcw.purged, ctl.cleared)
	}
	if fcw.ctxErr != nil {
		t.Fatalf("request ctx leaked into the purge: %v", fcw.ctxErr)
	}

	// A failed purge reports its error and never claims the log is clean.
	fcw.err = apperror.New(500, "uploads stuck", nil)
	ctl.cleared = false
	rec = doBody(t, h, "DELETE", "/api/conversations", "application/json", `{"confirm":"DELETE"}`)
	if rec.Code != 500 || ctl.cleared {
		t.Fatalf("failed purge → %d cleared=%v", rec.Code, ctl.cleared)
	}
}

// The activity route projects the ring, newest first, and maps the
// caller/target/limit filters.
func TestControlActivityRoute(t *testing.T) {
	ctl := &fakeControl{entries: []appcontrol.ActivityEntry{{
		At: time.Now(), Caller: "c_a", Tool: "send", Target: "c_b", Outcome: "ok", DurationMs: 3,
	}}}
	h := newTestHandler(t, Deps{Control: ctl})
	rec := do(t, h, "GET", "/api/conversation-control/activity?caller=c_a&target=c_b&limit=25", "", "")
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ctl.last.Caller != "c_a" || ctl.last.Target != "c_b" || ctl.last.Limit != 25 {
		t.Fatalf("filters = %+v", ctl.last)
	}
	doc := decodeBody(t, rec)
	entries, ok := doc["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries = %v", doc["entries"])
	}
	e := entries[0].(map[string]any)
	if e["caller"] != "c_a" || e["tool"] != "send" || e["target"] != "c_b" || e["outcome"] != "ok" {
		t.Fatalf("entry projection = %v", e)
	}
}

func TestCompactConversationReturnsCompletedState(t *testing.T) {
	fcw := &fakeConvWrites{}
	convs := &fakeConversations{infos: []drepo.ConversationInfo{{ID: "c_1", SealedSegments: 1}}}
	h := newTestHandler(t, Deps{Conversations: convs, ConversationWrites: fcw, Jobs: context.Background()})
	req := httptest.NewRequest("POST", "http://"+testAuthority+"/api/conversations/c_1/compactions", nil)
	req.Header.Set("Origin", "http://"+testAuthority)
	rec := httptest.NewRecorder()
	(*h).ServeHTTP(rec, req)
	if rec.Code != 200 || len(fcw.compacted) != 1 || decodeBody(t, rec)["sealedSegments"] != float64(1) {
		t.Fatalf("want completed compaction state, got %d %v %s", rec.Code, fcw.compacted, rec.Body.String())
	}
}

// --- provider mutations ---

func TestPutProviderBodyRules(t *testing.T) {
	fgw := &fakeGwWrites{}
	fg := &fakeProviders{infos: []appprovider.ProviderInfo{{ID: "openrouter", Name: "OR", Endpoint: "https://x"}}}
	h := newTestHandler(t, Deps{Providers: fg, ProviderWrites: fgw})
	valid := `{"name":"OR","style":"completion","endpoint":"https://x","models":[{"id":"gpt-5"}]}`

	for _, tc := range []struct{ name, body string }{
		{"body id rejected", `{"id":"openrouter","name":"OR","style":"completion","endpoint":"https://x"}`},
		{"optionalContent rejected", `{"name":"OR","style":"completion","endpoint":"https://x","models":[{"id":"gpt-5","optionalContent":{"k":"v"}}]}`},
		{"apiKey null rejected", `{"name":"OR","style":"completion","endpoint":"https://x","apiKey":null}`},
		{"unknown top field", `{"name":"OR","style":"completion","endpoint":"https://x","zzz":1}`},
	} {
		rec := doBody(t, h, "PUT", "/api/providers/openrouter", "application/json", tc.body)
		if rec.Code != 400 {
			t.Fatalf("%s: want 400, got %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}

	rec := doBody(t, h, "PUT", "/api/providers/openrouter", "application/json", valid)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	in := fgw.lastIn
	if in.ID != "openrouter" || in.Name != "OR" || in.Endpoint != "https://x" ||
		in.Style.String() != "completion" || len(in.Models) != 1 || in.Models[0].ID != "gpt-5" {
		t.Fatalf("input not mapped: %+v", in)
	}
	if in.ApiKey != nil {
		t.Fatal("absent apiKey must stay nil (keep)")
	}
	if in.Models[0].OptionalContent != nil {
		t.Fatal("optionalContent must never arrive from the wire")
	}
	doc := decodeBody(t, rec)
	if doc["id"] != "openrouter" || doc["keyState"] != "unset" {
		t.Fatalf("want sanitized provider, got %v", doc)
	}
}

func TestPutProviderApiKeyTriState(t *testing.T) {
	fgw := &fakeGwWrites{}
	fg := &fakeProviders{infos: []appprovider.ProviderInfo{{ID: "g", Endpoint: "https://x"}}}
	h := newTestHandler(t, Deps{Providers: fg, ProviderWrites: fgw})
	base := `{"name":"G","style":"completion","endpoint":"https://x","apiKey":%s}`

	for _, tc := range []struct {
		json string
		want *string
	}{
		{`""`, new("")},               // clear
		{`"sk-live"`, new("sk-live")}, // set
	} {
		rec := doBody(t, h, "PUT", "/api/providers/g", "application/json", fmt.Sprintf(base, tc.json))
		if rec.Code != 200 {
			t.Fatalf("apiKey %s: want 200, got %d %s", tc.json, rec.Code, rec.Body.String())
		}
		if fgw.lastIn.ApiKey == nil || *fgw.lastIn.ApiKey != *tc.want {
			t.Fatalf("apiKey %s: want %v, got %v", tc.json, *tc.want, fgw.lastIn.ApiKey)
		}
	}
	// absent → keep
	rec := doBody(t, h, "PUT", "/api/providers/g", "application/json",
		`{"name":"G","style":"completion","endpoint":"https://x"}`)
	if rec.Code != 200 || fgw.lastIn.ApiKey != nil {
		t.Fatalf("absent apiKey must keep (nil), got code %d", rec.Code)
	}
}

func TestDeleteProvider204(t *testing.T) {
	fgw := &fakeGwWrites{}
	h := newTestHandler(t, Deps{ProviderWrites: fgw})
	rec := doBody(t, h, "DELETE", "/api/providers/g", "", "")
	if rec.Code != 204 || len(fgw.deleted) != 1 || fgw.deleted[0] != "g" {
		t.Fatalf("want 204 + delete, got %d %v", rec.Code, fgw.deleted)
	}
}

// --- settings mutation ---

func TestPatchSettingsAllowlist(t *testing.T) {
	doc := []byte(`{"conversation":{"mailbox_cap":128,"max_turn_steps":512,"compact_budget_pct":90,"compact_keep_tail":4},"service":{"header_timeout":"30s","idle_timeout":"90s"},"log":{"max_lines":2000,"max_files":9},"storage":{"dir":"/root/secret-path"}}`)
	fsw := &fakeSettingsWriter{doc: doc}
	h := newTestHandler(t, Deps{SettingsWriter: fsw, Settings: &fakeSettings{raw: doc}})

	rec := doBody(t, h, "PATCH", "/api/settings", "application/json",
		`{"values":{"service.idle_timeout":"2m"}}`)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(fsw.values) != 1 || fsw.values["service.idle_timeout"] != `"2m"` {
		t.Fatalf("SetMany called with %v", fsw.values)
	}
	out := decodeBody(t, rec)
	if out["storage"] != nil {
		t.Fatal("response must not contain the storage section")
	}

	for _, tc := range []struct{ name, body string }{
		{"non-whitelisted leaf", `{"values":{"storage.dir":"/x"}}`},
		{"invented leaf", `{"values":{"conversation.secret":1}}`},
		{"null value", `{"values":{"log.max_files":null}}`},
		{"no section", `{"values":{"bodies":true}}`},
		{"one bad key among good", `{"values":{"log.max_files":3,"storage.dir":"/x"}}`},
		{"no values", `{"values":{}}`},
		{"old single-key shape", `{"key":"log.max_files","value":3}`},
	} {
		fsw.values = nil
		rec := doBody(t, h, "PATCH", "/api/settings", "application/json", tc.body)
		if rec.Code != 400 {
			t.Fatalf("%s: want 400, got %d", tc.name, rec.Code)
		}
		if fsw.values != nil {
			t.Fatalf("%s: rejected key reached Set", tc.name)
		}
	}
}

// --- 429 disambiguation ---

func TestRateLimitCodes(t *testing.T) {
	// Local mailbox pressure → queue_full.
	fc := &fakeConversations{err: apperror.New(429, "conversation queue full", nil)}
	h := newTestHandler(t, Deps{Conversations: fc})
	rec := do(t, h, "GET", "/api/conversations", "", "")
	if rec.Code != 429 || decodeBody(t, rec)["code"] != "queue_full" {
		t.Fatalf("local 429: want 429 queue_full, got %d %s", rec.Code, rec.Body.String())
	}
	// Provider rate limit → upstream_error.
	fc.err = apperror.FromStatus(429, "rate limit exceeded", errors.New("retry-after 30"))
	rec = do(t, h, "GET", "/api/conversations", "", "")
	if rec.Code != 429 || decodeBody(t, rec)["code"] != "upstream_error" {
		t.Fatalf("upstream 429: want 429 upstream_error, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "retry-after") {
		t.Fatal("upstream cause leaked")
	}
}

// --- paging edge cases ---

func TestPagingEdgeCases(t *testing.T) {
	h := newTestHandler(t, Deps{Conversations: &fakeConversations{infos: []drepo.ConversationInfo{mkInfo("c_1")}}})

	// Max-int offset must not overflow the slice bound computation.
	rec := do(t, h, "GET", "/api/conversations?offset=9223372036854775807&limit=30", "", "")
	if rec.Code != 200 {
		t.Fatalf("max-int offset: want 200, got %d", rec.Code)
	}
	doc := decodeBody(t, rec)
	if len(doc["data"].([]any)) != 0 || doc["page"].(map[string]any)["hasMore"] != false {
		t.Fatalf("max-int offset page wrong: %v", doc["page"])
	}

	// Present-but-empty params are a 400, not a default.
	for _, q := range []string{"?offset=", "?limit=", "?offset=&limit=30", "?start=&limit=5"} {
		path := "/api/conversations" + q
		if strings.HasPrefix(q, "?start") {
			path = "/api/conversations/c_1/messages" + q
		}
		rec = do(t, h, "GET", path, "", "")
		if rec.Code != 400 {
			t.Fatalf("%s: want 400, got %d", path, rec.Code)
		}
	}
}

// --- settings leaf projection ---

func TestSettingsProjectionDropsUnlistedLeaves(t *testing.T) {
	raw := []byte(`{"conversation":{"mailbox_cap":64,"secret_thing":"bearer-x"},"service":{"header_timeout":"30s"},"log":{"max_files":9},"storage":{"dir":"/root/x"}}`)
	h := newTestHandler(t, Deps{Settings: &fakeSettings{raw: raw}})
	rec := do(t, h, "GET", "/api/settings", "", "")
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret_thing") || strings.Contains(rec.Body.String(), "bearer-x") {
		t.Fatalf("unlisted leaf crossed the wire: %s", rec.Body.String())
	}
	conv := decodeBody(t, rec)["conversation"].(map[string]any)
	if len(conv) != 1 || conv["mailbox_cap"] != 64.0 {
		t.Fatalf("conversation section must carry whitelisted leaves only: %v", conv)
	}
}

// Deps documents the read and write surfaces as independently optional.
// Rename and compaction answer with a fresh conversation projection, so a
// writes-only composition cannot serve them: the handler must not be
// mounted and dereference a nil reader (recoverProblem turns that panic
// into a 500, which reads as a server fault instead of an absent route).
func TestConversationWritesOnlyLeavesProjectionMutationsUnregistered(t *testing.T) {
	h := newTestHandler(t, Deps{ConversationWrites: &fakeConvWrites{}})
	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"rename", "PATCH", "/api/conversations/c_1", `{"title":"new"}`},
		{"compact", "POST", "/api/conversations/c_1/compactions", ""},
	} {
		rec := doBody(t, h, tc.method, tc.path, "application/json", tc.body)
		if rec.Code != 404 {
			t.Fatalf("%s with writes only: want 404 for an unregistered projection route, got %d: %s",
				tc.name, rec.Code, rec.Body.String())
		}
	}
}
