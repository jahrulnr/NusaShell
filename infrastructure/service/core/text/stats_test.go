package coretext

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/redact"
)

// captureLog is a drepo.Logger fake that stores entries in their
// post-marshal shape — the same normalization the real sink applies
// before redaction, so tests read exactly what would be written.
type captureLog struct {
	mu   sync.Mutex
	recs []capturedEntry
}

type capturedEntry struct {
	kind  string
	entry map[string]any
}

func (l *captureLog) Append(_ context.Context, kind string, entry any) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs = append(l.recs, capturedEntry{kind, m})
	return nil
}

func (l *captureLog) Close() error { return nil }

func (l *captureLog) last(t *testing.T, kind string) map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, v := range slices.Backward(l.recs) {
		if v.kind == kind {
			return v.entry
		}
	}
	t.Fatalf("no %s record in %+v", kind, l.recs)
	return nil
}

func newConv(t *testing.T) *aggregate.Conversation {
	t.Helper()
	c, err := aggregate.NewConversation("c1")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func prov() *aggregate.Provider {
	p, err := aggregate.NewProvider("p1", "test", "https://api.test", enum.StyleCompletion)
	if err != nil {
		panic(err)
	}
	return &p
}

func TestAttemptRecordsSuccess(t *testing.T) {
	conv := newConv(t)
	u, _ := entity.NewMessage(enum.RoleUser, "hi")
	conv.Append(u)
	prior, _ := valueobject.NewUsage(40, 5, 3, 10)
	conv.AddUsage(prior) // earlier turns — the delta must exclude them

	type tool struct {
		Name string `json:"name"`
	}
	att := NewAttempt(conv, prov(), "m1", true, 512, 2, 1, []tool{{"get_time"}})

	if att.messages != 2 || att.tools != 1 || att.toolSchemaBytes == 0 {
		t.Fatalf("request accounting: %+v", att)
	}
	if att.requestBytes != 512 || att.model != "m1" || !att.stream {
		t.Fatalf("request fields: %+v", att)
	}

	att.MarkFirst()
	first := att.first
	att.MarkFirst() // second mark must not move the sample
	if !att.first.Equal(first) {
		t.Fatal("MarkFirst is not once-only")
	}

	reply, _ := entity.NewMessage(enum.RoleAssistant, "hello")
	reply.SetFinishReason(enum.FinishReasonStop)
	conv.Append(reply)
	delta, _ := valueobject.NewUsage(100, 900, 30, 50)
	conv.AddUsage(delta)

	log := &captureLog{}
	att.LogStats(t.Context(), log, conv, nil)
	rec := log.last(t, "provider.stats")

	if rec["provider"] != "p1" || rec["model"] != "m1" || rec["stream"] != true || rec["status"] != "ok" {
		t.Fatalf("identity fields: %v", rec)
	}
	if rec["status_code"] != float64(200) || rec["finish_reason"] != "stop" {
		t.Fatalf("outcome fields: %v", rec)
	}
	// Deltas, not the cumulative conversation totals.
	if rec["input"] != float64(100) || rec["cache_read"] != float64(900) ||
		rec["cache_write"] != float64(30) || rec["output"] != float64(50) {
		t.Fatalf("usage delta not isolated: %v", rec)
	}
	if rec["request_bytes"] != float64(512) || rec["messages"] != float64(2) ||
		rec["tools"] != float64(1) {
		t.Fatalf("request shape fields: %v", rec)
	}
	if tb, _ := rec["tool_schema_bytes"].(float64); tb <= 0 {
		t.Fatalf("tool_schema_bytes missing: %v", rec)
	}
	if d, _ := rec["duration_ms"].(float64); d < 0 {
		t.Fatalf("duration_ms negative: %v", rec)
	}
	if tf, _ := rec["ttft_ms"].(float64); tf < 0 {
		t.Fatalf("ttft_ms negative: %v", rec)
	}
	if _, leaked := rec["error"]; leaked {
		t.Fatalf("success record carries error: %v", rec)
	}
}

func TestAttemptRecordsError(t *testing.T) {
	conv := newConv(t)
	u, _ := entity.NewMessage(enum.RoleUser, "hi")
	conv.Append(u)

	att := NewAttempt(conv, prov(), "m1", false, 128, 1, 0, nil)
	log := &captureLog{}
	att.LogStats(t.Context(), log, conv, apperror.FromStatus(429, "rate limited", nil))
	rec := log.last(t, "provider.stats")

	if rec["status"] != "error" || rec["status_code"] != float64(429) {
		t.Fatalf("error status: %v", rec)
	}
	if rec["error_kind"] != "too_many_requests" || rec["error"] != "rate limited" {
		t.Fatalf("error detail: %v", rec)
	}
	if rec["finish_reason"] != "" {
		t.Fatalf("failed attempt must not leak a verdict: %v", rec)
	}
	if rec["ttft_ms"] != float64(0) {
		t.Fatalf("unary ttft must be 0: %v", rec)
	}
	// No usage appended — deltas are zero, not negative.
	if rec["input"] != float64(0) || rec["output"] != float64(0) {
		t.Fatalf("phantom usage on failed attempt: %v", rec)
	}
	if rec["tools"] != float64(0) || rec["tool_schema_bytes"] != float64(0) {
		t.Fatalf("empty tool list must report zero: %v", rec)
	}
}

// A failed send that never appended must not inherit an earlier turn's
// finish_reason — the verdict belongs to the message this attempt added.
func TestAttemptDoesNotLeakPriorFinish(t *testing.T) {
	conv := newConv(t)
	reply, _ := entity.NewMessage(enum.RoleAssistant, "earlier")
	reply.SetFinishReason(enum.FinishReasonStop)
	conv.Append(reply)

	att := NewAttempt(conv, prov(), "m1", false, 64, 1, 0, nil)
	log := &captureLog{}
	att.LogStats(t.Context(), log, conv, apperror.New(502, "upstream unreachable", nil))
	rec := log.last(t, "provider.stats")
	if rec["finish_reason"] != "" {
		t.Fatalf("prior turn's verdict leaked: %v", rec)
	}
	if rec["error_kind"] != "bad_gateway" {
		t.Fatalf("error_kind: %v", rec)
	}
}

// A canceled stream commits a partial reply with no verdict — the record
// reports the failure and an empty finish_reason, never "stop".
func TestAttemptPartialCommitHasNoVerdict(t *testing.T) {
	conv := newConv(t)
	u, _ := entity.NewMessage(enum.RoleUser, "hi")
	conv.Append(u)

	att := NewAttempt(conv, prov(), "m1", true, 64, 1, 0, nil)
	partial, _ := entity.NewMessage(enum.RoleAssistant, "cut off")
	conv.Append(partial) // partial commit carries no FinishReason
	pu, _ := valueobject.NewUsage(10, 0, 0, 4)
	conv.AddUsage(pu)

	log := &captureLog{}
	att.LogStats(t.Context(), log, conv, apperror.New(408, "request canceled or timed out", nil))
	rec := log.last(t, "provider.stats")
	if rec["status"] != "error" || rec["status_code"] != float64(408) {
		t.Fatalf("status: %v", rec)
	}
	if rec["error_kind"] != "request_timeout" || rec["finish_reason"] != "" {
		t.Fatalf("partial commit must carry no verdict: %v", rec)
	}
	if rec["input"] != float64(10) || rec["output"] != float64(4) {
		t.Fatalf("partial usage must still count: %v", rec)
	}
}

func TestAttemptLogStatsNeverPanics(t *testing.T) {
	conv := newConv(t)
	att := NewAttempt(conv, prov(), "m1", false, 64, 1, 0, nil)
	att.LogStats(t.Context(), nil, conv, nil) // no logger — silent
	var nilAtt *Attempt
	nilAtt.MarkFirst()
	log := &captureLog{}
	nilAtt.LogStats(t.Context(), log, conv, nil) // nil receiver — silent
	if len(log.recs) != 0 {
		t.Fatal("nothing may be logged")
	}
}

// The record rides through redact.WithoutBodies in production — every key
// must survive it. The usage counters are deliberately named
// input/cache_read/cache_write/output because any key containing "token"
// (or the other credential needles) is masked to "***".
func TestAttemptRecordSurvivesRedaction(t *testing.T) {
	conv := newConv(t)
	reply, _ := entity.NewMessage(enum.RoleAssistant, "ok")
	reply.SetFinishReason(enum.FinishReasonStop)
	conv.Append(reply)
	u, _ := valueobject.NewUsage(1, 2, 3, 4)
	conv.AddUsage(u)

	att := NewAttempt(conv, prov(), "m1", false, 64, 1, 0, nil)
	log := &captureLog{}
	att.LogStats(t.Context(), log, conv, apperror.FromStatus(502, "boom", nil))
	rec := log.last(t, "provider.stats")

	out, ok := redact.WithoutBodies("provider.stats", rec).(map[string]any)
	if !ok {
		t.Fatalf("record must stay a map through the redactor, got %T", redact.WithoutBodies("provider.stats", rec))
	}
	for k, v := range out {
		if s, is := v.(string); is && s == redact.Mask {
			t.Fatalf("key %q masked to %q — rename it or lose the field in real logs", k, redact.Mask)
		}
	}
	if len(out) != len(rec) {
		t.Fatalf("redactor dropped keys: %v → %v", rec, out)
	}
}

func TestAttemptTTFTMeasuresFirstEvent(t *testing.T) {
	conv := newConv(t)
	att := NewAttempt(conv, prov(), "m1", true, 64, 1, 0, nil)
	att.start = att.start.Add(-50 * time.Millisecond)
	att.MarkFirst()
	if got := att.ttft(); got < 50*time.Millisecond {
		t.Fatalf("ttft = %v, want >= 50ms", got)
	}
}
