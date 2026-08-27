package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"
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
		LearnedAt: time.Now().UTC(),
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

// ── 400 error classification ───────────────────────────────────────────
//
// The classifier extracts a parameter name from an upstream 400 error body
// and decides whether the recovery action is to strip (unsupported) or
// inject (required) that parameter. Mirrors OmniRoute's
// detectUnsupportedParam + the "reasoning_content must be passed back"
// pattern.

// unsupportedParamRe matches "Unsupported parameter: X",
// "Unsupported parameter(s): 'X'", "Unsupported parameter 'X'".
var unsupportedParamRe = regexp.MustCompile(`(?i)unsupported\s+parameter\w*(?:\s*\(s\))?\s*[:'"]+\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// requiredFieldRe matches "reasoning_content must be passed back",
// "reasoning_content is required", "field 'reasoning_content' is required".
var requiredFieldRe = regexp.MustCompile(`(?i)(?:field\s+['"]?)?([A-Za-z_][A-Za-z0-9_]*)(?:['"]?)?\s+(?:must be passed back|is required|is a required field|must be provided)`)

// textOnlyModelRe matches "text-only" in error messages from text-only
// models that reject non-text content (images, audio, video). Examples:
//   - "Qwen3.8 open checkpoint is text-only; messages[131].content[1] must be a text part"
//   - "model is text-only; messages[5].content[2] must be a text part"
//   - "this model is text-only and cannot process images"
//
// The param is "vision" because images are the most common non-text
// modality that triggers this error. If audio or video also fail, they
// will be learned separately on subsequent retries.
var textOnlyModelRe = regexp.MustCompile(`(?i)text-only`)

// contextLimitRe matches error bodies that state an explicit context-window
// limit, e.g.:
//
//	"Requested token count exceeds the model's maximum context length of 262144 tokens."
//	"This model's maximum context length is 8192 tokens."
var contextLimitRe = regexp.MustCompile(`(?i)(?:maximum\s+)?context\s+(?:length|window)(?:\s+(?:is|of))?\s*[:=]?\s*(\d[\d_,]*)\b`)

// ExtractContextLimit parses an explicit context-window limit from an
// upstream error body. Returns the limit, the normalized numeric string, and
// ok=true when a number was found.
func ExtractContextLimit(body string) (int, string, bool) {
	if body == "" {
		return 0, "", false
	}
	m := contextLimitRe.FindStringSubmatch(strings.TrimSpace(body))
	if len(m) < 2 {
		return 0, "", false
	}
	num := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(m[1], ",", ""), "_", ""))
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return 0, "", false
	}
	return n, num, true
}

// Classify400Error inspects an upstream 400 error body and returns the
// learned action + parameter name, or (0, "") when the body does not match
// a known pattern.
//
// Order matters:
//  1. "required field" pattern — strongest signal (a model explicitly
//     requiring a field like reasoning_content).
//  2. "text-only" pattern — the model rejects all non-text content; we
//     disable the vision modality (most common trigger) and retry.
//  3. "unsupported parameter" pattern — the model rejects a specific
//     parameter; we strip it and retry.
func Classify400Error(body string) (LearnedParamAction, string) {
	b := strings.TrimSpace(body)
	if b == "" {
		return "", ""
	}
	if m := requiredFieldRe.FindStringSubmatch(b); len(m) > 1 {
		return LearnedActionInject, strings.ToLower(m[1])
	}
	if textOnlyModelRe.MatchString(b) {
		return LearnedActionDisableModality, "vision"
	}
	if _, num, ok := ExtractContextLimit(b); ok {
		return LearnedActionCapContext, num
	}
	if m := unsupportedParamRe.FindStringSubmatch(b); len(m) > 1 {
		return LearnedActionStrip, strings.ToLower(m[1])
	}
	return "", ""
}
