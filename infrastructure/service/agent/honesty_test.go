package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

func TestClassifyPromptErrorKeepsTheAgentsWords(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		class  string
		status int
		code   string
		words  string
		delay  time.Duration
		none   bool
	}{
		{
			name:   "rate limit",
			err:    apperror.New(502, "agent turn failed", &acp.RPCError{Code: acp.CodeInternalError, Message: "rate limit exceeded", Data: []byte(`{"retryAfterMs":1500}`)}),
			class:  apperror.AgentClassRateLimited,
			status: 429,
			code:   "upstream_error",
			words:  "rate limit exceeded",
			delay:  1500 * time.Millisecond,
		},
		{
			name:   "quota",
			err:    apperror.New(502, "agent turn failed", &acp.RPCError{Code: acp.CodeInternalError, Message: "quota exhausted for this billing account"}),
			class:  apperror.AgentClassQuotaExhausted,
			status: 402,
			code:   "quota_exhausted",
			words:  "quota exhausted",
		},
		{
			name:   "sign-in",
			err:    apperror.New(401, "agent requires sign-in — open Settings → Agents to sign in", nil),
			class:  apperror.AgentClassCredential,
			status: 401,
			code:   "agent_credential",
			words:  "sign-in",
		},
		{
			name:   "forbidden",
			err:    apperror.New(403, "invalid api key", nil),
			class:  apperror.AgentClassCredential,
			status: 403,
			code:   "agent_credential",
			words:  "api key",
		},
		{
			name:   "process",
			err:    apperror.New(502, "agent process exited", acp.ErrProcessExited),
			class:  apperror.AgentClassProcess,
			status: 502,
			code:   "upstream_error",
			words:  "process exited",
		},
		{
			name:   "unknown",
			err:    apperror.New(502, "agent turn failed", &acp.RPCError{Code: acp.CodeInternalError, Message: "the compiler exploded"}),
			class:  apperror.AgentClassUnknown,
			status: 502,
			code:   "upstream_error",
			words:  "the compiler exploded",
		},
		{
			name: "lost session",
			err:  apperror.New(502, "agent turn failed", &acp.RPCError{Code: acp.CodeResourceNotFound, Message: "session not found"}),
			none: true,
		},
		{
			name: "model refusal",
			err:  apperror.New(400, "the agent does not offer this model", nil),
			none: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPromptError(tc.err)
			class, ok := apperror.AgentClass(got)
			if tc.none {
				if ok {
					t.Fatalf("class = %s, want none", class)
				}
				if strings.Contains(got.Error(), "agent turn failed") || strings.Contains(got.Error(), "unknown error") {
					t.Fatalf("message = %q", got.Error())
				}
				return
			}
			if !ok || class != tc.class {
				t.Fatalf("class = %q ok=%v, want %s", class, ok, tc.class)
			}
			ae := apperror.As(got)
			if ae.StatusCode() != tc.status || ae.Code() != tc.code {
				t.Fatalf("status/code = %d %q, want %d %q", ae.StatusCode(), ae.Code(), tc.status, tc.code)
			}
			if !strings.Contains(ae.Error(), tc.words) {
				t.Fatalf("message = %q, want it to contain %q", ae.Error(), tc.words)
			}
			if strings.Contains(ae.Error(), "agent turn failed") || ae.Error() == "unknown error" {
				t.Fatalf("message replaced the agent's words: %q", ae.Error())
			}
			var delayed interface{ RetryAfter() time.Duration }
			_ = errors.As(got, &delayed)
			gotDelay := time.Duration(0)
			if delayed != nil {
				gotDelay = delayed.RetryAfter()
			}
			if gotDelay != tc.delay {
				t.Fatalf("delay = %s, want %s", gotDelay, tc.delay)
			}
		})
	}
}

// A prompt the agent rejects is classified on the way out of Prompt.
// Dropping the defer leaves a class-less "agent turn failed".
func TestPromptFailureCarriesAClass(t *testing.T) {
	t.Setenv("FAKE_ACP_PROMPT_ERROR", "quota exhausted on this account")
	rt, in := setup(t, "hello")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := rt.Prompt(ctx, in, nil, nil)
	class, ok := apperror.AgentClass(err)
	if !ok || class != apperror.AgentClassQuotaExhausted {
		t.Fatalf("class = %q ok=%v, err = %v", class, ok, err)
	}
	ae := apperror.As(err)
	if ae.StatusCode() != 402 || ae.Code() != "quota_exhausted" {
		t.Fatalf("status/code = %d %q", ae.StatusCode(), ae.Code())
	}
	if !strings.Contains(ae.Error(), "quota exhausted") || strings.Contains(ae.Error(), "agent turn failed") {
		t.Fatalf("message = %q", ae.Error())
	}
}

// An update before the prompt write does not move the clock, and working
// never appears while promptedAt is absent.
func TestClockStaysPreparingUntilThePromptIsWritten(t *testing.T) {
	var clocks []event.TurnEvent
	sink := newTurnSink(context.Background(), func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventClock {
			clocks = append(clocks, ev)
		}
	}, nil)
	sink.update(usageUpdate(t, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"early"}}`))
	sink.update(usageUpdate(t, `{"sessionUpdate":"usage_update","used":10,"size":100}`))
	if len(clocks) != 0 {
		t.Fatalf("clock before the prompt = %q", clocks[0].Clock())
	}
	sink.markPrompted(1_700_000_000_000)
	if len(clocks) != 1 || clocks[0].Clock() != "waiting" || clocks[0].PromptedAt() != 1_700_000_000_000 || clocks[0].LastUpdateAt() != 0 {
		t.Fatalf("after prompt = clock %q prompted %d updated %d", clocks[0].Clock(), clocks[0].PromptedAt(), clocks[0].LastUpdateAt())
	}
	sink.update(usageUpdate(t, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}`))
	if len(clocks) != 3 {
		t.Fatalf("clocks after an update = %d, want waiting, working, waiting", len(clocks))
	}
	if clocks[1].Clock() != "working" || clocks[1].PromptedAt() == 0 || clocks[1].LastUpdateAt() == 0 {
		t.Fatalf("working = clock %q prompted %d updated %d", clocks[1].Clock(), clocks[1].PromptedAt(), clocks[1].LastUpdateAt())
	}
	if clocks[2].Clock() != "waiting" || clocks[2].PromptedAt() != clocks[1].PromptedAt() || clocks[2].LastUpdateAt() != clocks[1].LastUpdateAt() {
		t.Fatalf("silence = clock %q prompted %d updated %d", clocks[2].Clock(), clocks[2].PromptedAt(), clocks[2].LastUpdateAt())
	}
	if clocks[1].PromptedAt() == clocks[1].LastUpdateAt() && clocks[1].LastUpdateAt() == 1_700_000_000_000 {
		t.Fatal("lastUpdateAt was copied from promptedAt")
	}
	_ = enum.TurnEventClock
}
