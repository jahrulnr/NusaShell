package domain

import (
	"errors"
	"strings"
	"time"

	clock "nusashell/pkg/time"
)

// ModelOverride is a manual, field-level correction of a model's catalog
// metadata for a specific provider+model pair. Unlike learned 400
// adaptations (which are reactive and restrict-only — they cap context and
// disable modalities based on upstream errors), an override is assertive
// and bidirectional: it can raise or lower any supported field.
//
// Overrides exist because the models.dev catalog is authoritative for
// capability flags and is re-imported every 4 hours, so any correction
// written directly into the provider's model list is overwritten. This
// layer is applied last at resolve time, after catalog enrichment and
// learned 400 adaptations, so it always wins.
//
// Pointer fields are tri-state: nil means "not overridden" (leave the
// catalog value alone), non-nil means "assert this exact value". This lets
// a single override touch only the fields it corrects.
type ModelOverride struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`

	// Capability flags.
	ToolCall         *bool `json:"tool_call,omitempty"`
	StructuredOutput *bool `json:"structured_output,omitempty"`
	Reasoning        *bool `json:"reasoning,omitempty"`
	Vision           *bool `json:"vision,omitempty"`
	Audio            *bool `json:"audio,omitempty"`
	Video            *bool `json:"video,omitempty"`
	Document         *bool `json:"document,omitempty"`

	// Size fields.
	Context   *int `json:"context,omitempty"`
	MaxOutput *int `json:"max_output,omitempty"`

	// Provenance: who set this and why, for auditing.
	Source    string    `json:"source,omitempty"` // e.g. "review-agent", "user"
	Reason    string    `json:"reason,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ModelOverrideRegistry is the persisted set of manual model overrides,
// keyed by "<provider>:<model>". The registry itself is not internally
// synchronized; concurrent access is guarded by modelOverridesCache in the
// application layer, which holds the only registry instance at runtime.
type ModelOverrideRegistry struct {
	Entries map[string]*ModelOverride `json:"entries"`
}

// NewModelOverrideRegistry returns an empty registry.
func NewModelOverrideRegistry() *ModelOverrideRegistry {
	return &ModelOverrideRegistry{Entries: map[string]*ModelOverride{}}
}

func modelOverrideKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + ":" +
		strings.ToLower(strings.TrimSpace(model))
}

// ValidateModelOverride is the deterministic sandbox gate: every override
// must pass it before entering the registry. It rejects nil/empty targets,
// overrides with no fields set, and non-positive size values. Callers
// (LLM agents, UI) propose; this function decides.
func ValidateModelOverride(o *ModelOverride) error {
	if o == nil {
		return errors.New("override is nil")
	}
	if strings.TrimSpace(o.Provider) == "" {
		return errors.New("provider is required")
	}
	if strings.TrimSpace(o.Model) == "" {
		return errors.New("model is required")
	}
	if !o.hasAnyField() {
		return errors.New("at least one field must be set")
	}
	if o.Context != nil && *o.Context <= 0 {
		return errors.New("context must be positive")
	}
	if o.MaxOutput != nil && *o.MaxOutput <= 0 {
		return errors.New("max_output must be positive")
	}
	return nil
}

func (o *ModelOverride) hasAnyField() bool {
	return o.ToolCall != nil || o.StructuredOutput != nil || o.Reasoning != nil ||
		o.Vision != nil || o.Audio != nil || o.Video != nil || o.Document != nil ||
		o.Context != nil || o.MaxOutput != nil
}

// Set validates and stores (or merges) an override for its provider+model.
// Merging is field-level: only the non-nil fields of the incoming override
// replace the stored ones, so successive corrections accumulate instead of
// clobbering each other. Returns the validation error when the override is
// rejected.
func (r *ModelOverrideRegistry) Set(o *ModelOverride) error {
	if err := ValidateModelOverride(o); err != nil {
		return err
	}
	if r.Entries == nil {
		r.Entries = map[string]*ModelOverride{}
	}
	k := modelOverrideKey(o.Provider, o.Model)
	existing, ok := r.Entries[k]
	if !ok {
		stored := *o
		stored.Provider = strings.ToLower(strings.TrimSpace(o.Provider))
		stored.Model = strings.ToLower(strings.TrimSpace(o.Model))
		stored.UpdatedAt = clock.NewTime().Time()
		r.Entries[k] = &stored
		return nil
	}
	mergeOverrideFields(existing, o)
	existing.UpdatedAt = clock.NewTime().Time()
	return nil
}

// mergeOverrideFields copies every non-nil field from src onto dst.
func mergeOverrideFields(dst, src *ModelOverride) {
	if src.ToolCall != nil {
		dst.ToolCall = src.ToolCall
	}
	if src.StructuredOutput != nil {
		dst.StructuredOutput = src.StructuredOutput
	}
	if src.Reasoning != nil {
		dst.Reasoning = src.Reasoning
	}
	if src.Vision != nil {
		dst.Vision = src.Vision
	}
	if src.Audio != nil {
		dst.Audio = src.Audio
	}
	if src.Video != nil {
		dst.Video = src.Video
	}
	if src.Document != nil {
		dst.Document = src.Document
	}
	if src.Context != nil {
		dst.Context = src.Context
	}
	if src.MaxOutput != nil {
		dst.MaxOutput = src.MaxOutput
	}
	if src.Source != "" {
		dst.Source = src.Source
	}
	if src.Reason != "" {
		dst.Reason = src.Reason
	}
}

// Get returns the override for provider+model, or nil when none exists.
// Lookup is case-insensitive.
func (r *ModelOverrideRegistry) Get(provider, model string) *ModelOverride {
	if r == nil || r.Entries == nil {
		return nil
	}
	return r.Entries[modelOverrideKey(provider, model)]
}

// Remove deletes the override for provider+model. Returns true when an
// entry was removed.
func (r *ModelOverrideRegistry) Remove(provider, model string) bool {
	if r == nil || r.Entries == nil {
		return false
	}
	k := modelOverrideKey(provider, model)
	if _, ok := r.Entries[k]; !ok {
		return false
	}
	delete(r.Entries, k)
	return true
}

// Apply applies the override for provider+model to a model's metadata in
// place. Only non-nil override fields are written. Returns true when at
// least one field changed. Safe on nil receiver.
func (r *ModelOverrideRegistry) Apply(m *Model, provider, model string) bool {
	if r == nil || m == nil {
		return false
	}
	o := r.Get(provider, model)
	if o == nil {
		return false
	}
	return o.Apply(m)
}

// Apply writes the override's non-nil fields onto the model in place.
// Returns true when at least one field changed. Safe on nil receiver.
func (o *ModelOverride) Apply(m *Model) bool {
	if o == nil || m == nil {
		return false
	}
	changed := false
	setBool := func(dst *bool, src *bool) {
		if src != nil && *dst != *src {
			*dst = *src
			changed = true
		}
	}
	setInt := func(dst *int, src *int) {
		if src != nil && *dst != *src {
			*dst = *src
			changed = true
		}
	}
	setBool(&m.ToolCall, o.ToolCall)
	setBool(&m.StructuredOutput, o.StructuredOutput)
	setBool(&m.Reasoning, o.Reasoning)
	setBool(&m.Vision, o.Vision)
	setBool(&m.Audio, o.Audio)
	setBool(&m.Video, o.Video)
	setBool(&m.Document, o.Document)
	setInt(&m.Context, o.Context)
	setInt(&m.MaxOutput, o.MaxOutput)
	return changed
}

// Len returns the number of stored overrides.
func (r *ModelOverrideRegistry) Len() int {
	if r == nil || r.Entries == nil {
		return 0
	}
	return len(r.Entries)
}
