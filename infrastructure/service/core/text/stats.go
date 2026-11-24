package coretext

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Attempt accumulates the per-request metrics one provider call leaves
// behind: Send constructs it just before the POST (the clock starts there),
// the stream reader calls MarkFirst on the first payload event, and LogStats
// writes the provider.stats record when the attempt settles — success or
// failure. Only request metadata and counters ride here — never bodies,
// headers, or message content — so the record survives log redaction
// intact. Token counters are named input/cache_read/cache_write/output
// rather than *_tokens: the log redactor masks every key containing
// "token".
type Attempt struct {
	provider        string
	model           string
	stream          bool
	requestBytes    int
	messages        int
	tools           int
	toolSchemaBytes int

	start     time.Time
	first     time.Time
	before    valueobject.Usage
	msgBefore int
}

// NewAttempt opens the metrics window for one send. messages is the wire
// message count (body.Messages/Contents/Input — the entries the provider
// parses), toolCount the number of tool schemas the request declares
// (Gemini wraps every declaration in one tools entry, so it is counted
// separately from the wire list), and toolSchemas that wire list — its
// marshaled size is measured here so adapters stay free of byte
// accounting.
func NewAttempt(conv *aggregate.Conversation, prov *aggregate.Provider, model string, stream bool, requestBytes, messages, toolCount int, toolSchemas any) *Attempt {
	a := &Attempt{
		provider:     prov.ID(),
		model:        model,
		stream:       stream,
		requestBytes: requestBytes,
		messages:     messages,
		tools:        toolCount,
		start:        time.Now(),
		before:       conv.Usage(),
		msgBefore:    len(conv.Messages()),
	}
	if toolCount > 0 {
		if raw, err := json.Marshal(toolSchemas); err == nil {
			a.toolSchemaBytes = len(raw)
		}
	}
	return a
}

// MarkFirst stamps the stream's first payload — the TTFT sample. The first
// call wins; keepalive-only deliveries never reach it (readers gate on a
// non-empty event before marking), and a stream that dies before its first
// event leaves ttft at zero, same as a unary call.
func (a *Attempt) MarkFirst() {
	if a == nil || !a.first.IsZero() {
		return
	}
	a.first = time.Now()
}

// LogStats emits the provider.stats record for a settled attempt. err is
// the Send result: nil reports the committed reply's verdict and counters;
// non-nil reports status/error_kind instead — a failed attempt can still
// carry partial usage when a canceled stream committed a partial reply.
// Diagnostics never break a send, so a nil logger or receiver is a no-op.
// A panic unwinding through the caller's defer arrives as a nil err and
// records "ok" — the crash itself is the louder signal there; the record
// only claims a verdict for attempts that returned.
func (a *Attempt) LogStats(ctx context.Context, log drepo.Logger, conv *aggregate.Conversation, err error) {
	if a == nil || log == nil {
		return
	}
	after := conv.Usage()
	rec := map[string]any{
		"provider":          a.provider,
		"model":             a.model,
		"stream":            a.stream,
		"duration_ms":       a.elapsed().Milliseconds(),
		"ttft_ms":           a.ttft().Milliseconds(),
		"request_bytes":     a.requestBytes,
		"messages":          a.messages,
		"tools":             a.tools,
		"tool_schema_bytes": a.toolSchemaBytes,
		"input":             after.InputTokens() - a.before.InputTokens(),
		"cache_read":        after.CacheReadTokens() - a.before.CacheReadTokens(),
		"cache_write":       after.CacheWriteTokens() - a.before.CacheWriteTokens(),
		"output":            after.OutputTokens() - a.before.OutputTokens(),
		"finish_reason":     a.finishReason(conv),
		"status":            "ok",
		"status_code":       http.StatusOK,
	}
	if err != nil {
		appErr := apperror.As(err)
		rec["status"] = "error"
		rec["status_code"] = appErr.StatusCode()
		rec["error_kind"] = errorKind(appErr.StatusCode())
		rec["error"] = appErr.Error()
	}
	_ = log.Append(ctx, "provider.stats", rec)
}

// elapsed is the attempt wall time; a zero start means the window never
// opened and reports 0 rather than a bogus duration.
func (a *Attempt) elapsed() time.Duration {
	if a.start.IsZero() {
		return 0
	}
	return time.Since(a.start)
}

// ttft is the wait for the first stream payload; 0 on unary sends and on
// streams that never delivered an event.
func (a *Attempt) ttft() time.Duration {
	if a.first.IsZero() || a.start.IsZero() {
		return 0
	}
	return a.first.Sub(a.start)
}

// finishReason reads the verdict off the message this attempt appended —
// identified by history growth, not by position, so a failed send that
// appended nothing cannot leak an older turn's finish into its record.
// A partial (canceled) commit carries no verdict and reports "".
func (a *Attempt) finishReason(conv *aggregate.Conversation) string {
	msgs := conv.Messages()
	if len(msgs) <= a.msgBefore {
		return ""
	}
	if last := msgs[len(msgs)-1]; last.Role() == enum.RoleAssistant {
		return last.FinishReason().String()
	}
	return ""
}

// errorKind normalizes an HTTP-style status into a stable grep token —
// "bad_gateway", "too_many_requests", "request_timeout". The full public
// message stays in "error"; this is only the bucket.
func errorKind(code int) string {
	text := http.StatusText(code)
	if text == "" {
		return "error"
	}
	return strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(text))
}
