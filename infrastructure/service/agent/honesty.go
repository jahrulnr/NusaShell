package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	"github.com/jahrulnr/nusashell/infrastructure/service/agent/acp"
)

// classifyPromptError maps an agent prompt's failure onto one class.
// A lost session is not a class: the next prompt opens a new session.
// Anything that is not an agent failure is returned unchanged.
func classifyPromptError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apperror.AgentClass(err); ok {
		return err
	}
	if unknownSession(err) {
		if ae, ok := errors.AsType[apperror.IAppError](err); ok && stockAgentPhrase(ae.Error()) {
			return apperror.New(502, "the agent lost this session", err)
		}
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	status, code := appStatus(err)
	if code == "forbidden_origin" {
		return err
	}
	if errors.Is(err, acp.ErrProcessExited) || strings.Contains(strings.ToLower(err.Error()), "process exited") {
		return apperror.NewAgentFailure(apperror.AgentClassProcess, 502, "upstream_error", failureWords(err), err, 0)
	}
	words := failureWords(err)
	delay := namedDelayOf(err)
	switch status {
	case 401:
		return apperror.NewAgentFailure(apperror.AgentClassCredential, 401, "agent_credential", words, err, 0)
	case 403:
		return apperror.NewAgentFailure(apperror.AgentClassCredential, 403, "agent_credential", words, err, 0)
	case 402:
		return apperror.NewAgentFailure(apperror.AgentClassQuotaExhausted, 402, "quota_exhausted", words, err, 0)
	case 429:
		return apperror.NewAgentFailure(apperror.AgentClassRateLimited, 429, "upstream_error", words, err, delay)
	case 502:
		return classFromWords(words, err, delay)
	case 0:
		if words == "" {
			return err
		}
		return classFromWords(words, err, delay)
	default:
		return err
	}
}

func classFromWords(words string, err error, delay time.Duration) error {
	switch {
	case looksQuota(words):
		return apperror.NewAgentFailure(apperror.AgentClassQuotaExhausted, 402, "quota_exhausted", words, err, 0)
	case looksRate(words):
		return apperror.NewAgentFailure(apperror.AgentClassRateLimited, 429, "upstream_error", words, err, delay)
	case looksCredential(words):
		return apperror.NewAgentFailure(apperror.AgentClassCredential, 403, "agent_credential", words, err, 0)
	default:
		return apperror.NewAgentFailure(apperror.AgentClassUnknown, 502, "upstream_error", words, err, 0)
	}
}

func appStatus(err error) (int, string) {
	var typed apperror.IAppError
	if !errors.As(err, &typed) || typed == nil {
		return 0, ""
	}
	return typed.StatusCode(), typed.Code()
}

func unknownSession(err error) bool {
	for err != nil {
		if acp.IsUnknownSession(err) {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

func failureWords(err error) string {
	var re *acp.RPCError
	if errors.As(err, &re) && strings.TrimSpace(re.Message) != "" {
		return re.Message
	}
	var typed apperror.IAppError
	if errors.As(err, &typed) && typed != nil {
		if !stockAgentPhrase(typed.Error()) {
			return typed.Error()
		}
		if cause := errors.Unwrap(err); cause != nil {
			return failureWords(cause)
		}
		return ""
	}
	if stockAgentPhrase(err.Error()) {
		return ""
	}
	return err.Error()
}

func stockAgentPhrase(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "agent turn failed", "unknown error", "internal error", "agent process exited":
		return true
	default:
		return false
	}
}

func looksRate(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "rate limit") || strings.Contains(l, "too many requests")
}

func looksQuota(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "quota") || strings.Contains(l, "billing") || strings.Contains(l, "insufficient_quota")
}

func looksCredential(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "sign-in") || strings.Contains(l, "signin") || strings.Contains(l, "api key") ||
		strings.Contains(l, "unauthorized") || strings.Contains(l, "credential")
}

func namedDelayOf(err error) time.Duration {
	var delayed interface{ RetryAfter() time.Duration }
	if errors.As(err, &delayed) {
		if d := delayed.RetryAfter(); d > 0 {
			return d
		}
	}
	var re *acp.RPCError
	if !errors.As(err, &re) || len(re.Data) == 0 {
		return 0
	}
	var body struct {
		RetryAfterMs int64 `json:"retryAfterMs"`
		RetryAfter   int64 `json:"retryAfter"`
	}
	if json.Unmarshal(re.Data, &body) != nil {
		return 0
	}
	switch {
	case body.RetryAfterMs > 0:
		return time.Duration(body.RetryAfterMs) * time.Millisecond
	case body.RetryAfter > 0:
		return time.Duration(body.RetryAfter) * time.Second
	default:
		return 0
	}
}
