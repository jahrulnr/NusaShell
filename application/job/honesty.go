package job

import (
	"context"
	"errors"
	"sync"
	"time"

	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// AgentHold is the quartet a room carries while an agent failure holds it.
// RetryAt is unix milliseconds, zero when the agent named no delay.
type AgentHold struct {
	Class   string
	Retry   string
	RetryAt int64
	Message string
	Status  int
	Code    string
	Delay   time.Duration
}

// limitedAttempt is the model and upstream a rate limit was about.
// A later send on a different pair is a different attempt.
type limitedAttempt struct {
	model    string
	upstream string
}

// explicitRetry marks a turn.resume. The pause does not hold it.
type explicitRetry struct{}

// releasePause marks ctx as an explicit Retry, which the pause does not hold.
func releasePause(ctx context.Context) context.Context {
	return context.WithValue(ctx, explicitRetry{}, true)
}

func pauseReleased(ctx context.Context) bool {
	v, _ := ctx.Value(explicitRetry{}).(bool)
	return v
}

var (
	agentHolds  sync.Map
	agentPauses struct {
		mu    sync.Mutex
		until map[string]map[limitedAttempt]time.Time
	}
)

// HoldFrom reads the quartet off a classified agent failure. Retry is none:
// a turn that is still retrying overwrites that while it waits. The delay
// is copied only when the agent named one.
func HoldFrom(err error) (AgentHold, bool) {
	class, ok := apperror.AgentClass(err)
	if !ok || err == nil {
		return AgentHold{}, false
	}
	ae := apperror.As(err)
	hold := AgentHold{
		Class:   class,
		Retry:   "none",
		Message: ae.Error(),
		Status:  ae.StatusCode(),
		Code:    ae.Code(),
	}
	var delayed interface{ RetryAfter() time.Duration }
	if errors.As(err, &delayed) {
		if d := delayed.RetryAfter(); d > 0 {
			hold.Delay = d
			hold.RetryAt = time.Now().Add(d).UnixMilli()
		}
	}
	return hold, hold.Class != ""
}

// NoteAgentHold records the hold a later list or detail read projects.
func NoteAgentHold(convID string, hold AgentHold) {
	if convID == "" || hold.Class == "" {
		return
	}
	agentHolds.Store(convID, hold)
}

// ClearAgentHold drops the hold. The room is no longer being held.
func ClearAgentHold(convID string) { agentHolds.Delete(convID) }

// CurrentAgentHold is the hold a list or detail row writes, when one applies.
func CurrentAgentHold(convID string) (AgentHold, bool) {
	v, ok := agentHolds.Load(convID)
	if !ok {
		return AgentHold{}, false
	}
	hold, ok := v.(AgentHold)
	if !ok || hold.Class == "" {
		return AgentHold{}, false
	}
	return hold, true
}

func rememberAgentHold(convID string, err error) {
	if hold, ok := HoldFrom(err); ok {
		NoteAgentHold(convID, hold)
		return
	}
	ClearAgentHold(convID)
}

// rateLimitWait is the shared pause for one rate-limited failure, and
// whether this room retries. A named delay inside 60s replaces the
// backoff. A longer one does not retry. An update, or two retries
// already spent, does not retry either.
func rateLimitWait(delay time.Duration, attempt int, updated bool) (time.Duration, bool) {
	if delay > maxRateLimitWait {
		return maxRateLimitWait, false
	}
	if updated || attempt >= len(sendRetryBackoff) {
		if delay > 0 {
			return delay, false
		}
		return sendRetryBackoff[len(sendRetryBackoff)-1], false
	}
	wait := sendRetryBackoff[attempt]
	if delay > 0 {
		wait = delay
	}
	return wait, true
}

// pauseAgent remembers the model and upstream this rate limit was about,
// and holds later sends of that same pair. A different pair is not held.
func pauseAgent(agentID, model, upstream string, d time.Duration) {
	if agentID == "" || d <= 0 {
		return
	}
	until := time.Now().Add(d)
	key := limitedAttempt{model: model, upstream: upstream}
	agentPauses.mu.Lock()
	if agentPauses.until == nil {
		agentPauses.until = map[string]map[limitedAttempt]time.Time{}
	}
	attempts := agentPauses.until[agentID]
	if attempts == nil {
		attempts = map[limitedAttempt]time.Time{}
		agentPauses.until[agentID] = attempts
	}
	if cur := attempts[key]; until.After(cur) {
		attempts[key] = until
	}
	agentPauses.mu.Unlock()
}

// waitAgentPause holds a send on the same agent, model, and upstream.
// An explicit Retry is not held. A model or upstream that was not the
// limited pair is not held either.
func waitAgentPause(ctx context.Context, agentID, model, upstream string, explicit bool) error {
	if explicit {
		return nil
	}
	key := limitedAttempt{model: model, upstream: upstream}
	for {
		agentPauses.mu.Lock()
		until := time.Time{}
		if agentPauses.until != nil {
			until = agentPauses.until[agentID][key]
		}
		agentPauses.mu.Unlock()
		wait := time.Until(until)
		if wait <= 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return apperror.New(408, "send canceled", ctx.Err())
		case <-timer.C:
		}
	}
}

func agentPauseRemaining(agentID string) time.Duration {
	agentPauses.mu.Lock()
	defer agentPauses.mu.Unlock()
	var until time.Time
	for _, at := range agentPauses.until[agentID] {
		if at.After(until) {
			until = at
		}
	}
	return time.Until(until)
}

func resetAgentHonesty() {
	agentHolds = sync.Map{}
	agentPauses.mu.Lock()
	agentPauses.until = nil
	agentPauses.mu.Unlock()
}
