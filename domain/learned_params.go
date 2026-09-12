package domain

import (
	"strconv"
	"strings"
	"time"

	clock "nusashell/pkg/time"
)

// LearnedParamAction is the recovery action to apply for a learned parameter.
type LearnedParamAction string

const (
	// LearnedActionStrip: the upstream 400 rejected this parameter as
	// unsupported. Strip it from future requests for this provider+model.
	LearnedActionStrip LearnedParamAction = "strip"
	// LearnedActionInject: the upstream 400 rejected the request because a
	// required field was missing. Inject it (with a placeholder or cached
	// value) in future requests for this provider+model.
	LearnedActionInject LearnedParamAction = "inject"
	// LearnedActionDisableModality: the upstream 400 rejected the request
	// because the model is text-only (or lacks a specific modality). The
	// param is the modality name ("vision", "audio", "video"). The retry
	// loop sets the corresponding caps field to false and re-builds
	// messages, which strips the modality's attachments via chatMessages.
	LearnedActionDisableModality LearnedParamAction = "disable_modality"
	// LearnedActionCapContext: the upstream 400 rejected the request because
	// the prompt + max_output combination exceeded the model's actual context
	// window. The param is the context limit in tokens (e.g. "262144").
	// Future turns cap the context window to this value for the provider+model.
	LearnedActionCapContext LearnedParamAction = "cap_context"
	// LearnedActionNudgeUser: the upstream 400 rejected the request because
	// the messages array did not end with a user message. Some
	// providers/models require a user-role message as the final turn. The
	// retry loop injects a minimal user message (".") when this is learned;
	// an existing tool result remains the final turn because it is the active
	// tool continuation.
	LearnedActionNudgeUser LearnedParamAction = "nudge_user"
)

// LearnedParam is a single learned parameter entry for a provider+model
// pair, recovered from an upstream 400 error response.
//
// The registry is persisted to learning/provider_params.json so learned
// adaptations survive process restarts. Entries are monotonically added —
// once learned, a param is never silently unlearned; callers can inspect
// or clear the registry file to reset learning.
type LearnedParam struct {
	Provider string             `json:"provider"`
	Model    string             `json:"model"`
	Param    string             `json:"param"`
	Action   LearnedParamAction `json:"action"`
	// Reason is a short snippet of the upstream error that triggered the
	// learning, for audit/debugging. Capped at 200 chars.
	Reason    string    `json:"reason,omitempty"`
	LearnedAt time.Time `json:"learned_at"`
	// HitCount is incremented each time the learned rule fires. Lets
	// operators see which rules are active vs stale.
	HitCount int `json:"hit_count"`
}

// LearnedParamRegistry is the persisted set of learned parameter rules,
// keyed by "<provider>:<model>:<param>". The registry itself is not
// internally synchronized; concurrent access is guarded by
// learnedParamsCache in the application layer, which holds the only
// registry instance at runtime.
type LearnedParamRegistry struct {
	Entries map[string]*LearnedParam `json:"entries"`
}

func key(provider, model, param string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + ":" +
		strings.ToLower(strings.TrimSpace(model)) + ":" +
		strings.ToLower(strings.TrimSpace(param))
}

// NewLearnedParamRegistry returns an empty registry.
func NewLearnedParamRegistry() *LearnedParamRegistry {
	return &LearnedParamRegistry{Entries: map[string]*LearnedParam{}}
}

// record adds or refreshes a learned entry. Returns the stored entry so
// callers can inspect HitCount. Existing entries keep their LearnedAt but
// bump HitCount when re-observed with the same action.
func (r *LearnedParamRegistry) record(provider, model, param string, action LearnedParamAction, reason string) *LearnedParam {
	if r.Entries == nil {
		r.Entries = map[string]*LearnedParam{}
	}
	k := key(provider, model, param)
	if existing, ok := r.Entries[k]; ok {
		existing.HitCount++
		if reason != "" {
			existing.Reason = truncateReason(reason)
		}
		return existing
	}
	e := &LearnedParam{
		Provider:  strings.ToLower(strings.TrimSpace(provider)),
		Model:     strings.ToLower(strings.TrimSpace(model)),
		Param:     strings.ToLower(strings.TrimSpace(param)),
		Action:    action,
		Reason:    truncateReason(reason),
		LearnedAt: clock.NewTime().Time(),
		HitCount:  1,
	}
	r.Entries[k] = e
	return e
}

// RecordStrip learns that `param` is unsupported for provider+model and
// should be stripped from future requests.
func (r *LearnedParamRegistry) RecordStrip(provider, model, param, reason string) *LearnedParam {
	return r.record(provider, model, param, LearnedActionStrip, reason)
}

// RecordInject learns that `param` is required for provider+model and
// should be injected in future requests.
func (r *LearnedParamRegistry) RecordInject(provider, model, param, reason string) *LearnedParam {
	return r.record(provider, model, param, LearnedActionInject, reason)
}

// RecordDisableModality learns that `param` (a modality name: "vision",
// "audio", "video") is not supported by provider+model and should be
// disabled in future requests. The retry loop sets the corresponding
// caps field to false, which triggers chatMessages to strip attachments
// of that type.
func (r *LearnedParamRegistry) RecordDisableModality(provider, model, param, reason string) *LearnedParam {
	return r.record(provider, model, param, LearnedActionDisableModality, reason)
}

// RecordCapContext learns that `param` (a token count) is the actual context
// window for provider+model and should cap future context-window decisions.
func (r *LearnedParamRegistry) RecordCapContext(provider, model, param, reason string) *LearnedParam {
	return r.record(provider, model, param, LearnedActionCapContext, reason)
}

// RecordNudgeUser learns that provider+model requires a user message at the
// end of the request. The retry loop injects a minimal user message (".")
// when this is learned and the request does not already end in a user turn.
func (r *LearnedParamRegistry) RecordNudgeUser(provider, model, param, reason string) *LearnedParam {
	return r.record(provider, model, param, LearnedActionNudgeUser, reason)
}

// ContextCap returns the smallest learned context-window cap for the
// provider+model, or 0 if no cap has been learned. The cap is stored as a
// parameter string and parsed as an integer.
func (r *LearnedParamRegistry) ContextCap(provider, model string) int {
	if r == nil || r.Entries == nil {
		return 0
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	cap := 0
	for _, e := range r.Entries {
		if e.Action != LearnedActionCapContext || e.Provider != p || e.Model != m {
			continue
		}
		n, err := strconv.Atoi(strings.ReplaceAll(strings.ReplaceAll(e.Param, ",", ""), "_", ""))
		if err != nil || n <= 0 {
			continue
		}
		if cap == 0 || n < cap {
			cap = n
		}
	}
	return cap
}

// StripParams returns the list of params that should be stripped for the
// given provider+model. Returns nil when nothing is learned.
func (r *LearnedParamRegistry) StripParams(provider, model string) []string {
	if r == nil || r.Entries == nil {
		return nil
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	var out []string
	for _, e := range r.Entries {
		if e.Action == LearnedActionStrip && e.Provider == p && e.Model == m {
			out = append(out, e.Param)
		}
	}
	return out
}

// InjectParams returns the list of params that should be injected for the
// given provider+model. Returns nil when nothing is learned.
func (r *LearnedParamRegistry) InjectParams(provider, model string) []string {
	if r == nil || r.Entries == nil {
		return nil
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	var out []string
	for _, e := range r.Entries {
		if e.Action == LearnedActionInject && e.Provider == p && e.Model == m {
			out = append(out, e.Param)
		}
	}
	return out
}

// DisabledModalities returns the list of modality names ("vision", "audio",
// "video") that should be disabled for the given provider+model. Returns
// nil when nothing is learned.
func (r *LearnedParamRegistry) DisabledModalities(provider, model string) []string {
	if r == nil || r.Entries == nil {
		return nil
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	var out []string
	for _, e := range r.Entries {
		if e.Action == LearnedActionDisableModality && e.Provider == p && e.Model == m {
			out = append(out, e.Param)
		}
	}
	return out
}

// NeedsUserNudge reports whether provider+model has learned that requests
// must end with a user message. When true, the application layer injects a
// minimal user message (".") into the request when the messages do not
// already end in a user turn.
func (r *LearnedParamRegistry) NeedsUserNudge(provider, model string) bool {
	if r == nil || r.Entries == nil {
		return false
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	for _, e := range r.Entries {
		if e.Action == LearnedActionNudgeUser && e.Provider == p && e.Model == m {
			return true
		}
	}
	return false
}

// OverrideModel applies learned 400-adaptations to a model's metadata in
// place. It sets Context to the learned cap (when smaller) and disables
// modalities learned to be unsupported. This makes the model's catalog
// metadata reflect what the provider actually accepts, so future turns do
// not have to hit the 400 first. Returns true when at least one field was
// changed.
//
// This only works for models that have catalog metadata (m != nil). Models
// unknown to the catalog are covered separately by
// modelCapabilitiesWithLearned (modalities) and resolveContextWindow
// (context cap) in the application layer; all three are idempotent with
// each other.
func (r *LearnedParamRegistry) OverrideModel(m *Model, provider, model string) bool {
	if m == nil || r == nil {
		return false
	}
	changed := false
	if learnedCap := r.ContextCap(provider, model); learnedCap > 0 {
		if m.Context == 0 || learnedCap < m.Context {
			m.Context = learnedCap
			changed = true
		}
	}
	for _, mod := range r.DisabledModalities(provider, model) {
		switch strings.ToLower(mod) {
		case "vision":
			if m.Vision {
				m.Vision = false
				changed = true
			}
		case "audio":
			if m.Audio {
				m.Audio = false
				changed = true
			}
		case "video":
			if m.Video {
				m.Video = false
				changed = true
			}
		case "document":
			if m.Document {
				m.Document = false
				changed = true
			}
		}
	}
	return changed
}

// Lookup returns the learned entry for a specific provider+model+param, or
// nil when not learned.
func (r *LearnedParamRegistry) Lookup(provider, model, param string) *LearnedParam {
	if r == nil || r.Entries == nil {
		return nil
	}
	return r.Entries[key(provider, model, param)]
}

// BumpHit increments the hit counter for an existing entry. No-op when the
// entry does not exist.
func (r *LearnedParamRegistry) BumpHit(provider, model, param string) {
	if r == nil || r.Entries == nil {
		return
	}
	if e, ok := r.Entries[key(provider, model, param)]; ok {
		e.HitCount++
	}
}

// Remove deletes a learned entry. Returns true when an entry was removed.
func (r *LearnedParamRegistry) Remove(provider, model, param string) bool {
	if r == nil || r.Entries == nil {
		return false
	}
	k := key(provider, model, param)
	if _, ok := r.Entries[k]; !ok {
		return false
	}
	delete(r.Entries, k)
	return true
}

// Sanitize drops entries whose param is a prose stopword — garbage learned
// by older classifier versions (e.g. param="this" from Gemini's "This is
// required" phrasing). Such entries can never apply to a real request and
// only pollute the registry. Returns the number of entries removed.
// Idempotent: a second pass removes nothing.
func (r *LearnedParamRegistry) Sanitize() int {
	if r == nil || r.Entries == nil {
		return 0
	}
	removed := 0
	for k, e := range r.Entries {
		if IsParamStopword(e.Param) {
			delete(r.Entries, k)
			removed++
		}
	}
	return removed
}

// Len returns the number of learned entries.
func (r *LearnedParamRegistry) Len() int {
	if r == nil || r.Entries == nil {
		return 0
	}
	return len(r.Entries)
}

func truncateReason(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// paramStopwords are words that can occupy the capture slot of the
// required-field / missing-field regexes but are never a real parameter
// name — pronouns, determiners, and other function words that appear in
// prose around the actual field ("This is required...", "It must be
// provided"). Capturing one of these is a false positive; the classifier
// rejects it instead of recording garbage like param="this".
//
// Shared by the domain Sanitize policy and the application-layer 400
// classifier (application/provider). Kept here so the list is not
// duplicated; the classifier calls domain.IsParamStopword.
var paramStopwords = map[string]bool{
	"this": true, "that": true, "it": true, "its": true,
	"these": true, "those": true, "there": true, "here": true,
	"a": true, "an": true, "the": true,
	"and": true, "or": true, "but": true, "not": true,
	"is": true, "are": true, "was": true, "were": true, "be": true,
	"he": true, "she": true, "they": true, "we": true, "you": true,
	"his": true, "her": true, "their": true, "our": true, "your": true,
	"which": true, "who": true, "what": true, "when": true,
	"where": true, "why": true, "how": true,
}

// IsParamStopword reports whether a captured identifier is a prose
// function-word rather than a plausible parameter name. Exported so the
// application-layer 400 classifier (application/provider) can share the
// same stopword list without duplicating it. Also used by
// LearnedParamRegistry.Sanitize to drop legacy garbage entries.
func IsParamStopword(param string) bool {
	return paramStopwords[strings.ToLower(strings.TrimSpace(param))]
}
